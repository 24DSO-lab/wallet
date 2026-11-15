package testkit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/ports"
	"github.com/24DSO-lab/wallet/internal/secret"
)

// KeystoreOperation identifies an injectable failure point in FakeKeystore.
type KeystoreOperation string

const (
	KeystoreNewMnemonic KeystoreOperation = "new_mnemonic"
	KeystoreSave        KeystoreOperation = "save"
	KeystoreVerify      KeystoreOperation = "verify"
	KeystoreChangePassword KeystoreOperation = "change_password"
	KeystoreRevealSeed  KeystoreOperation = "reveal_seed"
	KeystoreDelete      KeystoreOperation = "delete"
	KeystoreSign        KeystoreOperation = "sign"
)

// FakeKeystoreOptions configures deterministic keystore behavior.
// Mnemonic and Password are copied during construction. The default mnemonic
// is a public BIP-39 test vector and MUST NOT be used with real funds.
type FakeKeystoreOptions struct {
	Mnemonic  []byte
	Password  []byte
	PublicKeys ports.PublicKeys
	Signatures []ports.Signature
	AddressFor func(domain.Network, []byte) (string, error)
	Delay     time.Duration
	Failures  map[KeystoreOperation]error
}

// FakeKeystore is a deterministic, in-memory implementation of ports.Keystore.
// It is suitable only for tests and mock builds.
type FakeKeystore struct {
	mu         sync.Mutex
	mnemonic   secret.Bytes
	password   secret.Bytes
	wallets    map[string]secret.Bytes
	publicKeys ports.PublicKeys
	signatures []ports.Signature
	addressFor func(domain.Network, []byte) (string, error)
	delay      time.Duration
	failures   map[KeystoreOperation]error
}

// NewFakeKeystore creates a fake keystore. An empty configured password means
// the first successful Save establishes the application password.
func NewFakeKeystore(opts FakeKeystoreOptions) *FakeKeystore {
	mnemonic := opts.Mnemonic
	if len(mnemonic) == 0 {
		mnemonic = []byte("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
	}
	failures := make(map[KeystoreOperation]error, len(opts.Failures))
	for op, err := range opts.Failures {
		failures[op] = err
	}
	return &FakeKeystore{
		mnemonic:   secret.Copy(mnemonic),
		password:   secret.Copy(opts.Password),
		wallets:    make(map[string]secret.Bytes),
		publicKeys: opts.PublicKeys,
		signatures: cloneSignatures(opts.Signatures),
		addressFor: opts.AddressFor,
		delay:      opts.Delay,
		failures:   failures,
	}
}

func (k *FakeKeystore) before(ctx context.Context, op KeystoreOperation) error {
	if ctx == nil {
		return errors.New("testkit: nil context")
	}
	k.mu.Lock()
	delay, injected := k.delay, k.failures[op]
	k.mu.Unlock()
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
func (k *FakeKeystore) SetFailure(op KeystoreOperation, err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err == nil {
		delete(k.failures, op)
		return
	}
	k.failures[op] = err
}

// SetDelay changes the artificial latency for subsequent calls.
func (k *FakeKeystore) SetDelay(delay time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.delay = delay
}

func (k *FakeKeystore) NewMnemonic(words int) (secret.Bytes, error) {
	if err := k.before(context.Background(), KeystoreNewMnemonic); err != nil {
		return secret.Bytes{}, err
	}
	if words != 12 && words != 24 {
		return secret.Bytes{}, fmt.Errorf("testkit: mnemonic word count must be 12 or 24")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.mnemonic.Clone(), nil
}

func (k *FakeKeystore) Save(ctx context.Context, id string, mnemonic secret.Bytes, s ports.Secrets) (ports.PublicKeys, error) {
	if err := k.before(ctx, KeystoreSave); err != nil {
		return ports.PublicKeys{}, err
	}
	if id == "" || mnemonic.IsEmpty() {
		return ports.PublicKeys{}, errors.New("testkit: wallet id and mnemonic are required")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.password.IsEmpty() && !k.password.Equal(s.Password) {
		return ports.PublicKeys{}, domain.NewError(domain.CodeWrongPassword, errors.New("testkit: password mismatch"))
	}
	if k.password.IsEmpty() {
		k.password = s.Password.Clone()
	}
	if old, ok := k.wallets[id]; ok {
		old.Wipe()
	}
	k.wallets[id] = mnemonic.Clone()
	return k.publicKeys, nil
}

func (k *FakeKeystore) Verify(ctx context.Context, s ports.Secrets) error {
	if err := k.before(ctx, KeystoreVerify); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.verifyLocked(s)
}

func (k *FakeKeystore) verifyLocked(s ports.Secrets) error {
	if k.password.IsEmpty() || !k.password.Equal(s.Password) {
		return domain.NewError(domain.CodeWrongPassword, errors.New("testkit: password mismatch"))
	}
	return nil
}

func (k *FakeKeystore) ChangePassword(ctx context.Context, oldPwd, newPwd secret.Bytes) error {
	if err := k.before(ctx, KeystoreChangePassword); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.verifyLocked(ports.Secrets{Password: oldPwd}); err != nil {
		return err
	}
	if newPwd.IsEmpty() {
		return domain.NewError(domain.CodeInvalidInput, errors.New("testkit: new password is empty"))
	}
	k.password.Wipe()
	k.password = newPwd.Clone()
	return nil
}

func (k *FakeKeystore) RevealSeed(ctx context.Context, id string, s ports.Secrets) (secret.Bytes, error) {
	if err := k.before(ctx, KeystoreRevealSeed); err != nil {
		return secret.Bytes{}, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.verifyLocked(s); err != nil {
		return secret.Bytes{}, err
	}
	mnemonic, ok := k.wallets[id]
	if !ok {
		return secret.Bytes{}, fmt.Errorf("wallet %q: %w", id, domain.ErrNotFound)
	}
	return mnemonic.Clone(), nil
}

func (k *FakeKeystore) Delete(ctx context.Context, id string, s ports.Secrets) error {
	if err := k.before(ctx, KeystoreDelete); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.verifyLocked(s); err != nil {
		return err
	}
	mnemonic, ok := k.wallets[id]
	if !ok {
		return fmt.Errorf("wallet %q: %w", id, domain.ErrNotFound)
	}
	mnemonic.Wipe()
	delete(k.wallets, id)
	return nil
}

func (k *FakeKeystore) Sign(ctx context.Context, id string, s ports.Secrets, reqs []ports.DigestRequest) ([]ports.Signature, error) {
	if err := k.before(ctx, KeystoreSign); err != nil {
		return nil, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.verifyLocked(s); err != nil {
		return nil, err
	}
	if _, ok := k.wallets[id]; !ok {
		return nil, fmt.Errorf("wallet %q: %w", id, domain.ErrNotFound)
	}

	out := make([]ports.Signature, len(reqs))
	for i, req := range reqs {
		pub := []byte{0x02}
		for j := 1; j < 33; j++ {
			pub = append(pub, byte(j))
		}
		if k.addressFor != nil {
			address, err := k.addressFor(req.Network, pub)
			if err != nil {
				return nil, err
			}
			if req.ExpectedAddress != "" && address != req.ExpectedAddress {
				return nil, domain.NewError(domain.CodeSigningRefused, errors.New("testkit: derived address mismatch"))
			}
		}
		if i < len(k.signatures) {
			out[i] = cloneSignature(k.signatures[i])
			continue
		}
		for j := range out[i].Sig {
			out[i].Sig[j] = byte(i + j + 1)
		}
		out[i].PubKey = append([]byte(nil), pub...)
	}
	return out, nil
}

func cloneSignature(in ports.Signature) ports.Signature {
	out := in
	out.PubKey = append([]byte(nil), in.PubKey...)
	return out
}

func cloneSignatures(in []ports.Signature) []ports.Signature {
	out := make([]ports.Signature, len(in))
	for i := range in {
		out[i] = cloneSignature(in[i])
	}
	return out
}

var _ ports.Keystore = (*FakeKeystore)(nil)
