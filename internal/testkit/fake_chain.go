package testkit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
)

// ChainOperation identifies an injectable failure point in FakeChain.
type ChainOperation string

const (
	ChainRefresh   ChainOperation = "refresh"
	ChainPrepare   ChainOperation = "prepare"
	ChainVerify    ChainOperation = "verify"
	ChainFinalize  ChainOperation = "finalize"
	ChainBroadcast ChainOperation = "broadcast"
	ChainStatus    ChainOperation = "status"
)

// FakeChainOptions configures canned responses and deterministic failures.
type FakeChainOptions struct {
	Network         domain.Network
	Snapshot        ports.Snapshot
	Prepared        ports.Prepared
	Signed          ports.Signed
	TxID            domain.TxID
	Status          ports.TxStatus
	ValidateAddress func(string) error
	AddressFromPublicKey func([]byte) (string, error)
	VerifyFunc      func(ports.Prepared, domain.SendIntent) error
	Delay           time.Duration
	Failures        map[ChainOperation]error
}

// FakeChain is a deterministic, in-memory implementation of ports.ChainAdapter.
// It never performs network I/O.
type FakeChain struct {
	mu         sync.RWMutex
	opts       FakeChainOptions
	failures   map[ChainOperation]error
}

// NewFakeChain creates a fake adapter for one network.
func NewFakeChain(opts FakeChainOptions) *FakeChain {
	failures := make(map[ChainOperation]error, len(opts.Failures))
	for op, err := range opts.Failures {
		failures[op] = err
	}
	opts.Snapshot = cloneSnapshot(opts.Snapshot)
	opts.Prepared = clonePrepared(opts.Prepared)
	opts.Signed.Raw = append([]byte(nil), opts.Signed.Raw...)
	return &FakeChain{opts: opts, failures: failures}
}

func (c *FakeChain) before(ctx context.Context, op ChainOperation) error {
	if ctx == nil {
		return errors.New("testkit: nil context")
	}
	c.mu.RLock()
	delay, injected := c.opts.Delay, c.failures[op]
	c.mu.RUnlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	} else if err := ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return injected
}

// SetFailure changes the injected error for subsequent calls.
func (c *FakeChain) SetFailure(op ChainOperation, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		delete(c.failures, op)
		return
	}
	c.failures[op] = err
}

// SetDelay changes the artificial latency for subsequent calls.
func (c *FakeChain) SetDelay(delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opts.Delay = delay
}

// SetSnapshot replaces the canned refresh result.
func (c *FakeChain) SetSnapshot(snapshot ports.Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opts.Snapshot = cloneSnapshot(snapshot)
}

// SetPrepared replaces the canned transaction preparation result.
func (c *FakeChain) SetPrepared(prepared ports.Prepared) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opts.Prepared = clonePrepared(prepared)
}

func (c *FakeChain) Network() domain.Network { return c.opts.Network }

func (c *FakeChain) ValidateAddress(addr string) error {
	if c.opts.ValidateAddress != nil {
		return c.opts.ValidateAddress(addr)
	}
	if addr == "" {
		return domain.NewError(domain.CodeInvalidAddress, errors.New("testkit: empty address"))
	}
	return nil
}

func (c *FakeChain) AddressFromPublicKey(pub []byte) (string, error) {
	if c.opts.AddressFromPublicKey != nil {
		return c.opts.AddressFromPublicKey(append([]byte(nil), pub...))
	}
	if len(pub) == 0 {
		return "", errors.New("testkit: empty public key")
	}
	return fmt.Sprintf("%s:%x", c.opts.Network, pub), nil
}

func (c *FakeChain) Refresh(ctx context.Context, wallet domain.PublicWallet, asset domain.Asset) (ports.Snapshot, error) {
	if err := c.before(ctx, ChainRefresh); err != nil {
		return ports.Snapshot{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneSnapshot(c.opts.Snapshot), nil
}

func (c *FakeChain) Prepare(ctx context.Context, in domain.SendIntent) (ports.Prepared, error) {
	if err := c.before(ctx, ChainPrepare); err != nil {
		return ports.Prepared{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return clonePrepared(c.opts.Prepared), nil
}

func (c *FakeChain) Verify(prepared ports.Prepared, in domain.SendIntent) error {
	if err := c.before(context.Background(), ChainVerify); err != nil {
		return err
	}
	if c.opts.VerifyFunc != nil {
		return c.opts.VerifyFunc(clonePrepared(prepared), in)
	}
	if prepared.Network != c.opts.Network || prepared.Asset != in.Asset {
		return domain.NewError(domain.CodeVerificationFailed, errors.New("testkit: prepared transaction mismatch"))
	}
	return nil
}

func (c *FakeChain) Finalize(prepared ports.Prepared, sigs []ports.Signature) (ports.Signed, error) {
	if err := c.before(context.Background(), ChainFinalize); err != nil {
		return ports.Signed{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.opts.Signed.Network != "" || c.opts.Signed.TxID != "" || len(c.opts.Signed.Raw) != 0 {
		out := c.opts.Signed
		out.Raw = append([]byte(nil), out.Raw...)
		return out, nil
	}
	return ports.Signed{
		Network: prepared.Network,
		TxID:    "fake-txid",
		Raw:     []byte("fake-signed-transaction"),
	}, nil
}

func (c *FakeChain) Broadcast(ctx context.Context, signed ports.Signed) (domain.TxID, error) {
	if err := c.before(ctx, ChainBroadcast); err != nil {
		return "", err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.opts.TxID != "" {
		return c.opts.TxID, nil
	}
	if c.opts.Signed.TxID != "" {
		return c.opts.Signed.TxID, nil
	}
	return "fake-txid", nil
}

func (c *FakeChain) Status(ctx context.Context, id domain.TxID) (ports.TxStatus, error) {
	if err := c.before(ctx, ChainStatus); err != nil {
		return ports.TxStatus{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.opts.Status, nil
}

func cloneSnapshot(in ports.Snapshot) ports.Snapshot {
	out := in
	out.History = append([]domain.Tx(nil), in.History...)
	out.Addresses = append([]domain.Address(nil), in.Addresses...)
	out.UTXOs = append([]domain.UTXO(nil), in.UTXOs...)
	return out
}

func clonePrepared(in ports.Prepared) ports.Prepared {
	out := in
	out.Draft = append([]byte(nil), in.Draft...)
	out.Digests = append([]ports.DigestRequest(nil), in.Digests...)
	out.Warnings = append([]domain.Warning(nil), in.Warnings...)
	return out
}

var _ ports.ChainAdapter = (*FakeChain)(nil)
