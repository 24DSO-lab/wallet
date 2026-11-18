package btc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/provider"
)

var (
	ErrMalformedResponse = errors.New("btc: malformed Esplora response")
	ErrInvalidTxID       = errors.New("btc: invalid transaction id")
)

// Esplora is the small subset of the Esplora HTTP API used by the Bitcoin
// adapter. It deliberately contains no HTTP implementation: all requests go
// through provider.Client for timeouts, retries, fallback and rate limiting.
type Esplora struct {
	client  provider.Client
	profile domain.Profile
}

// NewEsplora creates an Esplora API facade for the application's active
// network profile. The provider.Client must be configured with the matching
// profile endpoints from internal/config.
func NewEsplora(client provider.Client, profile domain.Profile) (*Esplora, error) {
	if client == nil {
		return nil, errors.New("btc: provider client is nil")
	}
	if !profile.Valid() {
		return nil, fmt.Errorf("btc: invalid profile %q", profile)
	}
	return &Esplora{client: client, profile: profile}, nil
}

// Profile returns the network profile bound to this Esplora client.
func (e *Esplora) Profile() domain.Profile { return e.profile }

// AddressStats mirrors the confirmed and mempool counters returned by
// GET /address/:address.
type AddressStats struct {
	Funded uint64
	Spent  uint64
}

type AddressInfo struct {
	ChainStats   AddressStats
	MempoolStats AddressStats
}

type EsploraUTXO struct {
	TxID   string
	Vout   uint32
	Value  uint64
	Status TxStatus
}

type TxStatus struct {
	Confirmed   bool
	BlockHeight uint64
	BlockTime   uint64
}

type FeeEstimates map[string]float64

type EsploraInput struct {
	IsCoinbase bool `json:"is_coinbase"`
	Prevout    *struct {
		ScriptPubKeyAddress string `json:"scriptpubkey_address"`
		Value               uint64 `json:"value"`
	} `json:"prevout"`
}

type EsploraOutput struct {
	ScriptPubKeyAddress string `json:"scriptpubkey_address"`
	Value               uint64 `json:"value"`
}

type EsploraTxStatus struct {
	Confirmed   bool   `json:"confirmed"`
	BlockHeight uint64 `json:"block_height"`
	BlockTime   uint64 `json:"block_time"`
}

type EsploraTx struct {
	TxID      string            `json:"txid"`
	Version   int32             `json:"version"`
	LockTime  uint32            `json:"locktime"`
	Size      uint32            `json:"size"`
	Weight    uint32            `json:"weight"`
	Fee       uint64            `json:"fee"`
	Status    EsploraTxStatus   `json:"status"`
	Vin       []EsploraInput    `json:"vin"`
	Vout      []EsploraOutput   `json:"vout"`
}

// GetAddress fetches confirmed and unconfirmed address counters.
func (e *Esplora) GetAddress(ctx context.Context, address string) (AddressInfo, error) {
	if err := ValidateAddress(e.profile, address); err != nil {
		return AddressInfo{}, err
	}
	var raw struct {
		ChainStats *struct {
			Funded *uint64 `json:"funded_txo_sum"`
			Spent  *uint64 `json:"spent_txo_sum"`
		} `json:"chain_stats"`
		MempoolStats *struct {
			Funded *uint64 `json:"funded_txo_sum"`
			Spent  *uint64 `json:"spent_txo_sum"`
		} `json:"mempool_stats"`
	}
	if err := e.getJSON(ctx, "/address/"+url.PathEscape(address), &raw); err != nil {
		return AddressInfo{}, err
	}
	if raw.ChainStats == nil || raw.MempoolStats == nil ||
		raw.ChainStats.Funded == nil || raw.ChainStats.Spent == nil ||
		raw.MempoolStats.Funded == nil || raw.MempoolStats.Spent == nil {
		return AddressInfo{}, ErrMalformedResponse
	}
	// Confirmed outputs can only be spent after they have been funded to this
	// address. Unlike mempool counters, confirmed spent totals cannot include
	// spends of outputs funded by an unconfirmed transaction.
	if *raw.ChainStats.Spent > *raw.ChainStats.Funded {
		return AddressInfo{}, ErrMalformedResponse
	}
	return AddressInfo{
		ChainStats: AddressStats{Funded: *raw.ChainStats.Funded, Spent: *raw.ChainStats.Spent},
		MempoolStats: AddressStats{Funded: *raw.MempoolStats.Funded, Spent: *raw.MempoolStats.Spent},
	}, nil
}

// GetUTXOs fetches unspent outputs for a validated address.
func (e *Esplora) GetUTXOs(ctx context.Context, address string) ([]EsploraUTXO, error) {
	if err := ValidateAddress(e.profile, address); err != nil {
		return nil, err
	}
	var raw []struct {
		TxID  string `json:"txid"`
		Vout  uint32 `json:"vout"`
		Value uint64 `json:"value"`
		Status *struct {
			Confirmed   *bool  `json:"confirmed"`
			BlockHeight uint64 `json:"block_height"`
			BlockTime   uint64 `json:"block_time"`
		} `json:"status"`
	}
	if err := e.getJSONArray(ctx, "/address/"+url.PathEscape(address)+"/utxo", &raw); err != nil {
		return nil, err
	}
	out := make([]EsploraUTXO, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, u := range raw {
		if !validTxID(u.TxID) || u.Status == nil || u.Status.Confirmed == nil {
			return nil, ErrMalformedResponse
		}
		txid := strings.ToLower(u.TxID)
		key := fmt.Sprintf("%s:%d", txid, u.Vout)
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrMalformedResponse
		}
		seen[key] = struct{}{}
		if *u.Status.Confirmed && (u.Status.BlockHeight == 0 || u.Status.BlockTime == 0) {
			return nil, ErrMalformedResponse
		}
		out = append(out, EsploraUTXO{
			TxID: txid, Vout: u.Vout, Value: u.Value,
			Status: TxStatus{Confirmed: *u.Status.Confirmed, BlockHeight: u.Status.BlockHeight, BlockTime: u.Status.BlockTime},
		})
	}
	return out, nil
}

// GetTransactions fetches recent transactions involving a validated address.
// Esplora paginates after 25 results using /address/:address/txs/chain/:last_seen_txid.
func (e *Esplora) GetTransactions(ctx context.Context, address, lastSeenTxID string) ([]EsploraTx, error) {
	if err := ValidateAddress(e.profile, address); err != nil {
		return nil, err
	}
	path := "/address/" + url.PathEscape(address) + "/txs"
	if lastSeenTxID != "" {
		if !validTxID(lastSeenTxID) {
			return nil, ErrInvalidTxID
		}
		path += "/chain/" + lastSeenTxID
	}
	var out []EsploraTx
	if err := e.getJSONArray(ctx, path, &out); err != nil {
		return nil, err
	}
	for i := range out {
		if !validTxID(out[i].TxID) {
			return nil, ErrMalformedResponse
		}
	}
	return out, nil
}

// GetFeeEstimates returns Esplora's target-block to sat/vB map.
func (e *Esplora) GetFeeEstimates(ctx context.Context) (FeeEstimates, error) {
	var fees FeeEstimates
	if err := e.getJSON(ctx, "/fee-estimates", &fees); err != nil {
		return nil, err
	}
	if len(fees) == 0 {
		return nil, ErrMalformedResponse
	}
	for target, rate := range fees {
		n, err := strconv.Atoi(target)
		if err != nil || n < 1 || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return nil, ErrMalformedResponse
		}
	}
	return fees, nil
}

// GetTransaction fetches a transaction by txid.
func (e *Esplora) GetTransaction(ctx context.Context, txid string) (EsploraTx, error) {
	if !validTxID(txid) {
		return EsploraTx{}, ErrInvalidTxID
	}
	var tx EsploraTx
	if err := e.getJSON(ctx, "/tx/"+txid, &tx); err != nil {
		return EsploraTx{}, err
	}
	if !validTxID(tx.TxID) || !strings.EqualFold(tx.TxID, txid) {
		return EsploraTx{}, ErrMalformedResponse
	}
	return tx, nil
}

// GetTransactionStatus fetches confirmation status by txid.
func (e *Esplora) GetTransactionStatus(ctx context.Context, txid string) (TxStatus, error) {
	if !validTxID(txid) {
		return TxStatus{}, ErrInvalidTxID
	}
	var status EsploraTxStatus
	if err := e.getJSON(ctx, "/tx/"+txid+"/status", &status); err != nil {
		return TxStatus{}, err
	}
	return TxStatus(status), nil
}

// BroadcastRaw sends a raw transaction exactly once. provider.Client must not
// retry this non-idempotent POST because a network failure leaves the outcome
// unknown; callers can reconcile by querying the locally calculated txid.
func (e *Esplora) BroadcastRaw(ctx context.Context, raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("btc: raw transaction is empty")
	}
	resp, err := e.client.Do(ctx, provider.Request{
		Method: http.MethodPost,
		Path:   "/tx",
		Header: http.Header{"Content-Type": []string{"text/plain"}},
		Body:   append([]byte(nil), raw...),
	})
	if err != nil {
		return "", err
	}
	txid := strings.TrimSpace(string(resp.Body))
	if !validTxID(txid) {
		return "", fmt.Errorf("%w: broadcast returned invalid txid", ErrMalformedResponse)
	}
	return strings.ToLower(txid), nil
}

func (e *Esplora) getJSON(ctx context.Context, path string, dst any) error {
	resp, err := e.client.Do(ctx, provider.Request{Method: http.MethodGet, Path: path})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(resp.Body, dst); err != nil {
		return fmt.Errorf("%w: invalid JSON", ErrMalformedResponse)
	}
	return nil
}

// getJSONArray rejects JSON null and non-array values. encoding/json accepts
// null when decoding into a slice, which would otherwise hide an invalid
// provider response as a valid empty result.
func (e *Esplora) getJSONArray(ctx context.Context, path string, dst any) error {
	resp, err := e.client.Do(ctx, provider.Request{Method: http.MethodGet, Path: path})
	if err != nil {
		return err
	}
	body := bytes.TrimSpace(resp.Body)
	if len(body) == 0 || body[0] != '[' {
		return ErrMalformedResponse
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%w: invalid JSON array", ErrMalformedResponse)
	}
	return nil
}

func validTxID(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}


// ConservativeFeeRate returns the provider's estimate for confirmation within
// three blocks. If that target is absent, it uses the fastest valid target
// supplied by the provider rather than silently choosing an arbitrarily slow
// estimate.
func ConservativeFeeRate(estimates FeeEstimates) (float64, error) {
	if rate, ok := estimates["3"]; ok && rate > 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0) {
		return rate, nil
	}
	bestTarget := 0
	var bestRate float64
	for target, rate := range estimates {
		n, err := strconv.Atoi(target)
		if err != nil || n < 1 || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			continue
		}
		if bestTarget == 0 || n < bestTarget {
			bestTarget, bestRate = n, rate
		}
	}
	if bestTarget == 0 {
		return 0, ErrMalformedResponse
	}
	return bestRate, nil
}


// GetTipHeight returns the current best-chain height.
func (e *Esplora) GetTipHeight(ctx context.Context) (uint64, error) {
	resp, err := e.client.Do(ctx, provider.Request{Method: http.MethodGet, Path: "/blocks/tip/height"})
	if err != nil {
		return 0, err
	}
	height, err := strconv.ParseUint(strings.TrimSpace(string(resp.Body)), 10, 64)
	if err != nil {
		return 0, ErrMalformedResponse
	}
	return height, nil
}


// HasSpentOutputs checks every output using Esplora's outspends endpoint.
// RBF is allowed only when the response accounts for every transaction output;
// incomplete or malformed provider data must fail closed.
func (e *Esplora) HasSpentOutputs(ctx context.Context, txid string) (bool, error) {
	if !validTxID(txid) {
		return false, ErrInvalidTxID
	}
	tx, err := e.GetTransaction(ctx, txid)
	if err != nil {
		return false, err
	}
	if len(tx.Vout) == 0 {
		return false, ErrMalformedResponse
	}

	var out []struct {
		Spent *bool  `json:"spent"`
		TxID  string `json:"txid"`
	}
	if err := e.getJSON(ctx, "/tx/"+txid+"/outspends", &out); err != nil {
		return false, err
	}
	if len(out) != len(tx.Vout) {
		return false, ErrMalformedResponse
	}
	for _, item := range out {
		if item.Spent == nil {
			return false, ErrMalformedResponse
		}
		if *item.Spent {
			return true, nil
		}
		if item.TxID != "" {
			return false, ErrMalformedResponse
		}
	}
	return false, nil
}
