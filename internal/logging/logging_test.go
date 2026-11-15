package logging_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/logging"
	"github.com/24DSO-lab/wallet/internal/secret"
)

const (
	seedWords = "abandon abandon abandon abandon abandon about 7f3a"
	apiKey    = "k_live_9c1d5e7f2b"
)

func newLogger(buf *bytes.Buffer) *slog.Logger {
	return logging.New(buf, slog.LevelDebug, logging.NewRedactor(apiKey))
}

func assertClean(t *testing.T, out string) {
	t.Helper()
	for _, bad := range []string{seedWords, apiKey, "abandon"} {
		if strings.Contains(out, bad) {
			t.Errorf("log leaked %q:\n%s", bad, out)
		}
	}
}

func TestSecretBytesNeverReachesLog(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf)
	s := secret.FromString(seedWords)
	defer s.Wipe()

	type payload struct{ Value secret.Bytes }

	l.Info("direct", "value", s)
	l.Info("any", slog.Any("value", &s))
	l.Info("nested", slog.Any("p", payload{Value: s}))
	l.Info("group", slog.Group("g", slog.Any("value", s)))
	l.With("value", s).Info("with")
	l.WithGroup("grp").Info("grouped", "value", s)
	l.Info(fmt.Sprintf("formatted %v and %s", s, s))
	l.Error("failure", "err", fmt.Errorf("unlock: %v", s))

	out := buf.String()
	assertClean(t, out)
	if !strings.Contains(out, "REDACTED") {
		t.Errorf("expected a redaction marker:\n%s", out)
	}
}

func TestSensitiveKeyNamesAreMasked(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf)

	for _, key := range []string{"password", "Passphrase", "seed", "mnemonic", "api_key", "apiKey", "token", "Authorization", "private_key", "xprv"} {
		buf.Reset()
		l.Info("event", key, "plain-text-value-123")
		if strings.Contains(buf.String(), "plain-text-value-123") {
			t.Errorf("value under key %q was not masked: %s", key, buf.String())
		}
	}

	buf.Reset()
	l.Info("event", "wallet", "w1", "asset", "BTC")
	if !strings.Contains(buf.String(), "wallet=w1") || !strings.Contains(buf.String(), "asset=BTC") {
		t.Errorf("harmless attributes must stay readable: %s", buf.String())
	}
}

func TestAPIKeyScrubbedFromStringsAndErrors(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf)

	l.Info("request to https://api.example.com/v1?apikey="+apiKey+"&x=1", "url", "https://api.example.com/v1?apikey="+apiKey)
	l.Error("failed", "err", errors.New("Get \"https://api.example.com/v1?key="+apiKey+"\": timeout"))
	l.Info("header", "note", "Bearer "+apiKey)

	assertClean(t, buf.String())
}

func TestUnregisteredQueryKeysAreStillMasked(t *testing.T) {
	var buf bytes.Buffer
	l := logging.New(&buf, slog.LevelInfo, nil) // без зарегистрированных значений

	l.Info("call", "url", "https://x.example/api?api_key=SECRETVALUE99&page=2")
	out := buf.String()
	if strings.Contains(out, "SECRETVALUE99") {
		t.Errorf("query parameter leaked: %s", out)
	}
	if !strings.Contains(out, "page=2") {
		t.Errorf("harmless parameter lost: %s", out)
	}
}

func TestScrub(t *testing.T) {
	r := logging.NewRedactor(apiKey, "ab", "") // короткие и пустые значения игнорируются
	if got := r.Scrub("plain text with ab inside"); got != "plain text with ab inside" {
		t.Errorf("short value must not be redacted, got %q", got)
	}
	if got := r.Scrub("key=" + apiKey + " end"); strings.Contains(got, apiKey) {
		t.Errorf("registered key leaked: %q", got)
	}
	r.Add("late_registered_key")
	if got := r.Scrub("x late_registered_key y"); strings.Contains(got, "late_registered_key") {
		t.Errorf("late-registered key leaked: %q", got)
	}

	var nilR *logging.Redactor
	if got := nilR.Scrub("?token=abc123"); strings.Contains(got, "abc123") {
		t.Errorf("nil redactor must still mask query keys: %q", got)
	}
}

func TestLevelIsRespected(t *testing.T) {
	var buf bytes.Buffer
	l := logging.New(&buf, slog.LevelWarn, nil)
	l.Info("hidden")
	l.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Errorf("level filtering broken: %s", buf.String())
	}
}
