package secret_test

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/24DSO-lab/wallet/internal/secret"
)

// sentinel достаточно необычен, чтобы его наличие в выводе было однозначной утечкой.
const sentinel = "correct horse battery staple 7f3a"

func TestFormatNeverRevealsContent(t *testing.T) {
	s := secret.FromString(sentinel)
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T", "%p", "%10v", "%-30s"}
	for _, verb := range verbs {
		got := fmt.Sprintf(verb, s)
		if strings.Contains(got, sentinel) || strings.Contains(got, fmt.Sprintf("%x", sentinel)) {
			t.Errorf("verb %s leaked the secret: %q", verb, got)
		}
	}
	for _, v := range []any{s, &s, []secret.Bytes{s}, map[string]secret.Bytes{"k": s}} {
		if got := fmt.Sprint(v); strings.Contains(got, sentinel) {
			t.Errorf("Sprint(%T) leaked the secret: %q", v, got)
		}
	}
}

func TestPrintingContainingStructs(t *testing.T) {
	s := secret.FromString(sentinel)

	type exported struct{ Password secret.Bytes }
	type unexported struct{ password secret.Bytes }

	// Неэкспортируемое поле fmt печатает рефлексией, минуя Format: защищает
	// указатель внутри Bytes, который показывает адрес, а не данные.
	cases := map[string]any{
		"exported":       exported{Password: s},
		"exported ptr":   &exported{Password: s},
		"unexported":     unexported{password: s},
		"unexported ptr": &unexported{password: s},
	}
	for name, v := range cases {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			got := fmt.Sprintf(verb, v)
			if strings.Contains(got, sentinel) {
				t.Errorf("%s with %s leaked the secret: %q", name, verb, got)
			}
			// Сырая печать среза выглядела бы как список чисел.
			if strings.Contains(got, fmt.Sprint([]byte(sentinel))) {
				t.Errorf("%s with %s printed raw bytes: %q", name, verb, got)
			}
		}
	}
}

func TestSerializationIsRefused(t *testing.T) {
	s := secret.FromString(sentinel)

	if _, err := json.Marshal(s); !errors.Is(err, secret.ErrNotSerializable) {
		t.Errorf("json.Marshal error = %v, want ErrNotSerializable", err)
	}
	if _, err := json.Marshal(struct{ P secret.Bytes }{s}); !errors.Is(err, secret.ErrNotSerializable) {
		t.Errorf("json.Marshal(struct) error = %v, want ErrNotSerializable", err)
	}
	if _, err := s.MarshalText(); !errors.Is(err, secret.ErrNotSerializable) {
		t.Errorf("MarshalText error = %v, want ErrNotSerializable", err)
	}

	var back secret.Bytes
	if err := json.Unmarshal([]byte(`"abc"`), &back); !errors.Is(err, secret.ErrNotSerializable) {
		t.Errorf("json.Unmarshal error = %v, want ErrNotSerializable", err)
	}
	if !back.IsEmpty() {
		t.Error("Unmarshal must not fill the secret")
	}

	// gob не должен найти экспортируемых полей и отказаться.
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err == nil {
		t.Errorf("gob encoded a secret: %q", buf.String())
	}
}

func TestSlogNeverRevealsContent(t *testing.T) {
	s := secret.FromString(sentinel)
	type payload struct{ Password secret.Bytes }

	for name, newHandler := range map[string]func(*bytes.Buffer) slog.Handler{
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		var buf bytes.Buffer
		l := slog.New(newHandler(&buf))
		l.Info("event", "secret", s, slog.Any("ptr", &s), slog.Any("nested", payload{Password: s}),
			slog.Group("g", slog.Any("secret", s)))
		l.With("secret", s).Info("with")

		out := buf.String()
		if strings.Contains(out, sentinel) {
			t.Errorf("%s handler leaked the secret: %s", name, out)
		}
		if !strings.Contains(out, "REDACTED") && name == "text" {
			t.Errorf("%s handler output has no redaction marker: %s", name, out)
		}
	}
}

func TestErrorsWrappingDoNotLeak(t *testing.T) {
	s := secret.FromString(sentinel)
	err := fmt.Errorf("unlock failed for %v: %w", s, errors.New("wrong password"))
	if strings.Contains(err.Error(), sentinel) {
		t.Errorf("error text leaked the secret: %q", err.Error())
	}
}

func TestWipe(t *testing.T) {
	src := []byte(sentinel)
	s := secret.FromBytes(src)
	alias := s // копия указывает на тот же буфер
	view := s.Expose()

	s.Wipe()

	if !s.IsEmpty() || !alias.IsEmpty() {
		t.Error("Wipe must empty every copy")
	}
	if s.Expose() != nil || alias.Expose() != nil {
		t.Error("Expose after Wipe must return nil")
	}
	for i, b := range view {
		if b != 0 {
			t.Fatalf("byte %d not zeroed after Wipe", i)
		}
	}
	for i, b := range src {
		if b != 0 {
			t.Fatalf("FromBytes must take ownership: source byte %d not zeroed", i)
		}
	}

	s.Wipe() // повторный вызов безопасен
}

func TestZeroValue(t *testing.T) {
	var s secret.Bytes
	if !s.IsEmpty() || s.Len() != 0 || s.Expose() != nil {
		t.Error("zero value must be an empty secret")
	}
	s.Wipe()
	if !s.Clone().IsEmpty() {
		t.Error("clone of empty must be empty")
	}
	if !s.Equal(secret.Bytes{}) {
		t.Error("two empty secrets must be equal")
	}
	if !secret.FromBytes(nil).IsEmpty() || !secret.Copy(nil).IsEmpty() || !secret.FromString("").IsEmpty() {
		t.Error("constructors must give an empty secret for empty input")
	}
}

func TestCopyAndCloneAreIndependent(t *testing.T) {
	src := []byte("abcdef")
	c := secret.Copy(src)
	src[0] = 'X'
	if string(c.Expose()) != "abcdef" {
		t.Error("Copy must not alias the source")
	}

	clone := c.Clone()
	c.Wipe()
	if string(clone.Expose()) != "abcdef" {
		t.Error("Clone must survive Wipe of the original")
	}
	clone.Wipe()
}

func TestFromStringCopies(t *testing.T) {
	s := secret.FromString("hunter2")
	defer s.Wipe()
	if s.Len() != 7 || string(s.Expose()) != "hunter2" {
		t.Errorf("unexpected content, len=%d", s.Len())
	}
}

func TestEqual(t *testing.T) {
	a := secret.FromString("same")
	b := secret.FromString("same")
	c := secret.FromString("other")
	d := secret.FromString("sam")
	defer a.Wipe()
	defer b.Wipe()
	defer c.Wipe()
	defer d.Wipe()

	if !a.Equal(b) {
		t.Error("equal secrets reported different")
	}
	if a.Equal(c) || a.Equal(d) {
		t.Error("different secrets reported equal")
	}
}
