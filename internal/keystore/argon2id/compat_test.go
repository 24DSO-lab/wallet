package argon2id_test

import (
	"bytes"
	"fmt"
	"testing"

	xargon2 "golang.org/x/crypto/argon2"

	"github.com/24DSO-lab/wallet/internal/keystore/argon2id"
)

// keystore вызывает golang.org/x/crypto/argon2 (ADR-003), а файлы, созданные
// прежней собственной реализацией, должны открываться без изменений. Тест
// сравнивает обе реализации на разных параметрах, включая несколько полос.
func TestMatchesXCrypto(t *testing.T) {
	cases := []struct {
		time, memory uint32
		threads      uint8
	}{
		{1, 32, 1},
		{2, 64, 1},
		{3, 256, 1},
		{2, 64, 2},
		{3, 256, 4},
		{2, 19 * 1024, 1},
	}
	passwords := [][]byte{[]byte("пароль-кошелька"), []byte("p"), bytes.Repeat([]byte("x"), 200)}
	for _, c := range cases {
		for i, pw := range passwords {
			salt := bytes.Repeat([]byte{byte(0x10 + i)}, 16)
			t.Run(fmt.Sprintf("t%d_m%d_p%d_pw%d", c.time, c.memory, c.threads, i), func(t *testing.T) {
				own := argon2id.Key(pw, salt, nil, nil, c.time, c.memory, c.threads, 32)
				ref := xargon2.IDKey(pw, salt, c.time, c.memory, c.threads, 32)
				if !bytes.Equal(own, ref) {
					t.Fatalf("keys differ:\n own %x\n ref %x", own, ref)
				}
			})
		}
	}
}
