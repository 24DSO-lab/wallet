package btc

import (
	"errors"
	"fmt"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd"
)

// HDAddressDeriver adapts the public BIP32 API to the scanner's address
// derivation contract. It never receives or handles private key material.
type HDAddressDeriver struct{}

// DeriveAddress derives m/84'/coin'/0'/chain/index from an account xpub.
// Receive is chain 0 and change is chain 1, as defined by BIP84.
func (HDAddressDeriver) DeriveAddress(accountXpub string, profile domain.Profile, chain domain.AddressChain, index uint32) (string, string, error) {
	if !profile.Valid() {
		return "", "", domain.NewError(domain.CodeInternal, errors.New("btc: invalid network profile"))
	}
	if index >= hd.Hardened {
		return "", "", domain.NewError(domain.CodeInvalidInput, errors.New("btc: address index exceeds BIP32 non-hardened range"))
	}

	chainIndex := uint32(0)
	switch chain {
	case domain.ChainReceive:
		chainIndex = 0
	case domain.ChainChange:
		chainIndex = 1
	default:
		return "", "", domain.NewError(domain.CodeInvalidInput, errors.New("btc: unsupported address chain"))
	}

	account, keyProfile, err := hd.ParsePublicKey(accountXpub)
	if err != nil {
		return "", "", fmt.Errorf("btc: parse account xpub: %w", err)
	}
	if keyProfile != profile {
		return "", "", domain.NewError(domain.CodeWrongNetwork, errors.New("btc: account xpub belongs to another profile"))
	}

	branch, err := account.Derive(chainIndex)
	if err != nil {
		return "", "", fmt.Errorf("btc: derive address branch: %w", err)
	}
	child, err := branch.Derive(index)
	if err != nil {
		return "", "", fmt.Errorf("btc: derive address index: %w", err)
	}

	program := child.Hash160()
	address, err := encodeP2WPKHAddress(profile, program[:])
	if err != nil {
		return "", "", fmt.Errorf("btc: encode derived address: %w", err)
	}
	accountPath, err := hd.BTCAccountPath(profile)
	if err != nil {
		return "", "", fmt.Errorf("btc: account path: %w", err)
	}
	path := accountPath.Child(chainIndex).Child(index).String()
	return address, path, nil
}

// encodeP2WPKHAddress encodes a 20-byte witness-v0 program using BIP173.
func encodeP2WPKHAddress(profile domain.Profile, program []byte) (string, error) {
	if len(program) != 20 {
		return "", ErrInvalidAddress
	}
	hrp := "bc"
	switch profile {
	case domain.ProfileMain:
	case domain.ProfileTest:
		hrp = "tb"
	default:
		return "", errors.New("btc: unknown network profile")
	}
	data, err := convertBits(program, 8, 5, true)
	if err != nil {
		return "", err
	}
	payload := append([]byte{0}, data...)
	values := append(hrpExpand(hrp), payload...)
	values = append(values, make([]byte, 6)...)
	mod := bech32Polymod(values) ^ 1
	checksum := make([]byte, 6)
	for i := range checksum {
		checksum[i] = byte((mod >> uint(5*(5-i))) & 31)
	}
	combined := append(payload, checksum...)
	out := make([]byte, 0, len(hrp)+1+len(combined))
	out = append(out, hrp...)
	out = append(out, '1')
	for _, v := range combined {
		out = append(out, charset[v])
	}
	return string(out), nil
}
