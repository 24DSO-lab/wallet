package eth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
)

// Adapter это адаптер сети Ethereum для ETH и USDC. Безопасен для вызова из
// нескольких горутин: изменяемое состояние одно, проверка chainId, и она под
// мьютексом.
type Adapter struct {
	params networkParams
	rpc    RPC
	idx    Indexer
	now    func() time.Time

	chainMu       sync.Mutex
	chainVerified bool
}

// Options настраивает адаптер. Нулевые значения означают значения по умолчанию.
type Options struct {
	// Now подменяется в тестах.
	Now func() time.Time
}

// New создаёт адаптер профиля. rpc и idx приходят снаружи (за ними стоит
// provider.Client), адаптер сам сетевых клиентов не создаёт.
func New(profile domain.Profile, rpc RPC, idx Indexer, opts Options) (*Adapter, error) {
	if rpc == nil || idx == nil {
		return nil, errors.New("eth: rpc and indexer are required")
	}
	params, err := paramsFor(profile)
	if err != nil {
		return nil, err
	}
	a := &Adapter{params: params, rpc: rpc, idx: idx, now: opts.Now}
	if a.now == nil {
		a.now = time.Now
	}
	return a, nil
}

// Network возвращает сеть адаптера.
func (a *Adapter) Network() domain.Network { return domain.NetworkEthereum }

// ValidateAddress проверяет адрес правилами Ethereum (см. README пакета).
func (a *Adapter) ValidateAddress(addr string) error { return ValidateAddress(addr) }

// AddressFromPublicKey превращает несжатый ключ в адрес EIP-55.
func (a *Adapter) AddressFromPublicKey(pub []byte) (string, error) {
	addr, err := addressFromPublicKey(pub)
	if err != nil {
		return "", domain.NewError(domain.CodeInvalidInput, err)
	}
	return addr.String(), nil
}

// ensureChain сверяет chainId узла с профилем один раз за успешную проверку.
// Провайдер, который отвечает чужой сетью (например, main вместо Sepolia),
// иначе подпись транзакции ушла бы в не ту сеть; защита второго уровня это
// chainId внутри подписываемой транзакции.
func (a *Adapter) ensureChain(ctx context.Context) error {
	a.chainMu.Lock()
	defer a.chainMu.Unlock()
	if a.chainVerified {
		return nil
	}
	id, err := a.rpc.ChainID(ctx)
	if err != nil {
		return err
	}
	if id != a.params.chainID {
		return domain.NewError(domain.CodeWrongNetwork,
			fmt.Errorf("eth: provider serves chain %d, profile %q needs %d", id, string(a.params.profile), a.params.chainID))
	}
	a.chainVerified = true
	return nil
}
