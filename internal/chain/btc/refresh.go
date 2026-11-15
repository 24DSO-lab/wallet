package btc

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

const (
	AddressGapLimit = 20
	MaxAddressScan  = 10000
)

var (
	ErrMissingXpub       = errors.New("btc: wallet has no account xpub")
	ErrAddressScanLimit  = errors.New("btc: address scan limit reached")
	ErrAddressDerivation = errors.New("btc: address derivation failed")
)

// AddressDeriver is the narrow integration contract for internal/hd. The HD
// package owns BIP32 parsing and child public-key derivation; this adapter only
// requests an address for an account xpub, chain and index.
type AddressDeriver interface {
	DeriveAddress(accountXpub string, profile domain.Profile, chain domain.AddressChain, index uint32) (address, path string, err error)
}

// Scanner performs BIP84 receive/change gap-limit scanning and aggregates an
// Esplora snapshot. It has no access to private keys or seed material.
type Scanner struct {
	api     *Esplora
	derive  AddressDeriver
	profile domain.Profile
	gap     uint32
	maxScan uint32
}

// NewScanner creates a BTC scanner using the given shared Esplora facade and
// the HD package's public-only address deriver.
func NewScanner(api *Esplora, derive AddressDeriver) (*Scanner, error) {
	if api == nil || derive == nil {
		return nil, errors.New("btc: Esplora API and address deriver are required")
	}
	return &Scanner{api: api, derive: derive, profile: api.Profile(), gap: AddressGapLimit, maxScan: MaxAddressScan}, nil
}

// Refresh scans both BIP84 chains until 20 consecutive unused addresses are
// found on each chain. The first unused addresses are included in Snapshot so
// the caller can persist them and display the next receive address.
func (s *Scanner) Refresh(ctx context.Context, wallet domain.PublicWallet, asset domain.Asset) (ports.Snapshot, error) {
	if asset != domain.BTC {
		return ports.Snapshot{}, domain.NewError(domain.CodeInvalidInput, fmt.Errorf("btc: unsupported asset %q", asset))
	}
	if wallet.ID == "" || wallet.BTCXpub == "" {
		return ports.Snapshot{}, domain.NewError(domain.CodeInvalidInput, ErrMissingXpub)
	}

	tip, err := s.api.GetTipHeight(ctx)
	if err != nil {
		return ports.Snapshot{}, err
	}

	result := ports.Snapshot{Asset: domain.BTC, FeeAsset: domain.BTC, FetchedAt: time.Now()}
	addressSet := make(map[string]struct{})
	txByID := make(map[string]EsploraTx)
	confirmedBalance := new(big.Int)
	pendingBalance := new(big.Int)

	for _, chain := range []domain.AddressChain{domain.ChainReceive, domain.ChainChange} {
		addresses, chainConfirmed, chainPending, utxos, txs, err := s.scanChain(ctx, wallet, chain, tip)
		if err != nil {
			return ports.Snapshot{}, err
		}
		result.Addresses = append(result.Addresses, addresses...)
		confirmedBalance.Add(confirmedBalance, chainConfirmed)
		pendingBalance.Add(pendingBalance, chainPending)
		result.UTXOs = append(result.UTXOs, utxos...)
		for _, address := range addresses {
			addressSet[address.Address] = struct{}{}
		}
		for _, tx := range txs {
			txByID[tx.TxID] = tx
		}
	}

	if confirmedBalance.Sign() < 0 || pendingBalance.Sign() < 0 {
		return ports.Snapshot{}, fmt.Errorf("%w: negative wallet balance", ErrMalformedResponse)
	}
	confirmed, err := domain.NewAmount(confirmedBalance)
	if err != nil {
		return ports.Snapshot{}, fmt.Errorf("btc: confirmed balance: %w", err)
	}
	pending, err := domain.NewAmount(pendingBalance)
	if err != nil {
		return ports.Snapshot{}, fmt.Errorf("btc: pending balance: %w", err)
	}
	result.Balance = domain.Balance{Confirmed: confirmed, Pending: pending}

	for _, tx := range txByID {
		history, err := transactionForWallet(wallet.ID, tx, addressSet)
		if err != nil {
			return ports.Snapshot{}, err
		}
		result.History = append(result.History, history)
	}
	sort.Slice(result.History, func(i, j int) bool {
		if result.History[i].Time.Equal(result.History[j].Time) {
			return result.History[i].TxID < result.History[j].TxID
		}
		return result.History[i].Time.After(result.History[j].Time)
	})
	sort.Slice(result.UTXOs, func(i, j int) bool {
		if result.UTXOs[i].TxID != result.UTXOs[j].TxID {
			return result.UTXOs[i].TxID < result.UTXOs[j].TxID
		}
		return result.UTXOs[i].Vout < result.UTXOs[j].Vout
	})
	return result, nil
}

func (s *Scanner) scanChain(ctx context.Context, wallet domain.PublicWallet, chain domain.AddressChain, tip uint64) ([]domain.Address, *big.Int, *big.Int, []domain.UTXO, []EsploraTx, error) {
	addresses := make([]domain.Address, 0, s.gap+1)
	confirmed := new(big.Int)
	pending := new(big.Int)
	utxos := make([]domain.UTXO, 0)
	txs := make([]EsploraTx, 0)
	seenTx := make(map[string]struct{})
	var gap uint32

	for index := uint32(0); index < s.maxScan; index++ {
		address, path, err := s.derive.DeriveAddress(wallet.BTCXpub, s.profile, chain, index)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("%w: %w", ErrAddressDerivation, err)
		}
		if err := ValidateAddress(s.profile, address); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("%w: derived address failed network validation", ErrAddressDerivation)
		}
		info, err := s.api.GetAddress(ctx, address)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		used := info.ChainStats.Funded != 0 || info.ChainStats.Spent != 0 ||
			info.MempoolStats.Funded != 0 || info.MempoolStats.Spent != 0
		addresses = append(addresses, domain.Address{
			WalletID: wallet.ID, Network: domain.NetworkBitcoin, Address: address,
			Path: path, Chain: chain, Index: index, Used: used,
		})

		chainNet := new(big.Int).SetUint64(info.ChainStats.Funded)
		chainNet.Sub(chainNet, new(big.Int).SetUint64(info.ChainStats.Spent))
		mempoolNet := new(big.Int).SetUint64(info.MempoolStats.Funded)
		mempoolNet.Sub(mempoolNet, new(big.Int).SetUint64(info.MempoolStats.Spent))
		confirmed.Add(confirmed, chainNet)
		if mempoolNet.Sign() < 0 {
			confirmed.Add(confirmed, mempoolNet)
		} else {
			pending.Add(pending, mempoolNet)
		}

		if used {
			addressUTXOs, err := s.api.GetUTXOs(ctx, address)
			if err != nil {
				return nil, nil, nil, nil, nil, err
			}
			for _, u := range addressUTXOs {
				confirmations := uint32(0)
				if u.Status.Confirmed {
					if u.Status.BlockHeight == 0 || u.Status.BlockHeight > tip {
						return nil, nil, nil, nil, nil, ErrMalformedResponse
					}
					depth := tip - u.Status.BlockHeight + 1
					if depth > uint64(^uint32(0)) {
						confirmations = ^uint32(0)
					} else {
						confirmations = uint32(depth)
					}
				}
				utxos = append(utxos, domain.UTXO{
					WalletID: wallet.ID, TxID: domain.TxID(u.TxID), Vout: u.Vout,
					Address: address, Amount: domain.AmountFromUint64(u.Value),
					Confirmations: confirmations, Spent: false,
				})
			}

			pageCursor := ""
			for {
				page, err := s.api.GetTransactions(ctx, address, pageCursor)
				if err != nil {
					return nil, nil, nil, nil, nil, err
				}
				for _, tx := range page {
					if _, exists := seenTx[tx.TxID]; !exists {
						seenTx[tx.TxID] = struct{}{}
						txs = append(txs, tx)
					}
				}
				if len(page) < 25 {
					break
				}
				pageCursor = page[len(page)-1].TxID
			}
		}

		if used {
			gap = 0
		} else {
			gap++
			if gap >= s.gap {
				return addresses, confirmed, pending, utxos, txs, nil
			}
		}
	}
	return nil, nil, nil, nil, nil, ErrAddressScanLimit
}

func transactionForWallet(walletID string, tx EsploraTx, walletAddresses map[string]struct{}) (domain.Tx, error) {
	walletInputs := new(big.Int)
	walletOutputs := new(big.Int)
	externalOutputs := new(big.Int)
	externalInputs := new(big.Int)
	counterparty := ""

	for _, input := range tx.Vin {
		if input.Prevout == nil {
			continue
		}
		if _, ours := walletAddresses[input.Prevout.ScriptPubKeyAddress]; ours {
			walletInputs.Add(walletInputs, new(big.Int).SetUint64(input.Prevout.Value))
		} else {
			externalInputs.Add(externalInputs, new(big.Int).SetUint64(input.Prevout.Value))
			if counterparty == "" {
				counterparty = input.Prevout.ScriptPubKeyAddress
			}
		}
	}
	for _, output := range tx.Vout {
		if _, ours := walletAddresses[output.ScriptPubKeyAddress]; ours {
			walletOutputs.Add(walletOutputs, new(big.Int).SetUint64(output.Value))
		} else {
			externalOutputs.Add(externalOutputs, new(big.Int).SetUint64(output.Value))
			if counterparty == "" {
				counterparty = output.ScriptPubKeyAddress
			}
		}
	}

	direction := domain.DirectionSelf
	amount := new(big.Int).Set(walletOutputs)
	fee := uint64(0)
	switch {
	case walletInputs.Sign() == 0 && walletOutputs.Sign() > 0:
		direction = domain.DirectionIn
	case externalOutputs.Sign() > 0:
		direction = domain.DirectionOut
		amount.Set(externalOutputs)
		fee = tx.Fee
	case walletInputs.Sign() > 0 && externalOutputs.Sign() == 0:
		direction = domain.DirectionSelf
	}
	amountValue, err := domain.NewAmount(amount)
	if err != nil {
		return domain.Tx{}, fmt.Errorf("%w: invalid transaction amount", ErrMalformedResponse)
	}
	feeValue := domain.AmountFromUint64(fee)
	status := domain.TxPending
	var at time.Time
	if tx.Status.Confirmed {
		status = domain.TxConfirmed
		if tx.Status.BlockTime > 0 {
			at = time.Unix(int64(tx.Status.BlockTime), 0)
		}
	}
	return domain.Tx{
		WalletID: walletID, Asset: domain.BTC, TxID: domain.TxID(tx.TxID),
		Direction: direction, Amount: amountValue, Fee: feeValue, FeeAsset: domain.BTC,
		Status: status, BlockHeight: tx.Status.BlockHeight, Time: at, Counterparty: counterparty,
	}, nil
}
