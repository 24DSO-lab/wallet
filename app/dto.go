package app

import (
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// DTOs are the only data structures serialized across the Wails boundary.
type SessionDTO struct {
	Locked  bool   `json:"locked"`
	Profile string `json:"profile"`
	Timeout int    `json:"timeout"`
}
type WalletDTO struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Balances []BalanceDTO `json:"balances"`
}
type CreateWalletDTO struct {
	Wallet   WalletDTO `json:"wallet"`
	Mnemonic string    `json:"mnemonic"`
}

type PrepareWalletDTO struct {
	Mnemonic           string `json:"mnemonic"`
	ChallengePositions []int  `json:"challengePositions"`
}
type BalanceDTO struct {
	Asset      string `json:"asset"`
	Network    string `json:"network"`
	Amount     string `json:"amount"`
	PendingAmount string `json:"pendingAmount"`
	Decimals   uint8  `json:"decimals"`
	UpdatedAt  string `json:"updatedAt"`
	FeeAsset   string `json:"feeAsset,omitempty"`
	FeeBalance string `json:"feeBalance,omitempty"`
}
type TxDTO struct {
	ID        string `json:"id"`
	Asset     string `json:"asset"`
	Direction string `json:"direction"`
	Amount    string `json:"amount"`
	Fee       string `json:"fee"`
	FeeAsset  string `json:"feeAsset"`
	ExplorerURL string `json:"explorerUrl,omitempty"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Address   string `json:"address"`
}
type QuoteDTO struct {
	DraftID   string   `json:"draftId"`
	Asset     string   `json:"asset"`
	Network   string   `json:"network"`
	Recipient string   `json:"recipient"`
	Amount    string   `json:"amount"`
	Fee       string   `json:"fee"`
	FeeAsset  string   `json:"feeAsset"`
	Total     string   `json:"total"`
	Warnings  []string `json:"warnings"`
}

func walletDTO(w domain.Wallet, records map[domain.Asset]ports.BalanceRecord) WalletDTO {
	out := WalletDTO{ID: w.ID, Name: w.Name, Balances: make([]BalanceDTO, 0, len(domain.WalletAssets()))}
	for _, asset := range domain.WalletAssets() {
		record, ok := records[asset]
		if !ok {
			continue
		}
		info, ok := asset.Info()
		if !ok {
			continue
		}
		b := BalanceDTO{Asset: string(asset), Network: string(info.Network), Amount: record.Balance.Confirmed.String(), PendingAmount: record.Balance.Pending.String(), Decimals: info.Decimals, UpdatedAt: record.UpdatedAt.UTC().Format(time.RFC3339Nano)}
		if info.FeeAsset != asset {
			if fee, exists := records[info.FeeAsset]; exists {
				b.FeeAsset = string(info.FeeAsset)
				b.FeeBalance = fee.Balance.Confirmed.String()
			}
		}
		out.Balances = append(out.Balances, b)
	}
	return out
}
func txDTO(tx domain.Tx) TxDTO {
	decimals := uint8(0)
	if info, ok := tx.Asset.Info(); ok {
		decimals = info.Decimals
	}
	feeDecimals := decimals
	if info, ok := tx.FeeAsset.Info(); ok {
		feeDecimals = info.Decimals
	}
	return TxDTO{ID: string(tx.TxID), Asset: string(tx.Asset), Direction: string(tx.Direction), Amount: tx.Amount.FormatDecimal(decimals), Fee: tx.Fee.FormatDecimal(feeDecimals), FeeAsset: string(tx.FeeAsset), Status: string(tx.Status), Timestamp: tx.Time.UTC().Format(time.RFC3339Nano), Address: tx.Counterparty}
}
func snapshotDTO(s ports.Snapshot) BalanceDTO {
	info, _ := s.Asset.Info()
	out := BalanceDTO{Asset: string(s.Asset), Network: string(info.Network), Amount: s.Balance.Confirmed.String(), Decimals: info.Decimals, UpdatedAt: s.FetchedAt.UTC().Format(time.RFC3339Nano)}
	if s.FeeAsset != "" {
		out.FeeAsset = string(s.FeeAsset)
		out.FeeBalance = s.FeeBalance.String()
	}
	return out
}
func warningCodes(in []domain.Warning) []string {
	out := make([]string, 0, len(in))
	for _, w := range in {
		out = append(out, string(w.Code))
	}
	return out
}

func feeDecimals(asset domain.Asset, fallback uint8) uint8 {
	if info, ok := asset.Info(); ok {
		return info.Decimals
	}
	return fallback
}
