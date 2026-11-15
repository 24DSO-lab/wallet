package hd

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/secret"
)

// Эталоны: энтропия 00/7f/80/ff по 16, 24, 32 байта из официального набора
// BIP39 (Trezor), мнемоники и seed с парольной фразой TREZOR; остальные
// строки и производные seed получены независимой реализацией на Python
// (hashlib.pbkdf2_hmac, вордлист из этого же репозитория).
var bip39Vectors = []struct{ entropy, mnemonic, seedTrezor string }{
	{"00000000000000000000000000000000", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "c55257c360c07c72029aebc1b53c05ed0362ada38ead3e3e9efa3708e53495531f09a6987599d18264c1e1c92f2cf141630c7a3c4ab7c81b2f001698e7463b04"},
	{"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", "legal winner thank year wave sausage worth useful legal winner thank yellow", "2e8905819b8723fe2c1d161860e5ee1830318dbf49a83bd451cfb8440c28bd6fa457fe1296106559a3c80937a1c1069be3a3a5bd381ee6260e8d9739fce1f607"},
	{"80808080808080808080808080808080", "letter advice cage absurd amount doctor acoustic avoid letter advice cage above", "d71de856f81a8acc65e6fc851a38d4d7ec216fd0796d0a6827a3ad6ed5511a30fa280f12eb2e47ed2ac03b5c462a0358d18d69fe4f985ec81778c1b370b652a8"},
	{"ffffffffffffffffffffffffffffffff", "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong", "ac27495480225222079d7be181583751e86f571027b0497b5b5d11218e0a8a13332572917f0f8e5a589620c6f15b11c61dee327651a14c34e18231052e48c069"},
	{"000000000000000000000000000000000000000000000000", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon agent", "035895f2f481b1b0f01fcf8c289c794660b289981a78f8106447707fdd9666ca06da5a9a565181599b79f53b844d8a71dd9f439c52a3d7b3e8a79c906ac845fa"},
	{"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", "legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal will", "f2b94508732bcbacbcc020faefecfc89feafa6649a5491b8c952cede496c214a0c7b3c392d168748f2d4a612bada0753b52a1c7ac53c1e93abd5c6320b9e95dd"},
	{"808080808080808080808080808080808080808080808080", "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic avoid letter always", "107d7c02a5aa6f38c58083ff74f04c607c2d2c0ecc55501dadd72d025b751bc27fe913ffb796f841c49b1d33b610cf0e91d3aa239027f5e99fe4ce9e5088cd65"},
	{"ffffffffffffffffffffffffffffffffffffffffffffffff", "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo when", "0cd6e5d827bb62eb8fc1e262254223817fd068a74b5b449cc2f667c3f1f985a76379b43348d952e2265b4cd129090758b3e3c2c49103b5051aac2eaeb890a528"},
	{"0000000000000000000000000000000000000000000000000000000000000000", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art", "bda85446c68413707090a52022edd26a1c9462295029f2e60cd7c4f2bbd3097170af7a4d73245cafa9c3cca8d561a7c3de6f5d4a10be8ed2a5e608d68f92fcc8"},
	{"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", "legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth title", "bc09fca1804f7e69da93c2f2028eb238c227f2e9dda30cd63699232578480a4021b146ad717fbb7e451ce9eb835f43620bf5c514db0f8add49f5d121449d3e87"},
	{"8080808080808080808080808080808080808080808080808080808080808080", "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic bless", "c0c519bd0e91a2ed54357d9d1ebef6f5af218a153624cf4f2da911a0ed8f7a09e2ef61af0aca007096df430022f7a2b6fb91661a9589097069720d015e4e982f"},
	{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo vote", "dd48c104698c30cfe2b6142103248622fb7bb0ff692eebb00089b32d22484e1613912f0a5b694407be899ffd31ed3992c456cdf60f5d4564b8ba3f05a69890ad"},
	{"5c88e7a226e11ad1204cb8d30cd5d6ff", "frame elbow trigger evidence balance hammer license novel spread grit interest youth", "29ce7ab5a0c62f0bdb38779d8f98759e4f36a2d09385107658f73a2aacb1f673029d6bfa8db700d8b03294d3865ac21d20c46e10b140b67cd8edcdf540bb5a03"},
	{"8b5cc4df7eec7d32a7814eca4af047ae33b2d523", "mercy tower daughter worry sick off own clap ski fiscal ball fragile depth hedgehog million", "e99de545568f99e7cd60d2ef6ba0b498471c5528d21473b84fd76e3c3d7cbdb0f6b13b900d2bcac0241cda3289c84aac98955893bbb3c3f34bba809b22475c47"},
	{"ac0f09c0f8bf5e7a4b063d863255f16d8ce9abe600e288d934cf313b", "project joke ice vapor vote diamond clown sibling mail naive labor swallow soldier sting object brisk match situate critic give sauce", "022810fda6fa79e4b189093c960612bc5f7267da7496c30976cc24528b3deb08dc0dac011ca2e9e2ff24231220fd20dc2345c58dc88621c82ac45f74e81e46f2"},
	{"cef7fc13a38180936ffa2635489088778e059f07a5d1beda53f1719d35577631", "solve say agent either blossom endless say dynamic crystal duty capital upper theory sort burden riot salute spoon wisdom tobacco spy produce sugar correct", "b1fc2416a9ac7be9ab0a9d1e2a46087214c5379680d0be85f6b79a572c84793f9a950fed2ea08ff82980de283f830e93675d129cee32982a3c17f2e31f074937"},
}

const abandonSeed = "5eb00bbddcf069084889a8ab9155568165f5c453ccb85e70811aaed6f6da5fc19a5ac40b389cd370d086206dec8aa6c43daea6690f20ad3d8d48b2d2ce9e38e4"
const abandonSeedPass = "836e53dcc33fa6331dc49afa953310c3f2e88984cec33b383bd0a20c0958827db33c4046a77f974a60a52dcc0aaa838b83b29f6f3b908f545db6920ea8fbdfd7" // passphrase "wallet-pass"
const abandonSeedCyr = "cf09db3b92f91830d3995977e27ae692af837831c958144802da3dce9f958169e7269e536240ad79826a8b64e931dcc8ffd544975cff6ab9eff97eb9148ff7e9"  // passphrase "Йёж" (NFKD)

// Контрольная сумма SHA-256 официального english.txt из bitcoin/bips.
// Сверьте с оригиналом: sha256sum bip39/english.txt.
const wordlistSHA256 = "2f5eed53a4727b4bf8880d8f3f199efc90e58503646d9ff8eff3a2ed3b24dbda"

const abandonMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func TestWordlistIntegrity(t *testing.T) {
	sum := sha256.Sum256([]byte(wordlistText))
	if got := hex.EncodeToString(sum[:]); got != wordlistSHA256 {
		t.Fatalf("wordlist sha256 = %s, want %s", got, wordlistSHA256)
	}
	w := wordlist()
	if len(w) != 2048 || w[0] != "abandon" || w[2047] != "zoo" {
		t.Fatalf("unexpected list: %d words, first %q, last %q", len(w), w[0], w[len(w)-1])
	}
	seen := map[string]bool{}
	for i, word := range w {
		if i > 0 && w[i-1] >= word {
			t.Fatalf("list is not strictly sorted at %q", word)
		}
		prefix := word[:min(4, len(word))]
		if seen[prefix] {
			t.Fatalf("4-letter prefix of %q is not unique", word)
		}
		seen[prefix] = true
	}
}

func TestMnemonicVectors(t *testing.T) {
	for _, v := range bip39Vectors {
		entropy, _ := hex.DecodeString(v.entropy)
		m, err := MnemonicFromEntropy(entropy)
		if err != nil {
			t.Fatal(err)
		}
		if string(m.Expose()) != v.mnemonic {
			t.Errorf("entropy %s: mnemonic %q, want %q", v.entropy, m.Expose(), v.mnemonic)
		}
		if err := ValidateMnemonic(m); err != nil {
			t.Errorf("own mnemonic rejected: %v", err)
		}
		seed, err := SeedFromMnemonic(m, secret.FromString("TREZOR"))
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(seed.Expose()); got != v.seedTrezor {
			t.Errorf("seed for %s = %s, want %s", v.entropy, got, v.seedTrezor)
		}
		m.Wipe()
		seed.Wipe()
	}
}

func TestSeedKnownPhrases(t *testing.T) {
	m := secret.FromString(abandonMnemonic)
	for name, tc := range map[string]struct{ pass, want string }{
		"empty":    {"", abandonSeed},
		"ascii":    {"wallet-pass", abandonSeedPass},
		"nfkd":     {"Йёж", abandonSeedCyr},   // составные символы
		"nfkd dec": {"Йёж", abandonSeedCyr}, // тот же текст в разложенном виде
	} {
		seed, err := SeedFromMnemonic(m, secret.FromString(tc.pass))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := hex.EncodeToString(seed.Expose()); got != tc.want {
			t.Errorf("%s: seed %s, want %s", name, got, tc.want)
		}
	}
	// Известный seed для abandon...about без парольной фразы (BIP39/BIP32 экосистема).
	if !strings.HasPrefix(abandonSeed, "5eb00bbddcf06908") {
		t.Errorf("abandon seed prefix mismatch: %s", abandonSeed)
	}
}

func TestPBKDF2AgainstStdlib(t *testing.T) {
	for _, c := range []struct {
		pw, salt string
		iter, n  int
	}{{"password", "salt", 1, 64}, {"password", "salt", 2048, 64}, {"", "mnemonic", 5, 100}, {strings.Repeat("k", 200), "s", 3, 130}} {
		want, err := pbkdf2.Key(sha512.New, c.pw, []byte(c.salt), c.iter, c.n)
		if err != nil {
			t.Fatal(err)
		}
		if got := pbkdf2SHA512([]byte(c.pw), []byte(c.salt), c.iter, c.n); hex.EncodeToString(got) != hex.EncodeToString(want) {
			t.Errorf("pbkdf2 mismatch for %+v", c)
		}
	}
}

func TestValidateErrors(t *testing.T) {
	good := strings.Fields(abandonMnemonic)
	tests := map[string]struct {
		in   string
		want error
	}{
		"checksum":     {strings.Join(append(append([]string{}, good[:11]...), "abandon"), " "), ErrChecksum},
		"unknown":      {strings.Join(append(append([]string{}, good[:11]...), "zzzz"), " "), ErrUnknownWord},
		"11 words":     {strings.Join(good[:11], " "), ErrWordCount},
		"empty":        {"", ErrWordCount},
		"13 words":     {abandonMnemonic + " about", ErrWordCount},
		"invalid utf8": {"abandon \xff", ErrInvalidText},
		"prefix only":  {strings.Join(append(append([]string{}, good[:11]...), "abo"), " "), ErrUnknownWord},
	}
	for name, tc := range tests {
		err := ValidateMnemonic(secret.FromString(tc.in))
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

func TestValidateIsLenientAboutFormat(t *testing.T) {
	messy := "  ABANDON\tabandon abandon\nabandon abandon abandon abandon abandon abandon abandon abandon About 　"
	m := secret.FromString(messy)
	if err := ValidateMnemonic(m); err != nil {
		t.Fatalf("messy but valid phrase rejected: %v", err)
	}
	canon, err := NormalizeMnemonic(m)
	if err != nil || string(canon.Expose()) != abandonMnemonic {
		t.Fatalf("NormalizeMnemonic = %q, %v", canon.Expose(), err)
	}
	a, _ := SeedFromMnemonic(m, secret.Bytes{})
	b, _ := SeedFromMnemonic(secret.FromString(abandonMnemonic), secret.Bytes{})
	if !a.Equal(b) {
		t.Error("formatting changes the seed")
	}
}

func TestAllWordCounts(t *testing.T) {
	for _, n := range []int{16, 20, 24, 28, 32} {
		e := make([]byte, n)
		for i := range e {
			e[i] = byte(i * 13)
		}
		m, err := MnemonicFromEntropy(e)
		if err != nil || len(strings.Fields(string(m.Expose()))) != n*3/4 {
			t.Fatalf("%d bytes: %v", n, err)
		}
		if err := ValidateMnemonic(m); err != nil {
			t.Errorf("%d bytes: %v", n, err)
		}
	}
	if _, err := MnemonicFromEntropy(make([]byte, 15)); !errors.Is(err, ErrInvalidEntropy) {
		t.Errorf("15 bytes: %v", err)
	}
}

func TestNewMnemonic(t *testing.T) {
	for _, n := range []int{Words12, Words24} {
		m, err := NewMnemonic(n)
		if err != nil {
			t.Fatal(err)
		}
		if len(strings.Fields(string(m.Expose()))) != n || ValidateMnemonic(m) != nil {
			t.Errorf("bad %d-word mnemonic", n)
		}
		other, _ := NewMnemonic(n)
		if m.Equal(other) {
			t.Error("two fresh mnemonics are equal")
		}
	}
	if _, err := NewMnemonic(15); !errors.Is(err, ErrUnsupportedLength) {
		t.Errorf("15 words: %v", err)
	}
}

func TestSuggestWords(t *testing.T) {
	if got := SuggestWords("aba", 5); fmt.Sprint(got) != "[abandon]" {
		t.Errorf("aba: %v", got)
	}
	if got := SuggestWords("ZO", 5); fmt.Sprint(got) != "[zone zoo]" {
		t.Errorf("ZO: %v", got)
	}
	if got := SuggestWords("ab", 3); len(got) != 3 || got[0] != "abandon" {
		t.Errorf("limit: %v", got)
	}
	if SuggestWords("", 5) != nil || SuggestWords("a", 0) != nil || len(SuggestWords("xyz", 5)) != 0 {
		t.Error("empty cases")
	}
}

// Ни ошибки, ни печать, ни slog не раскрывают слова мнемоники и парольную фразу.
func TestNoLeaksInErrorsAndFormatting(t *testing.T) {
	phrase := "zebra zebra zebra zebra zebra zebra zebra zebra zebra zebra zebra qqqqsecret"
	m := secret.FromString(phrase)
	_, err := SeedFromMnemonic(m, secret.FromString("hunter2-passphrase"))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, s := range []string{err.Error(), fmt.Sprintf("%v %+v %#v %s", m, m, m, m)} {
		if strings.Contains(s, "qqqqsecret") || strings.Contains(s, "zebra") || strings.Contains(s, "hunter2") {
			t.Errorf("leak: %q", s)
		}
	}
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("x", "m", m, "err", err)
	if strings.Contains(sb.String(), "qqqqsecret") || strings.Contains(sb.String(), "zebra") {
		t.Errorf("slog leak: %s", sb.String())
	}
}

func TestSeedWipe(t *testing.T) {
	seed, _ := SeedFromMnemonic(secret.FromString(abandonMnemonic), secret.Bytes{})
	seed.Wipe()
	if !seed.IsEmpty() {
		t.Error("seed not wiped")
	}
}

func FuzzValidateMnemonic(f *testing.F) {
	f.Add(abandonMnemonic)
	f.Add("")
	f.Add("abandon \xff")
	f.Add(strings.Repeat("zoo ", 24))
	f.Fuzz(func(t *testing.T, s string) {
		m := secret.FromString(s)
		err := ValidateMnemonic(m)
		canon, nerr := NormalizeMnemonic(m)
		if (err == nil) != (nerr == nil) {
			t.Fatalf("Validate and Normalize disagree: %v vs %v", err, nerr)
		}
		if err == nil {
			if verr := ValidateMnemonic(canon); verr != nil {
				t.Fatalf("canonical form invalid: %v", verr)
			}
		}
	})
}

// Регрессия: norm.NFKD.Bytes возвращает вход как есть, если он уже в форме
// NFKD, и раньше затирание копии обнуляло буфер вызывающего.
func TestSeedDoesNotModifyItsInputs(t *testing.T) {
	const pass = "привет мир"                           // кириллица без составных символов: уже NFKD
	mn := strings.Replace(abandonMnemonic, " ", " ", 3) // U+2028 тоже устойчив к NFKD
	p, m := secret.FromString(pass), secret.FromString(mn)

	first, err := SeedFromMnemonic(m, p)
	if err != nil {
		t.Fatal(err)
	}
	if string(p.Expose()) != pass || string(m.Expose()) != mn {
		t.Fatalf("inputs were modified: passphrase %q, mnemonic %q", p.Expose(), m.Expose())
	}
	second, err := SeedFromMnemonic(m, p)
	if err != nil || !first.Equal(second) {
		t.Fatalf("second call gives another seed (%v)", err)
	}
	if err := ValidateMnemonic(m); err != nil || string(m.Expose()) != mn {
		t.Fatalf("ValidateMnemonic changed its input: %v", err)
	}
}

func TestOverlongTokenIsUnknownWord(t *testing.T) {
	long := strings.Repeat("a", 4096)
	err := ValidateMnemonic(secret.FromString(strings.Replace(abandonMnemonic, "about", long, 1)))
	if !errors.Is(err, ErrUnknownWord) {
		t.Fatalf("got %v", err)
	}
}
