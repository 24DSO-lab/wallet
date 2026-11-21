package ports

import (
	"context"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/secret"
)

// DigestRequest это запрос подписи 32-байтного дайджеста. Keystore выводит
// ключ по Path, превращает его в адрес сети и сверяет с ExpectedAddress до
// подписи; при несовпадении подпись отменяется с CodeSigningRefused.
type DigestRequest struct {
	Network         domain.Network
	Path            string
	ExpectedAddress string
	Digest          [32]byte
}

// Signature это подпись secp256k1.
type Signature struct {
	Sig    [65]byte // r||s||v, s каноническая (low-S), v равно 0 или 1
	PubKey []byte   // сжатый публичный ключ, 33 байта
}

// Secrets это секреты одной операции. Вызывающий затирает оба поля после вызова.
type Secrets struct {
	Password   secret.Bytes
	Passphrase secret.Bytes // парольная фраза BIP39, может быть пустой
}

// PublicKeys это публичные данные нового кошелька, выведенные из seed.
type PublicKeys struct {
	BTCXpub     string // xpub или tpub (A8), счёт m/84'/coin'/0'
	ETHAddress  string
	TronAddress string
}

// Keystore единственный видит seed и приватные ключи. Все методы принимают
// секреты через secret.Bytes и сами затирают внутренние буферы.
type Keystore interface {
	// NewMnemonic создаёт мнемонику из 12 или 24 слов.
	NewMnemonic(words int) (secret.Bytes, error)

	// Save шифрует мнемонику в файл кошелька id и возвращает публичные данные.
	// Используется и при создании, и при импорте.
	Save(ctx context.Context, id string, mnemonic secret.Bytes, s Secrets) (PublicKeys, error)

	// Verify проверяет пароль расшифровкой keystore, результат не сохраняется.
	Verify(ctx context.Context, s Secrets) error

	// ChangePassword перешифровывает все файлы атомарно.
	ChangePassword(ctx context.Context, oldPwd, newPwd secret.Bytes) error

	// RevealSeed возвращает мнемонику кошелька. Вызывающий обязан затереть её.
	RevealSeed(ctx context.Context, id string, s Secrets) (secret.Bytes, error)

	// PublicKeys заново выводит публичные данные кошелька id из seed. Нужен для
	// проверки того, что сохранённые в базе xpub и адреса не подменены: база не
	// аутентифицирована, а seed не покидает keystore.
	PublicKeys(ctx context.Context, id string, s Secrets) (PublicKeys, error)

	// Delete удаляет файл кошелька после проверки пароля.
	Delete(ctx context.Context, id string, s Secrets) error

	// Sign подписывает дайджесты. Порядок подписей совпадает с порядком reqs.
	Sign(ctx context.Context, id string, s Secrets, reqs []DigestRequest) ([]Signature, error)
}
