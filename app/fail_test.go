package app

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/logging"
)

func TestFailLogsTheCauseButReturnsOnlyTheCode(t *testing.T) {
	var buf bytes.Buffer
	redactor := logging.NewRedactor("SECRETKEY123")
	a := NewWithDependencies(Dependencies{Logger: logging.New(&buf, slog.LevelInfo, redactor)})

	cause := domain.NewError(domain.CodeProviderUnavailable, errors.New("GET https://rpc.example/v2/SECRETKEY123 failed: EOF"))
	got := a.fail(cause)

	if got == nil || got.Error() != "provider_unavailable" {
		t.Fatalf("fail returned %v, want the bare code", got)
	}
	log := buf.String()
	for _, want := range []string{"request failed", "code=provider_unavailable", "op=TestFailLogsTheCauseButReturnsOnlyTheCode", "EOF"} {
		if !strings.Contains(log, want) {
			t.Errorf("log %q does not contain %q", log, want)
		}
	}
	if strings.Contains(log, "SECRETKEY123") {
		t.Fatalf("API key leaked into the log: %s", log)
	}
}

func TestFailWithoutALoggerStillReturnsTheCode(t *testing.T) {
	a := NewWithDependencies(Dependencies{})
	if err := a.fail(domain.NewError(domain.CodeNotFound, errors.New("x"))); err == nil || err.Error() != "not_found" {
		t.Fatalf("fail = %v, want not_found", err)
	}
	if a.fail(nil) != nil {
		t.Fatal("fail(nil) must return nil")
	}
}
