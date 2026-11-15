package hd

import (
	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/secret"
)

// PublicData это публичные данные нового кошелька (ADR, жизненный цикл ключа).
// Адреса ETH и TRON получают из ETHPublicKey и TronPublicKey функциями сети,
// переданными в keystore: пакет hd не знает форматов адресов.
//
// Формат BTCXpub (согласован как A8, для Dev 4): стандартная сериализация
// BIP32 счёта m/84'/coin'/0', глубина 3, версия xpub (0x0488B21E) для
// основного профиля и tpub (0x043587CF) для тестового, coin равен 0 и 1.
// Тип скрипта (P2WPKH, адреса bc1q и tb1q) определяет код адаптера, а не
// префикс ключа. Адреса: ParsePublicKey, затем Derive(chain), Derive(index) и
// Hash160 дают программу witness v0.
type PublicData struct {
	BTCXpub       string
	ETHPublicKey  [65]byte // несжатый, 04||X||Y
	TronPublicKey [65]byte
}

// DerivePublicData выводит публичные данные кошелька из seed. Seed не
// затирается, все приватные ключи затираются внутри.
func DerivePublicData(seed secret.Bytes, profile domain.Profile) (PublicData, error) {
	var out PublicData
	account, err := BTCAccountPath(profile)
	if err != nil {
		return out, err
	}
	master, err := MasterKey(seed)
	if err != nil {
		return out, err
	}
	defer master.Wipe()

	k, err := master.DerivePath(account)
	if err != nil {
		return out, err
	}
	out.BTCXpub, err = k.PublicKey().Serialize(profile)
	k.Wipe()
	if err != nil {
		return out, err
	}

	if out.ETHPublicKey, err = uncompressedAt(master, ETHPath()); err != nil {
		return out, err
	}
	if out.TronPublicKey, err = uncompressedAt(master, TronPath()); err != nil {
		return out, err
	}
	return out, nil
}

func uncompressedAt(master *PrivateKey, p Path) ([65]byte, error) {
	k, err := master.DerivePath(p)
	if err != nil {
		return [65]byte{}, err
	}
	defer k.Wipe()
	return k.PublicKey().Uncompressed(), nil
}
