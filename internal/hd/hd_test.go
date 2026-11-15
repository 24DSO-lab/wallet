package hd_test

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/hd"
	"github.com/24DSO-lab/wallet/internal/hd/hdtest"
	"github.com/24DSO-lab/wallet/internal/secret"
)

const abandon = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

type goldenSet struct {
	xpub, tpub                                 string
	btcMain00, btcMain01, btcMain10, btcTest00 string
	ethPub, eth, tronPub, tron                 string
}

// Гейт этапа 1. Значения выведены независимой реализацией на Python
// (hashlib, OpenSSL через cryptography, собственный Keccak) и сверены с
// опубликованными: BIP84 (bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu и
// следующие), адрес ETH 0x9858EfFD232B4033E47d90003D41EC34EcaEda94 для
// «abandon … about» и вектор BIP32 №1. Ключ "none" это без парольной
// фразы, "trezor" с парольной фразой TREZOR.
var golden = map[string]goldenSet{
	"none": {
		xpub: "xpub6CatWdiZiodmUeTDp8LT5or8nmbKNcuyvz7WyksVFkKB4RHwCD3XyuvPEbvqAQY3rAPshWcMLoP2fMFMKHPJ4ZeZXYVUhLv1VMrjPC7PW6V", tpub: "tpubDC8msFGeGuwnKG9Upg7DM2b4DaRqg3CUZa5g8v2SRQ6K4NSkxUgd7HsL2XVWbVm39yBA4LAxysQAm397zwQSQoQgewGiYZqrA9DsP4zbQ1M",
		btcMain00: "bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu", btcMain01: "bc1qnjg0jd8228aq7egyzacy8cys3knf9xvrerkf9g", btcMain10: "bc1q8c6fshw2dlwun7ekn9qwf37cu2rn755upcp6el", btcTest00: "tb1q6rz28mcfaxtmd6v789l9rrlrusdprr9pqcpvkl",
		ethPub: "0437b0bb7a8288d38ed49a524b5dc98cff3eb5ca824c9f9dc0dfdb3d9cd600f299a6179912b7451c09896c4098eca7ce6b2e58330672795e847c4d6af44e024230", eth: "0x9858EfFD232B4033E47d90003D41EC34EcaEda94", tronPub: "04ff21f8e64d3a3c0198edfbb7afdc79be959432e92e2f8a1984bb436a414b8edcec0345aad0c1bf7da04fd036dd7f9f617e30669224283d950fab9dd84831dc83", tron: "TUEZSdKsoDHQMeZwihtdoBiN46zxhGWYdH",
	},
	"trezor": {
		xpub: "xpub6Crgkie5Rb7wDabkf4Uf6A2qnuERMA3p2QrnmHNQDrsXTaGvz9zugU38Apne8WqrcbSjdLwbhtfHrzWjNCJPVAkkNoQhMfzhBm8rKMA8KxH", tpub: "tpubDC3QUzyNJLFENW1qz75ATkef1wkGURb2bvPiBd7ty35wpR9oTzV12aHjzWFr9eZDnL5H9Wqbq6riaHcfa4FR4iv22uFLufe7Ldor9jr2MJF",
		btcMain00: "bc1qv5rmq0kt9yz3pm36wvzct7p3x6mtgehjul0feu", btcMain01: "bc1qq4w9xyzsylpcy6hd76xr8pecxsvzqe743t2uj0", btcMain10: "bc1qest2x6zfncjezklycdghhjxzlptky4xx6rgewa", btcTest00: "tb1qfwxzlmn42rh6wu6gt4f0yrfpe4gf0jtwgxagrr",
		ethPub: "04986dee3b8afe24cb8ccb2ac23dac3f8c43d22850d14b809b26d6b8aa5a1f47784152cd2c7d9edd0ab20392a837464b5a750b2a7f3f06e6a5756b5211b6a6ed05", eth: "0x9c32F71D4DB8Fb9e1A58B0a80dF79935e7256FA6", tronPub: "04111c4267dff570eaf2d2be12e0bdb9e3a7dbdb8a765ba86bf4879aa2a5595964b58c5264bcf9cd6619633ad67fc5db0dafe13bece071f1dce8894038ede28d66", tron: "TAyDUYP5rcf56xFwrg8cU1qQwvnWpkeapM",
	},
}

var bip32Vector1 = map[string]string{
	"m":                      "xpub661MyMwAqRbcFtXgS5sYJABqqG9YLmC4Q1Rdap9gSE8NqtwybGhePY2gZ29ESFjqJoCu1Rupje8YtGqsefD265TMg7usUDFdp6W1EGMcet8",
	"m/0'":                   "xpub68Gmy5EdvgibQVfPdqkBBCHxA5htiqg55crXYuXoQRKfDBFA1WEjWgP6LHhwBZeNK1VTsfTFUHCdrfp1bgwQ9xv5ski8PX9rL2dZXvgGDnw",
	"m/0'/1":                 "xpub6ASuArnXKPbfEwhqN6e3mwBcDTgzisQN1wXN9BJcM47sSikHjJf3UFHKkNAWbWMiGj7Wf5uMash7SyYq527Hqck2AxYysAA7xmALppuCkwQ",
	"m/0'/1/2'/2/1000000000": "xpub6H1LXWLaKsWFhvm6RVpEL9P4KfRZSW7abD2ttkWP3SSQvnyA8FSVqNTEcYFgJS2UaFcxupHiYkro49S8yGasTvXEYBVPamhGW6cFJodrTHy",
}

func seedFor(t testing.TB, passphrase string) secret.Bytes {
	t.Helper()
	seed, err := hd.SeedFromMnemonic(secret.FromString(abandon), secret.FromString(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	return seed
}

func TestBIP32OfficialVector1(t *testing.T) {
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	master, err := hd.MasterKey(secret.FromBytes(seed))
	if err != nil {
		t.Fatal(err)
	}
	defer master.Wipe()
	for path, want := range bip32Vector1 {
		p, err := hd.ParsePath(path)
		if err != nil {
			t.Fatal(err)
		}
		k, err := master.DerivePath(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := k.PublicKey().Serialize(domain.ProfileMain)
		k.Wipe()
		if err != nil || got != want {
			t.Errorf("%s: xpub %s (%v), want %s", path, got, err, want)
		}
	}
}

func TestGateAddressesMatchVectors(t *testing.T) {
	for name, want := range golden {
		passphrase := map[string]string{"none": "", "trezor": "TREZOR"}[name]
		t.Run(name, func(t *testing.T) {
			seed := seedFor(t, passphrase)
			defer seed.Wipe()

			main, err := hd.DerivePublicData(seed, domain.ProfileMain)
			if err != nil {
				t.Fatal(err)
			}
			test, err := hd.DerivePublicData(seed, domain.ProfileTest)
			if err != nil {
				t.Fatal(err)
			}
			if main.BTCXpub != want.xpub || test.BTCXpub != want.tpub {
				t.Errorf("xpub/tpub: %s / %s", main.BTCXpub, test.BTCXpub)
			}
			if !strings.HasPrefix(main.BTCXpub, "xpub") || !strings.HasPrefix(test.BTCXpub, "tpub") {
				t.Error("wrong serialization prefix (zpub is not allowed, A8)")
			}
			// Профиль влияет только на BTC: ETH и TRON одинаковы.
			if main.ETHPublicKey != test.ETHPublicKey || main.TronPublicKey != test.TronPublicKey {
				t.Error("ETH/TRON keys depend on the profile")
			}
			if got := hex.EncodeToString(main.ETHPublicKey[:]); got != want.ethPub {
				t.Errorf("eth pub %s", got)
			}
			if eth, _ := hdtest.ETHAddress(main.ETHPublicKey[:]); eth != want.eth {
				t.Errorf("ETH address %s, want %s", eth, want.eth)
			}
			if tron, _ := hdtest.TronAddress(main.TronPublicKey[:]); tron != want.tron {
				t.Errorf("TRON address %s, want %s", tron, want.tron)
			}

			// Адреса BTC строятся из xpub так, как это сделает адаптер.
			for _, tc := range []struct {
				xpub, hrp string
				chain, i  uint32
				want      string
			}{
				{main.BTCXpub, "bc", 0, 0, want.btcMain00},
				{main.BTCXpub, "bc", 0, 1, want.btcMain01},
				{main.BTCXpub, "bc", 1, 0, want.btcMain10},
				{test.BTCXpub, "tb", 0, 0, want.btcTest00},
			} {
				acct, profile, err := hd.ParsePublicKey(tc.xpub)
				if err != nil {
					t.Fatal(err)
				}
				if (tc.hrp == "bc") != (profile == domain.ProfileMain) {
					t.Errorf("profile %s for hrp %s", profile, tc.hrp)
				}
				ch, err := acct.Derive(tc.chain)
				if err != nil {
					t.Fatal(err)
				}
				leaf, err := ch.Derive(tc.i)
				if err != nil {
					t.Fatal(err)
				}
				if got := hdtest.P2WPKHAddress(tc.hrp, leaf.Hash160()); got != tc.want {
					t.Errorf("%s/%d/%d: %s, want %s", tc.hrp, tc.chain, tc.i, got, tc.want)
				}
			}
		})
	}
}

// Публичная деривация обязана совпадать с приватной на всех неусиленных индексах.
func TestPublicDerivationMatchesPrivate(t *testing.T) {
	seed := seedFor(t, "")
	defer seed.Wipe()
	master, _ := hd.MasterKey(seed)
	defer master.Wipe()
	account, _ := hd.BTCAccountPath(domain.ProfileMain)
	acct, err := master.DerivePath(account)
	if err != nil {
		t.Fatal(err)
	}
	defer acct.Wipe()
	pub := acct.PublicKey()
	for _, idx := range []uint32{0, 1, 19, 20, 1 << 20, hd.Hardened - 1} {
		priv, err := acct.Derive(idx)
		if err != nil {
			t.Fatal(err)
		}
		fromPub, err := pub.Derive(idx)
		if err != nil {
			t.Fatal(err)
		}
		a, b := priv.PublicKey().Compressed(), fromPub.Compressed()
		if a != b {
			t.Errorf("index %d: private and public derivation differ", idx)
		}
		priv.Wipe()
	}
	if _, err := pub.Derive(hd.Hardened); !errors.Is(err, hd.ErrHardenedFromPub) {
		t.Errorf("hardened from public: %v", err)
	}
}

func TestPassphraseTypoGivesOtherAddress(t *testing.T) {
	addr := func(pass string) string {
		seed := seedFor(t, pass)
		defer seed.Wipe()
		pd, err := hd.DerivePublicData(seed, domain.ProfileMain)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := hdtest.ETHAddress(pd.ETHPublicKey[:])
		return a
	}
	if addr("correct horse") == addr("correct horsE") || addr("") == addr(" ") {
		t.Error("a passphrase typo must change the derived address")
	}
}

func TestPathParsing(t *testing.T) {
	for in, want := range map[string]string{
		"m": "m", "m/84'/0'/0'": "m/84'/0'/0'", "m/84h/0h/0'/1/5": "m/84'/0'/0'/1/5",
		"m/44'/60'/0'/0/0": "m/44'/60'/0'/0/0", "m/2147483647'": "m/2147483647'",
	} {
		p, err := hd.ParsePath(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		// h и H записываются как ' при выводе.
		if got := p.String(); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "M", "84'/0'", "m/", "m//1", "m/01", "m/-1", "m/+1", "m/1 ", "m/1''", "m/2147483648", "m/'", "m/1/2/3/4/5/6/7/8/9/10/11", "m/0x1", "m/1.5", "m/1H", "m/99999999999"} {
		if _, err := hd.ParsePath(bad); !errors.Is(err, hd.ErrInvalidPath) {
			t.Errorf("%q accepted (%v)", bad, err)
		}
	}
}

func TestCheckPathForNetwork(t *testing.T) {
	ok := func(n domain.Network, pr domain.Profile, path string) bool {
		p, _ := hd.ParsePath(path)
		return hd.CheckPathForNetwork(n, pr, p) == nil
	}
	cases := []struct {
		n    domain.Network
		pr   domain.Profile
		path string
		want bool
	}{
		{domain.NetworkBitcoin, domain.ProfileMain, "m/84'/0'/0'/0/7", true},
		{domain.NetworkBitcoin, domain.ProfileMain, "m/84'/0'/0'/1/0", true},
		{domain.NetworkBitcoin, domain.ProfileTest, "m/84'/1'/0'/0/0", true},
		{domain.NetworkBitcoin, domain.ProfileMain, "m/84'/1'/0'/0/0", false}, // монета теста в основном профиле
		{domain.NetworkBitcoin, domain.ProfileTest, "m/84'/0'/0'/0/0", false},
		{domain.NetworkBitcoin, domain.ProfileMain, "m/84'/0'/1'/0/0", false}, // другой счёт
		{domain.NetworkBitcoin, domain.ProfileMain, "m/44'/0'/0'/0/0", false}, // не BIP84
		{domain.NetworkBitcoin, domain.ProfileMain, "m/84'/0'/0'/2/0", false},
		{domain.NetworkBitcoin, domain.ProfileMain, "m/84'/0'/0'/0/0'", false},
		{domain.NetworkEthereum, domain.ProfileMain, "m/44'/60'/0'/0/0", true},
		{domain.NetworkEthereum, domain.ProfileMain, "m/44'/60'/0'/0/1", false},
		{domain.NetworkEthereum, domain.ProfileMain, "m/44'/195'/0'/0/0", false},
		{domain.NetworkTron, domain.ProfileMain, "m/44'/195'/0'/0/0", true},
		{domain.NetworkTron, domain.ProfileMain, "m/44'/60'/0'/0/0", false},
		{domain.Network("other"), domain.ProfileMain, "m/44'/60'/0'/0/0", false},
	}
	for _, c := range cases {
		if got := ok(c.n, c.pr, c.path); got != c.want {
			t.Errorf("%s %s %s: %v, want %v", c.n, c.pr, c.path, got, c.want)
		}
	}
}

func TestParsePublicKeyRejects(t *testing.T) {
	seed := seedFor(t, "")
	defer seed.Wipe()
	pd, _ := hd.DerivePublicData(seed, domain.ProfileMain)
	good := pd.BTCXpub
	broken := []byte(good)
	broken[10] ^= 1
	xprv := "xprv9s21ZrQH143K3QTDL4LXw2F7HEK3wJUD2nW2nRk4stbPy6cq3jPPqjiChkVvvNKmPGJxWUtg6LnF5kejMRNNU3TGtRBeJgk33yuGBxrMPHi"
	for name, in := range map[string]string{
		"empty": "", "garbage": "not base58 !!", "checksum": string(broken), "xprv": xprv, "short": good[:40],
	} {
		if _, _, err := hd.ParsePublicKey(in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Закрытый ключ не попадает в текст ошибки.
	_, _, err := hd.ParsePublicKey(xprv)
	if err != nil && strings.Contains(err.Error(), xprv[:20]) {
		t.Error("error text echoes the key")
	}
}

func TestSecretsDoNotLeak(t *testing.T) {
	seed := seedFor(t, "pp")
	defer seed.Wipe()
	master, _ := hd.MasterKey(seed)
	defer master.Wipe()
	keyHex := hex.EncodeToString(master.Secret().Expose())
	seedHex := hex.EncodeToString(seed.Expose())
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("k", "key", master.Secret(), "seed", seed)
	all := fmt.Sprintf("%v %+v %s %x %d", master.Secret(), seed, seed, seed, seed) + sb.String()
	if strings.Contains(all, keyHex) || strings.Contains(all, seedHex) || strings.Contains(all, "pp") && strings.Contains(all, "mnemonic") {
		t.Error("secret material is printed")
	}
}

func TestWipeAndIndependence(t *testing.T) {
	seed := seedFor(t, "")
	defer seed.Wipe()
	master, _ := hd.MasterKey(seed)
	clone, err := master.DerivePath(nil)
	if err != nil {
		t.Fatal(err)
	}
	clone.Wipe()
	if master.Secret().IsEmpty() {
		t.Error("wiping a clone wiped the original")
	}
	master.Wipe()
	if !master.Secret().IsEmpty() {
		t.Error("Wipe left the key in place")
	}
}

func TestSeedLengthRejected(t *testing.T) {
	for _, n := range []int{0, 15, 65} {
		if _, err := hd.MasterKey(secret.FromBytes(make([]byte, n))); !errors.Is(err, hd.ErrInvalidSeed) {
			t.Errorf("seed of %d bytes: %v", n, err)
		}
	}
}

func FuzzParsePath(f *testing.F) {
	for _, s := range []string{"m", "m/84'/0'/0'/0/0", "m/1h", "", "m/99999999999", "m/'"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := hd.ParsePath(s)
		if err != nil {
			return
		}
		// Принятый путь обязан давать тот же путь после печати и разбора.
		back, err := hd.ParsePath(p.String())
		if err != nil || back.String() != p.String() {
			t.Fatalf("%q → %q does not round trip: %v", s, p.String(), err)
		}
	})
}

func FuzzParsePublicKey(f *testing.F) {
	seed := secret.FromBytes(make([]byte, 32))
	pd, _ := hd.DerivePublicData(seed, domain.ProfileMain)
	f.Add(pd.BTCXpub)
	f.Add("")
	f.Add("xpub")
	f.Fuzz(func(t *testing.T, s string) {
		pk, profile, err := hd.ParsePublicKey(s)
		if err != nil {
			return
		}
		again, err := pk.Serialize(profile)
		if err != nil || again != s {
			t.Fatalf("accepted %q but serializes as %q (%v)", s, again, err)
		}
	})
}
