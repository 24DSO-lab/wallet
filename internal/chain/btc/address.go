package btc

import (
	"errors"
	"fmt"
	"strings"

	"github.com/24DSO-lab/wallet/internal/domain"
)

var (
	ErrInvalidAddress = errors.New("btc: invalid native SegWit address")
	ErrWrongNetwork   = errors.New("btc: address belongs to another network")
)

// ValidateAddress accepts native P2WPKH (BIP84) addresses only. The profile
// determines the human-readable part: bc for mainnet and tb for testnet.
// Taproot and wrapped/legacy scripts are intentionally not accepted.
func ValidateAddress(profile domain.Profile, address string) error {
	if !profile.Valid() {
		return domain.NewError(domain.CodeInternal, errors.New("btc: invalid network profile"))
	}
	hrp, version, program, err := decodeWitnessAddress(address)
	if err != nil || version != 0 || len(program) != 20 {
		return domain.NewError(domain.CodeInvalidAddress, fmt.Errorf("%w", ErrInvalidAddress))
	}
	want := "bc"
	if profile == domain.ProfileTest {
		want = "tb"
	}
	if hrp != want {
		return domain.NewError(domain.CodeWrongNetwork, ErrWrongNetwork)
	}
	return nil
}

func decodeWitnessAddress(s string) (string, byte, []byte, error) {
	if len(s) < 8 || len(s) > 90 {
		return "", 0, nil, ErrInvalidAddress
	}
	if strings.ToLower(s) != s && strings.ToUpper(s) != s {
		return "", 0, nil, ErrInvalidAddress
	}
	s = strings.ToLower(s)
	for i := 0; i < len(s); i++ {
		if s[i] < 33 || s[i] > 126 {
			return "", 0, nil, ErrInvalidAddress
		}
	}
	sep := strings.LastIndexByte(s, '1')
	if sep < 1 || sep+7 > len(s) {
		return "", 0, nil, ErrInvalidAddress
	}
	hrp := s[:sep]
	data := make([]byte, len(s)-sep-1)
	for i, c := range []byte(s[sep+1:]) {
		if charsetRev[c] == 0xff {
			return "", 0, nil, ErrInvalidAddress
		}
		data[i] = charsetRev[c]
	}
	if bech32Polymod(append(hrpExpand(hrp), data...)) != 1 {
		return "", 0, nil, ErrInvalidAddress
	}
	payload := data[:len(data)-6]
	if len(payload) < 1 || payload[0] > 16 {
		return "", 0, nil, ErrInvalidAddress
	}
	program, err := convertBits(payload[1:], 5, 8, false)
	if err != nil {
		return "", 0, nil, ErrInvalidAddress
	}
	return hrp, payload[0], program, nil
}

const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var charsetRev = func() [128]byte {
	var out [128]byte
	for i := range out {
		out[i] = 0xff
	}
	for i, c := range []byte(charset) {
		out[c] = byte(i)
	}
	return out
}()

func hrpExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func bech32Polymod(values []byte) uint32 {
	constant := uint32(1)
	generators := [...]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	for _, value := range values {
		top := constant >> 25
		constant = (constant&0x1ffffff)<<5 ^ uint32(value)
		for i, gen := range generators {
			if (top>>i)&1 != 0 {
				constant ^= gen
			}
		}
	}
	return constant
}

func convertBits(data []byte, from, to uint, pad bool) ([]byte, error) {
	var acc uint32
	var bits uint
	maxv := uint32(1<<to) - 1
	maxAcc := uint32(1<<(from+to-1)) - 1
	out := make([]byte, 0, len(data)*int(from)/int(to)+1)
	for _, value := range data {
		if uint(value)>>from != 0 {
			return nil, ErrInvalidAddress
		}
		acc = (acc<<from | uint32(value)) & maxAcc
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte((acc<<(to-bits))&maxv))
		}
	} else if bits >= from || ((acc<<(to-bits))&maxv) != 0 {
		return nil, ErrInvalidAddress
	}
	return out, nil
}
