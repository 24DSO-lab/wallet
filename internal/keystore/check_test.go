package keystore

import (
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
)

func TestCheckFile(t *testing.T) {
	e := newEnv(t, domain.ProfileMain)
	e.save("w1", abandon, pwd, "")
	good := readBytes(t, e.s.path("w1"))

	if err := CheckFile("w1.keystore", good); err != nil {
		t.Fatalf("valid file rejected: %v", err)
	}

	bad := append([]byte(nil), good...)
	bad[0] ^= 1
	long := append(append([]byte(nil), good...), 0)

	cases := []struct {
		name string
		file string
		data []byte
	}{
		{"renamed file", "w2.keystore", good},
		{"no extension", "w1", good},
		{"wrong extension", "w1.json", good},
		{"empty id", ".keystore", good},
		{"path separator", "../w1.keystore", good},
		{"reserved name", "con.keystore", good},
		{"bad magic", "w1.keystore", bad},
		{"truncated", "w1.keystore", good[:len(good)-1]},
		{"trailing byte", "w1.keystore", long},
		{"empty", "w1.keystore", nil},
	}
	for _, c := range cases {
		err := CheckFile(c.file, c.data)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		wantCode(t, err, domain.CodeKeystoreCorrupt)
	}
}

func FuzzCheckFile(f *testing.F) {
	f.Add("w1.keystore", []byte("WLTK"))
	f.Add("", []byte(nil))
	f.Fuzz(func(t *testing.T, name string, data []byte) {
		_ = CheckFile(name, data) // не должно паниковать
	})
}
