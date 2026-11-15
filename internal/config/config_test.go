package config_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/config"
	"github.com/24DSO-lab/wallet/internal/domain"
)

func TestMaxWallets(t *testing.T) {
	if config.MaxWallets != 3 {
		t.Errorf("MaxWallets = %d, ADR fixes it at 3", config.MaxWallets)
	}
}

func TestNewSeparatesProfileDirectories(t *testing.T) {
	base := filepath.Join("data", "Wallet")
	main, err := config.New(domain.ProfileMain, base, config.APIKeys{})
	if err != nil {
		t.Fatal(err)
	}
	test, err := config.New(domain.ProfileTest, base, config.APIKeys{})
	if err != nil {
		t.Fatal(err)
	}
	if main.DataDir == test.DataDir {
		t.Fatalf("profiles share a data dir: %s", main.DataDir)
	}
	if main.DataDir != filepath.Join(base, "main") || test.DataDir != filepath.Join(base, "test") {
		t.Errorf("unexpected dirs: %s, %s", main.DataDir, test.DataDir)
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	if _, err := config.New("", "base", config.APIKeys{}); !errors.Is(err, config.ErrInvalidProfile) {
		t.Errorf("empty profile: %v", err)
	}
	if _, err := config.New("Main", "base", config.APIKeys{}); !errors.Is(err, config.ErrInvalidProfile) {
		t.Errorf("wrong-case profile: %v", err)
	}
	if _, err := config.New(domain.ProfileMain, "", config.APIKeys{}); !errors.Is(err, config.ErrEmptyBaseDir) {
		t.Errorf("empty base: %v", err)
	}
}

func TestProvidersAreHTTPSAndComplete(t *testing.T) {
	for _, p := range []domain.Profile{domain.ProfileMain, domain.ProfileTest} {
		cfg, err := config.New(p, "base", config.APIKeys{})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []config.Provider{config.ProviderBTC, config.ProviderETHRPC, config.ProviderETHIndex, config.ProviderTron} {
			ep, ok := cfg.Providers[name]
			if !ok {
				t.Errorf("%s: provider %s missing", p, name)
				continue
			}
			for _, raw := range ep.All() {
				u, err := url.Parse(raw)
				if err != nil || u.Scheme != "https" || u.Host == "" {
					t.Errorf("%s/%s: %q is not an absolute https URL", p, name, raw)
				}
			}
		}
	}
}

func TestMainAndTestUseDifferentEndpoints(t *testing.T) {
	m, _ := config.New(domain.ProfileMain, "b", config.APIKeys{})
	x, _ := config.New(domain.ProfileTest, "b", config.APIKeys{})
	for name, ep := range m.Providers {
		for _, a := range ep.All() {
			for _, b := range x.Providers[name].All() {
				if a == b {
					t.Errorf("%s: main and test share endpoint %s", name, a)
				}
			}
		}
	}
}

func TestBTCCoinType(t *testing.T) {
	if config.BTCCoinType(domain.ProfileMain) != 0 || config.BTCCoinType(domain.ProfileTest) != 1 {
		t.Error("BTC coin type must be 0 for main and 1 for test (A4)")
	}
}

func TestEndpointsAll(t *testing.T) {
	ep := config.Endpoints{Primary: "a", Fallback: []string{"b", "c"}}
	if got := fmt.Sprint(ep.All()); got != "[a b c]" {
		t.Errorf("All() = %s", got)
	}
	if got := fmt.Sprint(config.Endpoints{Primary: "a"}.All()); got != "[a]" {
		t.Errorf("All() = %s", got)
	}
}

func TestAPIKeysNeverPrinted(t *testing.T) {
	const key = "k_live_9c1d5e7f2b"
	keys := config.NewAPIKeys(map[config.Provider]string{config.ProviderETHRPC: key, config.ProviderTron: ""})

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if out := fmt.Sprintf(verb, keys); strings.Contains(out, key) {
			t.Errorf("%s leaked the key: %s", verb, out)
		}
	}
	cfg, _ := config.New(domain.ProfileMain, "b", keys)
	if out := fmt.Sprintf("%+v", cfg); strings.Contains(out, key) {
		t.Errorf("printing Config leaked the key: %s", out)
	}
	if _, err := json.Marshal(keys); err == nil {
		t.Error("JSON encoding of keys must fail")
	}
	if _, err := json.Marshal(cfg); err == nil {
		t.Error("JSON encoding of Config must fail while it holds keys")
	}
	if v := slog.AnyValue(keys).Resolve().String(); strings.Contains(v, key) {
		t.Errorf("slog value leaked the key: %s", v)
	}

	if got, ok := keys.Get(config.ProviderETHRPC); !ok || got != key {
		t.Error("Get must return the stored key")
	}
	if _, ok := keys.Get(config.ProviderTron); ok {
		t.Error("empty keys must be dropped")
	}
	if vals := keys.Values(); len(vals) != 1 || vals[0] != key {
		t.Errorf("Values() = %v", vals)
	}
}

func TestAPIKeysCopiesInput(t *testing.T) {
	src := map[config.Provider]string{config.ProviderBTC: "abcd1234"}
	keys := config.NewAPIKeys(src)
	src[config.ProviderBTC] = "changed"
	if v, _ := keys.Get(config.ProviderBTC); v != "abcd1234" {
		t.Errorf("NewAPIKeys must copy its input, got %q", v)
	}
	var zero config.APIKeys
	if _, ok := zero.Get(config.ProviderBTC); ok || len(zero.Values()) != 0 {
		t.Error("zero value must hold no keys")
	}
}
