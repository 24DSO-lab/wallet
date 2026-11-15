package ripemd160

import (
	"encoding/hex"
	"testing"
)

// Официальные векторы RIPEMD-160 (Dobbertin, Bosselaers, Preneel).
func TestOfficialVectors(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "9c1185a5c5e9fc54612808977ee8f548b2258d31",
		"a":                          "0bdc9d2d256b3ee9daae347be6f4dc835a467ffe",
		"abc":                        "8eb208f7e05d987a9b044a8e98c6b087f15a0bfc",
		"message digest":             "5d0689ef49d2fae572b881b123a85ffa21595f36",
		"abcdefghijklmnopqrstuvwxyz": "f71c27109c692c1b56bbdceb5b9d2865b3708dbc",
		"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq": "12a053384a9c0c88e405a06c27dcf49ada62eb2b",
	} {
		got := Sum([]byte(in))
		if hex.EncodeToString(got[:]) != want {
			t.Errorf("Sum(%q) = %x, want %s", in, got, want)
		}
	}
}

func data(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*7 + 3) % 256)
	}
	return b
}

// Эталоны получены из hashlib (OpenSSL) для длин на границах блока.
var ripemdVectors = []struct {
	n    int
	want string
}{
	{0, "9c1185a5c5e9fc54612808977ee8f548b2258d31"},
	{1, "b2afadd73b9922f395573a52e7032b7597ff8c3e"},
	{3, "4198d88f75583a3c1b1dbf18501eaff97669ae63"},
	{55, "ced4a416d2eddc4c54a59c57fa299bc86af70de9"},
	{56, "581330764dcfaa5bbe4de58601aa56a838cc58d7"},
	{57, "9694d882853e0ed51a4f81fe71c5c4ffe5bc8480"},
	{63, "f946627df85129e38a6142b694aed087f2f85d71"},
	{64, "6049fc18acb2ba0205d12fbf2ebc57628031d28c"},
	{65, "a03bc7711af42632ce8e675adbdc1d690a850f07"},
	{119, "22d9d4f2a6368b58145202e9ffa1479f0a13fd11"},
	{120, "c7ca324844fcb190fb8a9e7d843251391e2556de"},
	{121, "5ae42760f0077840983fc0f267165552cbdf54bf"},
	{127, "0c8d2fd036279a926ff6a4e51a2c0aa6e2f7306f"},
	{128, "0612b629dd5aedbd608df63782bac221b62336df"},
	{129, "23fdfec1625b24182445d9a92643b20926d77553"},
	{1000, "462fa67a8f19c1df2d98cff47379ba31d681b572"},
}

func TestAgainstHashlib(t *testing.T) {
	for _, v := range ripemdVectors {
		got := Sum(data(v.n))
		if hex.EncodeToString(got[:]) != v.want {
			t.Errorf("len %d: got %x, want %s", v.n, got, v.want)
		}
	}
}
