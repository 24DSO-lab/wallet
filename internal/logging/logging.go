// Package logging настраивает log/slog так, чтобы секреты и ключи API не
// попадали в лог, даже если их передали по ошибке.
//
// Защита в два слоя:
//   - secret.Bytes сам печатает "[REDACTED]" (slog.LogValuer);
//   - обработчик Redactor маскирует атрибуты с чувствительными именами и
//     вырезает из строк значения, которые ему зарегистрировали (ключи API).
package logging

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// Mask это текст вместо скрытого значения.
const Mask = "[REDACTED]"

// sensitiveKey находит имена атрибутов, значения которых скрываются целиком.
var sensitiveKey = regexp.MustCompile(`(?i)(pass(word|phrase)?|seed|mnemonic|secret|private|api[_-]?key|apikey|token|auth(orization)?|xprv|wif)`)

// queryParam находит значения чувствительных параметров URL внутри строк.
var queryParam = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|apikey|key|token|access_token)=)[^&\s"']+`)

// Redactor хранит значения, которые нужно вырезать из любых строк лога.
// Безопасен для вызова из нескольких горутин.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

// NewRedactor создаёт Redactor. Пустые значения игнорируются, чтобы не
// превратить каждый текст в маску.
func NewRedactor(values ...string) *Redactor {
	r := &Redactor{}
	r.Add(values...)
	return r
}

// Add регистрирует значения, которые нужно скрывать (ключи API провайдеров).
// Значения короче четырёх символов игнорируются: вырезание короткой строки
// портило бы весь лог и не защищало бы ничего.
func (r *Redactor) Add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if len(v) >= 4 {
			r.secrets = append(r.secrets, v)
		}
	}
}

// Scrub вырезает из s зарегистрированные значения и чувствительные параметры
// URL. Используйте и для текстов ошибок, которые уходят в лог или интерфейс.
func (r *Redactor) Scrub(s string) string {
	if r == nil {
		return queryParam.ReplaceAllString(s, "${1}"+Mask)
	}
	r.mu.RLock()
	vals := r.secrets
	r.mu.RUnlock()
	for _, v := range vals {
		s = strings.ReplaceAll(s, v, Mask)
		if esc := url.QueryEscape(v); esc != v {
			s = strings.ReplaceAll(s, esc, Mask)
		}
	}
	return queryParam.ReplaceAllString(s, "${1}"+Mask)
}

// New возвращает slog.Logger, пишущий в w через маскирующий обработчик.
// Формат: текст, уровень level.
func New(w io.Writer, level slog.Level, r *Redactor) *slog.Logger {
	inner := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(NewHandler(inner, r))
}

// Handler оборачивает slog.Handler и маскирует атрибуты и сообщения.
type Handler struct {
	next slog.Handler
	r    *Redactor
}

// NewHandler создаёт маскирующий обработчик поверх next.
func NewHandler(next slog.Handler, r *Redactor) *Handler {
	return &Handler{next: next, r: r}
}

// Enabled передаёт решение внутреннему обработчику.
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

// Handle маскирует сообщение и атрибуты записи.
func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.Scrub(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.scrub(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

// WithAttrs маскирует постоянные атрибуты.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	scrubbed := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		scrubbed[i] = h.scrub(a)
	}
	return &Handler{next: h.next.WithAttrs(scrubbed), r: h.r}
}

// WithGroup передаёт группу внутреннему обработчику.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name), r: h.r}
}

func (h *Handler) scrub(a slog.Attr) slog.Attr {
	if sensitiveKey.MatchString(a.Key) {
		return slog.String(a.Key, Mask)
	}
	// Resolve вызывает LogValue у secret.Bytes и других LogValuer.
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		group := v.Group()
		out := make([]slog.Attr, len(group))
		for i, g := range group {
			out[i] = h.scrub(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	case slog.KindString:
		return slog.String(a.Key, h.r.Scrub(v.String()))
	case slog.KindAny:
		// Значения произвольных типов (ошибки, структуры) приводятся к строке
		// после маскирования, чтобы вложенный ключ API не прошёл мимо.
		return slog.String(a.Key, h.r.Scrub(sprint(v.Any())))
	}
	return slog.Attr{Key: a.Key, Value: v}
}
