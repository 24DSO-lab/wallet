package backup

import (
	"strings"
	"testing"
)

func TestClassifyEntry(t *testing.T) {
	good := []string{"manifest.json", "db/wallet.db", "keystore/a.json", "keystore/0a1b-2c.keystore", "keystore/x"}
	bad := []string{
		"", "/", ".", "..", "../x", "a", "db/other.db", "db/", "db/wallet.db/", "keystore", "keystore/", "keystore/..",
		"keystore/.", "keystore/.hidden", "keystore/a/b", "keystore/a\\b", "keystore/a:b", "keystore/a b",
		"keystore/a.", "keystore/a ", "keystore/CON", "keystore/con.json", "keystore/Nul.txt", "keystore/LPT1", "keystore/com9.x",
		"keystore/" + strings.Repeat("a", 129), "keystore/\x00", "keystore/а.json", "KEYSTORE/a.json", "./manifest.json", "manifest.json/",
	}
	for _, n := range good {
		if _, err := classifyEntry(n); err != nil {
			t.Errorf("%q rejected: %v", n, err)
		}
	}
	for _, n := range bad {
		if _, err := classifyEntry(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func FuzzClassifyEntry(f *testing.F) {
	for _, s := range []string{"manifest.json", "db/wallet.db", "keystore/a.json", "../x", `keystore\a`, "keystore/CON", "", "keystore/\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		kind, err := classifyEntry(name)
		if err != nil {
			return
		}
		// Принятое имя никогда не содержит ничего, кроме безопасных символов
		// и не выходит из каталога.
		if strings.ContainsAny(name, "\\:\x00") || strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			t.Fatalf("unsafe name accepted: %q", name)
		}
		if kind == kindKeystore && strings.Count(name, "/") != 1 {
			t.Fatalf("nested keystore path accepted: %q", name)
		}
	})
}

func FuzzParseManifest(f *testing.F) {
	f.Add([]byte(`{"format":1,"created_at":"2026-11-15T10:30:00Z","schema_version":3,"files":[{"path":"db/wallet.db","size":1,"sha256":"` + strings.Repeat("a", 64) + `"}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[`))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := parseManifest(data) // не паникует; принятое манифестом проходит собственные правила
		if err != nil {
			return
		}
		if m.Format != FormatVersion || m.SchemaVersion < 1 {
			t.Fatalf("accepted bad header: %+v", m)
		}
		seen := map[string]bool{}
		for _, fe := range m.Files {
			if _, err := classifyEntry(fe.Path); err != nil || seen[fe.Path] || fe.Size < 0 || !validSHA256Hex(fe.SHA256) {
				t.Fatalf("accepted bad file entry: %+v", fe)
			}
			seen[fe.Path] = true
		}
	})
}

func TestReadSchemaVersionHeader(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := dir + "/" + name
		if err := osWriteFile(p, b); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if v, err := readSchemaVersion(write("ok", fakeDBBytes(7))); err != nil || v != 7 {
		t.Errorf("ok: %d, %v", v, err)
	}
	if _, err := readSchemaVersion(write("short", []byte("SQLite format 3\x00"))); err == nil {
		t.Error("short file accepted")
	}
	if _, err := readSchemaVersion(write("magic", make([]byte, 200))); err == nil {
		t.Error("file without magic accepted")
	}
	huge := fakeDBBytes(0)
	copy(huge[60:], []byte{0xFF, 0xFF, 0xFF, 0xFF})
	if _, err := readSchemaVersion(write("huge", huge)); err == nil {
		t.Error("implausible version accepted")
	}
}

func FuzzReadChecksumFile(f *testing.F) {
	f.Add("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  a.zip\n")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		p := t.TempDir() + "/x.sha256"
		if err := osWriteFile(p, []byte(s)); err != nil {
			t.Fatal(err)
		}
		if sum, err := readChecksumFile(p); err == nil && !validSHA256Hex(sum) {
			t.Fatalf("accepted %q -> %q", s, sum)
		}
	})
}
