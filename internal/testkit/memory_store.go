package testkit

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// MemoryStore is a deterministic, concurrency-safe in-memory implementation
// of ports.Store. It is intended for tests and mock builds, not production.
type MemoryStore struct {
	mu sync.RWMutex
	s  *memoryState
}

type memoryState struct {
	wallets      map[string]domain.Wallet
	addresses    map[string]domain.Address
	balances     map[string]ports.BalanceRecord
	utxos        map[string][]domain.UTXO
	transactions map[string]domain.Tx
	settings     map[string]string
	pending      map[domain.TxID]ports.PendingTx
}

func newMemoryState() *memoryState {
	return &memoryState{
		wallets:      make(map[string]domain.Wallet),
		addresses:    make(map[string]domain.Address),
		balances:     make(map[string]ports.BalanceRecord),
		utxos:        make(map[string][]domain.UTXO),
		transactions: make(map[string]domain.Tx),
		settings:     make(map[string]string),
		pending:      make(map[domain.TxID]ports.PendingTx),
	}
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{s: newMemoryState()}
}

func (m *MemoryStore) Wallets() ports.WalletRepo           { return memoryWalletRepo{m} }
func (m *MemoryStore) Addresses() ports.AddressRepo        { return memoryAddressRepo{m} }
func (m *MemoryStore) Balances() ports.BalanceRepo         { return memoryBalanceRepo{m} }
func (m *MemoryStore) UTXOs() ports.UTXORepo               { return memoryUTXORepo{m} }
func (m *MemoryStore) Transactions() ports.TransactionRepo { return memoryTransactionRepo{m} }
func (m *MemoryStore) Settings() ports.SettingRepo         { return memorySettingRepo{m} }
func (m *MemoryStore) PendingTxs() ports.PendingTxRepo     { return memoryPendingRepo{m} }

// WithTx runs fn against an isolated snapshot. Changes are committed only
// when fn succeeds; the parent store remains locked for the transaction so
// concurrent writes cannot be silently overwritten by the snapshot commit.
func (m *MemoryStore) WithTx(ctx context.Context, fn func(context.Context, ports.Store) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("testkit: nil transaction callback")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	tx := &MemoryStore{s: cloneMemoryState(m.s)}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.s = tx.s
	return nil
}

func cloneMemoryState(src *memoryState) *memoryState {
	dst := newMemoryState()
	for k, v := range src.wallets {
		dst.wallets[k] = v
	}
	for k, v := range src.addresses {
		dst.addresses[k] = v
	}
	for k, v := range src.balances {
		dst.balances[k] = v
	}
	for k, v := range src.utxos {
		dst.utxos[k] = append([]domain.UTXO(nil), v...)
	}
	for k, v := range src.transactions {
		dst.transactions[k] = v
	}
	for k, v := range src.settings {
		dst.settings[k] = v
	}
	for k, v := range src.pending {
		v.Raw = append([]byte(nil), v.Raw...)
		v.Draft = append([]byte(nil), v.Draft...)
		dst.pending[k] = v
	}
	return dst
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("testkit: nil context")
	}
	return ctx.Err()
}

type memoryWalletRepo struct{ m *MemoryStore }

func (r memoryWalletRepo) Create(ctx context.Context, w domain.Wallet) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if w.ID == "" {
		return fmt.Errorf("testkit: wallet id is empty")
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	if _, ok := r.m.s.wallets[w.ID]; ok {
		return fmt.Errorf("testkit: wallet already exists")
	}
	r.m.s.wallets[w.ID] = w
	return nil
}

func (r memoryWalletRepo) Get(ctx context.Context, id string) (domain.Wallet, error) {
	if err := checkContext(ctx); err != nil {
		return domain.Wallet{}, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	w, ok := r.m.s.wallets[id]
	if !ok {
		return domain.Wallet{}, fmt.Errorf("wallet %q: %w", id, domain.ErrNotFound)
	}
	return w, nil
}

func (r memoryWalletRepo) List(ctx context.Context) ([]domain.Wallet, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	out := make([]domain.Wallet, 0, len(r.m.s.wallets))
	for _, w := range r.m.s.wallets {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder == out[j].SortOrder {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].SortOrder < out[j].SortOrder
	})
	return out, nil
}

func (r memoryWalletRepo) Rename(ctx context.Context, id, name string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	w, ok := r.m.s.wallets[id]
	if !ok {
		return fmt.Errorf("wallet %q: %w", id, domain.ErrNotFound)
	}
	w.Name = name
	r.m.s.wallets[id] = w
	return nil
}

func (r memoryWalletRepo) Delete(ctx context.Context, id string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	if _, ok := r.m.s.wallets[id]; !ok {
		return fmt.Errorf("wallet %q: %w", id, domain.ErrNotFound)
	}
	delete(r.m.s.wallets, id)
	for k, a := range r.m.s.addresses {
		if a.WalletID == id {
			delete(r.m.s.addresses, k)
		}
	}
	delete(r.m.s.balances, id)
	delete(r.m.s.utxos, id)
	for k, tx := range r.m.s.transactions {
		if tx.WalletID == id {
			delete(r.m.s.transactions, k)
		}
	}
	return nil
}

type memoryAddressRepo struct{ m *MemoryStore }

func addressKey(a domain.Address) string {
	return string(a.Network) + "\x00" + a.WalletID + "\x00" + a.Address
}

func (r memoryAddressRepo) Upsert(ctx context.Context, addrs []domain.Address) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	for _, a := range addrs {
		r.m.s.addresses[addressKey(a)] = a
	}
	return nil
}

func (r memoryAddressRepo) List(ctx context.Context, walletID string, n domain.Network) ([]domain.Address, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	out := make([]domain.Address, 0)
	for _, a := range r.m.s.addresses {
		if a.WalletID == walletID && a.Network == n {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Chain == out[j].Chain {
			return out[i].Index < out[j].Index
		}
		return out[i].Chain < out[j].Chain
	})
	return out, nil
}

type memoryBalanceRepo struct{ m *MemoryStore }

func balanceKey(walletID string, asset domain.Asset) string {
	return walletID + "\x00" + string(asset)
}

func (r memoryBalanceRepo) Put(ctx context.Context, walletID string, a domain.Asset, b domain.Balance, at time.Time) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	r.m.s.balances[balanceKey(walletID, a)] = ports.BalanceRecord{Balance: b, UpdatedAt: at}
	return nil
}

func (r memoryBalanceRepo) Get(ctx context.Context, walletID string, a domain.Asset) (ports.BalanceRecord, error) {
	if err := checkContext(ctx); err != nil {
		return ports.BalanceRecord{}, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	v, ok := r.m.s.balances[balanceKey(walletID, a)]
	if !ok {
		return ports.BalanceRecord{}, fmt.Errorf("balance: %w", domain.ErrNotFound)
	}
	return v, nil
}

type memoryUTXORepo struct{ m *MemoryStore }

func (r memoryUTXORepo) Replace(ctx context.Context, walletID string, utxos []domain.UTXO) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	r.m.s.utxos[walletID] = append([]domain.UTXO(nil), utxos...)
	return nil
}

func (r memoryUTXORepo) List(ctx context.Context, walletID string) ([]domain.UTXO, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	return append([]domain.UTXO(nil), r.m.s.utxos[walletID]...), nil
}

type memoryTransactionRepo struct{ m *MemoryStore }

func transactionKey(tx domain.Tx) string {
	return tx.WalletID + "\x00" + string(tx.Asset) + "\x00" + string(tx.TxID)
}

func (r memoryTransactionRepo) Upsert(ctx context.Context, txs []domain.Tx) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	for _, tx := range txs {
		r.m.s.transactions[transactionKey(tx)] = tx
	}
	return nil
}

func (r memoryTransactionRepo) List(ctx context.Context, walletID string, asset domain.Asset) ([]domain.Tx, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	out := make([]domain.Tx, 0)
	for _, tx := range r.m.s.transactions {
		if tx.WalletID == walletID && (asset == "" || tx.Asset == asset) {
			out = append(out, tx)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

func (r memoryTransactionRepo) Get(ctx context.Context, walletID string, asset domain.Asset, id domain.TxID) (domain.Tx, error) {
	if err := checkContext(ctx); err != nil {
		return domain.Tx{}, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	tx, ok := r.m.s.transactions[walletID+"\x00"+string(asset)+"\x00"+string(id)]
	if !ok {
		return domain.Tx{}, fmt.Errorf("transaction: %w", domain.ErrNotFound)
	}
	return tx, nil
}

type memorySettingRepo struct{ m *MemoryStore }

func (r memorySettingRepo) Get(ctx context.Context, key string) (string, error) {
	if err := checkContext(ctx); err != nil {
		return "", err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	v, ok := r.m.s.settings[key]
	if !ok {
		return "", fmt.Errorf("setting: %w", domain.ErrNotFound)
	}
	return v, nil
}

func (r memorySettingRepo) Set(ctx context.Context, key, value string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	r.m.s.settings[key] = value
	return nil
}

func sensitiveSettingKey(key string) bool {
	k := strings.ToLower(key)
	for _, marker := range []string{"api_key", "apikey", "token", "secret", "password"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

func (r memorySettingRepo) List(ctx context.Context) (map[string]string, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	out := make(map[string]string)
	for k, v := range r.m.s.settings {
		if !sensitiveSettingKey(k) {
			out[k] = v
		}
	}
	return out, nil
}

type memoryPendingRepo struct{ m *MemoryStore }

func (r memoryPendingRepo) Put(ctx context.Context, p ports.PendingTx) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if p.TxID == "" {
		return fmt.Errorf("testkit: pending transaction id is empty")
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	p.Raw = append([]byte(nil), p.Raw...)
	p.Draft = append([]byte(nil), p.Draft...)
	r.m.s.pending[p.TxID] = p
	return nil
}

func (r memoryPendingRepo) Get(ctx context.Context, id domain.TxID) (ports.PendingTx, error) {
	if err := checkContext(ctx); err != nil {
		return ports.PendingTx{}, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	p, ok := r.m.s.pending[id]
	if !ok {
		return ports.PendingTx{}, fmt.Errorf("pending transaction: %w", domain.ErrNotFound)
	}
	p.Raw = append([]byte(nil), p.Raw...)
	p.Draft = append([]byte(nil), p.Draft...)
	return p, nil
}

func (r memoryPendingRepo) List(ctx context.Context) ([]ports.PendingTx, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	out := make([]ports.PendingTx, 0, len(r.m.s.pending))
	for _, p := range r.m.s.pending {
		p.Raw = append([]byte(nil), p.Raw...)
		p.Draft = append([]byte(nil), p.Draft...)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (r memoryPendingRepo) Delete(ctx context.Context, id domain.TxID) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	if _, ok := r.m.s.pending[id]; !ok {
		return fmt.Errorf("pending transaction: %w", domain.ErrNotFound)
	}
	delete(r.m.s.pending, id)
	return nil
}

var _ ports.Store = (*MemoryStore)(nil)
