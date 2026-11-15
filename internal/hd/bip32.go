package hd

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/internal/base58"
	"github.com/24DSO-lab/wallet/internal/hd/internal/ripemd160"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
	"github.com/24DSO-lab/wallet/internal/secret"
)

// Версии сериализации расширенного публичного ключа (A8): xpub для основного
// профиля и tpub для тестового. Тип скрипта задаёт код, а не префикс (без zpub).
const (
	versionXpub uint32 = 0x0488B21E
	versionTpub uint32 = 0x043587CF
	extKeyLen          = 78
)

// Ошибки BIP32. Тексты не содержат ключей.
var (
	ErrInvalidSeed      = errors.New("hd: seed must be 16 to 64 bytes")
	ErrInvalidChild     = errors.New("hd: derived key is invalid, use the next index")
	ErrHardenedFromPub  = errors.New("hd: hardened child cannot be derived from a public key")
	ErrInvalidExtended  = errors.New("hd: invalid extended public key")
	ErrNotPublicVersion = errors.New("hd: extended key is not an xpub or tpub")
)

var masterSalt = []byte("Bitcoin seed")

// PrivateKey это расширенный приватный ключ BIP32. Секретные части (ключ и
// chain code) лежат в secret.Bytes; владелец вызывает Wipe.
type PrivateKey struct {
	key, chain secret.Bytes
	pub        secp256k1.Point
	depth      byte
	parentFP   [4]byte
	index      uint32
}

// MasterKey выводит главный ключ из seed (BIP32). Seed не затирается.
func MasterKey(seed secret.Bytes) (*PrivateKey, error) {
	if n := seed.Len(); n < 16 || n > 64 {
		return nil, ErrInvalidSeed
	}
	mac := hmac.New(sha512.New, masterSalt)
	mac.Write(seed.Expose())
	sum := mac.Sum(nil)
	defer clear(sum)
	return newPrivateKey(sum[:32], sum[32:], 0, [4]byte{}, 0)
}

func newPrivateKey(key, chain []byte, depth byte, fp [4]byte, index uint32) (*PrivateKey, error) {
	pub, err := secp256k1.ScalarBaseMult(key)
	if err != nil {
		return nil, ErrInvalidChild
	}
	return &PrivateKey{
		key:      secret.Copy(key),
		chain:    secret.Copy(chain),
		pub:      pub,
		depth:    depth,
		parentFP: fp,
		index:    index,
	}, nil
}

// Derive выводит дочерний ключ (CKDpriv). Индексы от Hardened и выше усиленные.
func (k *PrivateKey) Derive(index uint32) (*PrivateKey, error) {
	if k.depth == 255 {
		return nil, ErrInvalidChild
	}
	data := make([]byte, 0, 37)
	defer func() { clear(data) }()
	if index >= Hardened {
		data = append(data, 0)
		data = append(data, k.key.Expose()...)
	} else {
		c := k.pub.Compressed()
		data = append(data, c[:]...)
	}
	data = binary.BigEndian.AppendUint32(data, index)

	mac := hmac.New(sha512.New, k.chain.Expose())
	mac.Write(data)
	sum := mac.Sum(nil)
	defer clear(sum)

	if !secp256k1.ScalarBelowOrder(sum[:32]) {
		return nil, ErrInvalidChild
	}
	child, err := secp256k1.AddScalarsModOrder(sum[:32], k.key.Expose())
	defer clear(child[:])
	if err != nil {
		return nil, ErrInvalidChild
	}
	return newPrivateKey(child[:], sum[32:], k.depth+1, fingerprint(k.pub), index)
}

// DerivePath последовательно выводит ключ по пути. Промежуточные ключи
// затираются сразу. Сам k не затирается.
func (k *PrivateKey) DerivePath(p Path) (*PrivateKey, error) {
	cur := k
	for _, idx := range p {
		next, err := cur.Derive(idx)
		if cur != k {
			cur.Wipe()
		}
		if err != nil {
			return nil, err
		}
		cur = next
	}
	if cur == k {
		// Пустой путь: возвращаем независимую копию, чтобы Wipe копии не
		// затирал оригинал.
		return &PrivateKey{key: k.key.Clone(), chain: k.chain.Clone(), pub: k.pub,
			depth: k.depth, parentFP: k.parentFP, index: k.index}, nil
	}
	return cur, nil
}

// Secret возвращает 32 байта секретного ключа без копирования. Не печатайте
// и не сохраняйте; Wipe ключа затирает и этот срез.
func (k *PrivateKey) Secret() secret.Bytes { return k.key }

// PublicKey возвращает соответствующий публичный ключ.
func (k *PrivateKey) PublicKey() PublicKey {
	var chain [32]byte
	copy(chain[:], k.chain.Expose())
	return PublicKey{point: k.pub, chain: chain, depth: k.depth, parentFP: k.parentFP, index: k.index}
}

// Wipe затирает секретные части ключа.
func (k *PrivateKey) Wipe() {
	k.key.Wipe()
	k.chain.Wipe()
}

// DeriveFromSeed выводит ключ по пути прямо из seed и затирает всё
// промежуточное. Результат затирает вызывающий.
func DeriveFromSeed(seed secret.Bytes, p Path) (*PrivateKey, error) {
	master, err := MasterKey(seed)
	if err != nil {
		return nil, err
	}
	defer master.Wipe()
	return master.DerivePath(p)
}

// PublicKey это расширенный публичный ключ BIP32. Он не секретный, но
// раскрывает все адреса счёта (ADR): не логировать без необходимости.
type PublicKey struct {
	point    secp256k1.Point
	chain    [32]byte
	depth    byte
	parentFP [4]byte
	index    uint32
}

// Derive выводит дочерний публичный ключ (CKDpub) для неусиленного индекса.
func (p PublicKey) Derive(index uint32) (PublicKey, error) {
	if index >= Hardened {
		return PublicKey{}, ErrHardenedFromPub
	}
	if p.depth == 255 {
		return PublicKey{}, ErrInvalidChild
	}
	c := p.point.Compressed()
	data := binary.BigEndian.AppendUint32(append([]byte(nil), c[:]...), index)
	mac := hmac.New(sha512.New, p.chain[:])
	mac.Write(data)
	sum := mac.Sum(nil)
	if !secp256k1.ScalarBelowOrder(sum[:32]) {
		return PublicKey{}, ErrInvalidChild
	}
	pt, err := secp256k1.AddScalarBase(p.point, sum[:32])
	if err != nil {
		return PublicKey{}, ErrInvalidChild
	}
	child := PublicKey{point: pt, depth: p.depth + 1, parentFP: fingerprint(p.point), index: index}
	copy(child.chain[:], sum[32:])
	return child, nil
}

// Compressed возвращает сжатый ключ в 33 байта.
func (p PublicKey) Compressed() [33]byte { return p.point.Compressed() }

// Uncompressed возвращает несжатый ключ в 65 байт: вход AddressFromPublicKey.
func (p PublicKey) Uncompressed() [65]byte { return p.point.Uncompressed() }

// Hash160 возвращает RIPEMD160(SHA256(сжатый ключ)): основа адреса P2WPKH.
func (p PublicKey) Hash160() [20]byte {
	c := p.point.Compressed()
	return hash160(c[:])
}

// Serialize возвращает xpub (основной профиль) или tpub (тестовый) в
// стандартной сериализации BIP32.
func (p PublicKey) Serialize(profile domain.Profile) (string, error) {
	version := versionXpub
	switch profile {
	case domain.ProfileMain:
	case domain.ProfileTest:
		version = versionTpub
	default:
		return "", errors.New("hd: unknown network profile")
	}
	buf := make([]byte, 0, extKeyLen)
	buf = binary.BigEndian.AppendUint32(buf, version)
	buf = append(buf, p.depth)
	buf = append(buf, p.parentFP[:]...)
	buf = binary.BigEndian.AppendUint32(buf, p.index)
	buf = append(buf, p.chain[:]...)
	c := p.point.Compressed()
	buf = append(buf, c[:]...)
	return base58.CheckEncode(buf), nil
}

// ParsePublicKey разбирает xpub или tpub и возвращает ключ и профиль, которому
// он принадлежит. Приватные расширенные ключи (xprv, tprv) отвергаются, их
// текст в ошибку не попадает.
func ParsePublicKey(s string) (PublicKey, domain.Profile, error) {
	raw, err := base58.CheckDecode(s)
	if err != nil || len(raw) != extKeyLen {
		return PublicKey{}, "", ErrInvalidExtended
	}
	var profile domain.Profile
	switch binary.BigEndian.Uint32(raw[:4]) {
	case versionXpub:
		profile = domain.ProfileMain
	case versionTpub:
		profile = domain.ProfileTest
	default:
		return PublicKey{}, "", ErrNotPublicVersion
	}
	pk := PublicKey{depth: raw[4], index: binary.BigEndian.Uint32(raw[9:13])}
	copy(pk.parentFP[:], raw[5:9])
	copy(pk.chain[:], raw[13:45])
	if pk.depth == 0 && (pk.parentFP != [4]byte{} || pk.index != 0) {
		return PublicKey{}, "", ErrInvalidExtended
	}
	point, err := secp256k1.ParsePoint(raw[45:])
	if err != nil || raw[45] > 0x03 {
		return PublicKey{}, "", ErrInvalidExtended
	}
	pk.point = point
	return pk, profile, nil
}

func fingerprint(p secp256k1.Point) [4]byte {
	c := p.Compressed()
	h := hash160(c[:])
	return [4]byte(h[:4])
}

func hash160(b []byte) [20]byte {
	s := sha256.Sum256(b)
	return ripemd160.Sum(s[:])
}
