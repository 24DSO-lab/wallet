package btc

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"golang.org/x/crypto/ripemd160" //nolint:gosec // Bitcoin HASH160 requires RIPEMD-160; this is not a password hash.

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd/secp256k1"
)

// AddressFromPublicKey converts a 65-byte uncompressed secp256k1 public key
// into its native SegWit P2WPKH address for profile. P2WPKH hashes the
// compressed 33-byte serialization, even when the caller supplies an
// uncompressed key (as keystore.Sign does).
//
// This function is suitable for keystore.Config.Addresses[domain.NetworkBitcoin]:
//
//	func(pub []byte) (string, error) {
//		return btc.AddressFromPublicKey(profile, pub)
//	}
func AddressFromPublicKey(profile domain.Profile, uncompressedPub []byte) (string, error) {
	if !profile.Valid() {
		return "", domain.NewError(domain.CodeInternal, errors.New("btc: invalid network profile"))
	}
	if len(uncompressedPub) != secp256k1.PubKeyUncompressedLen {
		return "", domain.NewError(domain.CodeInvalidInput, errors.New("btc: expected 65-byte uncompressed public key"))
	}

	point, err := secp256k1.ParsePoint(uncompressedPub)
	if err != nil {
		return "", domain.NewError(domain.CodeInvalidInput, fmt.Errorf("btc: parse public key: %w", err))
	}

	compressed := point.Compressed()
	sha := sha256.Sum256(compressed[:])
	ripemd := ripemd160.New() //nolint:gosec // Required by the Bitcoin P2WPKH address specification.
	if _, err := ripemd.Write(sha[:]); err != nil {
		return "", fmt.Errorf("btc: hash public key: %w", err)
	}

	address, err := encodeP2WPKHAddress(profile, ripemd.Sum(nil))
	if err != nil {
		return "", fmt.Errorf("btc: encode public-key address: %w", err)
	}
	return address, nil
}
