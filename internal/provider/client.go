// Package provider это общий HTTP-клиент для внешних API (Esplora, Ethereum
// RPC, Etherscan, TronGrid). Адаптеры получают его через интерфейс Client в
// конструкторе и сами http.Client не создают.
//
// Что делает клиент:
//   - таймаут на каждую попытку и лимит размера ответа;
//   - повторы с экспоненциальной паузой и джиттером, но только для
//     идемпотентных запросов;
//   - резервные адреса: после исчерпания попыток идёт следующий адрес;
//   - ограничитель частоты запросов;
//   - ошибки без адресов запросов и без ключей API;
//   - запрет редиректов: провайдер не должен перенаправлять наши запросы.
//
// Данные провайдера не доверенные: клиент отдаёт тело ответа как есть, а
// проверку получателя, суммы и комиссии делают адаптеры перед подписью.
package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/logging"
)

// Client это тонкий интерфейс над HTTP-клиентом. Do возвращает ответ для
// любого HTTP-статуса, по которому сервер ответил; ошибка nil только для 2xx.
// Для статусов 4xx ответ возвращается вместе с ошибкой, чтобы адаптер мог
// прочитать причину отказа (например, при отклонении отправки).
type Client interface {
	Do(ctx context.Context, req Request) (*Response, error)
}

// Request это запрос к провайдеру.
type Request struct {
	Method string      // пусто означает GET
	Path   string      // добавляется к базовому адресу, пусто или начинается с "/"
	Query  url.Values  // параметры запроса
	Header http.Header // заголовки поверх статических
	Body   []byte

	// Idempotent разрешает повторы и резервные адреса для запроса, который по
	// методу идемпотентным не считается (чтение по JSON-RPC через POST).
	// Для GET, HEAD и OPTIONS повторы разрешены всегда. Отправку транзакции
	// помечать нельзя: после обрыва неизвестно, дошёл ли запрос.
	Idempotent bool
}

// Response это ответ провайдера.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Ошибки клиента. Все они приходят обёрнутыми в domain.AppError с кодом.
var (
	// ErrResponseTooLarge: ответ превысил лимит размера.
	ErrResponseTooLarge = errors.New("provider: response exceeds the size limit")
	// ErrBadRequest: запрос не удалось собрать (неверный путь или метод).
	ErrBadRequest = errors.New("provider: invalid request")
)

// StatusError это ошибка по HTTP-статусу, без адреса запроса.
type StatusError struct{ Status int }

func (e *StatusError) Error() string { return "unexpected HTTP status " + strconv.Itoa(e.Status) }

// Значения по умолчанию.
const (
	DefaultTimeout          = 15 * time.Second
	DefaultMaxAttempts      = 3
	DefaultBaseDelay        = 300 * time.Millisecond
	DefaultMaxDelay         = 5 * time.Second
	DefaultMaxResponseBytes = 4 << 20
)

// Options настраивает HTTPClient. Нулевые значения означают значения по
// умолчанию, кроме MinInterval: ноль отключает ограничитель.
type Options struct {
	// Endpoints это основной базовый адрес и резервные по порядку. Адреса
	// только https; http допустим для loopback (тесты на httptest).
	Endpoints []string

	Timeout          time.Duration // на одну попытку
	MaxAttempts      int           // на один адрес, включая первую
	BaseDelay        time.Duration
	MaxDelay         time.Duration
	MaxResponseBytes int64
	MinInterval      time.Duration // наименьший интервал между началами запросов

	Header http.Header // статические заголовки (например, ключ API)

	// Redactor вырезает ключи API из текстов ошибок. Ключи, вшитые в адрес
	// (Alchemy), обязательно регистрируйте в нём.
	Redactor *logging.Redactor

	Transport http.RoundTripper // nil означает http.DefaultTransport

	// Sleep и Rand подменяются в тестах. Sleep должна вернуть ошибку контекста,
	// если контекст завершился раньше паузы. Rand возвращает число из [0, 1).
	Sleep func(ctx context.Context, d time.Duration) error
	Rand  func() float64
}

// HTTPClient это реализация Client. Безопасен для нескольких горутин.
type HTTPClient struct {
	opts    Options
	bases   []string
	http    *http.Client
	limiter *limiter
}

var _ Client = (*HTTPClient)(nil)

// New создаёт клиент и проверяет адреса.
func New(opts Options) (*HTTPClient, error) {
	if len(opts.Endpoints) == 0 {
		return nil, errors.New("provider: no endpoints")
	}
	bases := make([]string, 0, len(opts.Endpoints))
	for _, raw := range opts.Endpoints {
		if err := checkBase(raw); err != nil {
			return nil, err
		}
		bases = append(bases, strings.TrimRight(raw, "/"))
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = DefaultMaxAttempts
	}
	if opts.BaseDelay <= 0 {
		opts.BaseDelay = DefaultBaseDelay
	}
	if opts.MaxDelay <= 0 {
		opts.MaxDelay = DefaultMaxDelay
	}
	if opts.MaxResponseBytes <= 0 {
		opts.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if opts.Sleep == nil {
		opts.Sleep = sleep
	}
	if opts.Rand == nil {
		opts.Rand = rand.Float64
	}
	hc := &http.Client{
		Transport: opts.Transport,
		// Редиректы запрещены: провайдер не должен уводить запрос на другой
		// хост вместе с нашими заголовками.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &HTTPClient{
		opts:    opts,
		bases:   bases,
		http:    hc,
		limiter: newLimiter(opts.MinInterval, time.Now, opts.Sleep),
	}, nil
}

func checkBase(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return fmt.Errorf("provider: invalid endpoint (must be an absolute URL without credentials)")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("provider: endpoint must use https")
}

// Do выполняет запрос с повторами и резервными адресами.
func (c *HTTPClient) Do(ctx context.Context, req Request) (*Response, error) {
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	if req.Path != "" && !strings.HasPrefix(req.Path, "/") {
		return nil, domain.NewError(domain.CodeInternal, fmt.Errorf("%w: path must start with /", ErrBadRequest))
	}
	retryable := req.Idempotent || method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions

	var last error
	for _, base := range c.bases {
		for attempt := 1; attempt <= c.opts.MaxAttempts; attempt++ {
			if err := c.limiter.wait(ctx); err != nil {
				return nil, ctxError(err)
			}
			resp, again, err := c.once(ctx, base, method, req)
			if err == nil {
				return resp, nil
			}
			last = err
			if !again {
				return resp, err
			}
			if !retryable {
				// Неизвестно, дошёл ли запрос: повтор или другой адрес
				// могли бы отправить его дважды.
				return resp, err
			}
			if attempt < c.opts.MaxAttempts {
				if serr := c.opts.Sleep(ctx, c.delay(attempt, resp)); serr != nil {
					return nil, ctxError(serr)
				}
			}
		}
	}
	return nil, last
}

// once делает одну попытку. Третье значение сообщает, имеет ли смысл повтор.
func (c *HTTPClient) once(ctx context.Context, base, method string, req Request) (*Response, bool, error) {
	actx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()

	full := base + req.Path
	if len(req.Query) > 0 {
		full += "?" + req.Query.Encode()
	}
	hreq, err := http.NewRequestWithContext(actx, method, full, bytes.NewReader(req.Body))
	if err != nil {
		return nil, false, domain.NewError(domain.CodeInternal, fmt.Errorf("%w: %s", ErrBadRequest, c.scrub(unwrapURL(err).Error())))
	}
	for k, vs := range c.opts.Header {
		hreq.Header[k] = append([]string(nil), vs...)
	}
	for k, vs := range req.Header {
		hreq.Header[k] = append([]string(nil), vs...)
	}

	hresp, err := c.http.Do(hreq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctxError(ctx.Err())
		}
		// Таймаут одной попытки или сетевая ошибка: пробуем снова.
		return nil, true, domain.NewError(domain.CodeProviderUnavailable, c.safe(err))
	}
	defer func() { _ = hresp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(hresp.Body, c.opts.MaxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctxError(ctx.Err())
		}
		return nil, true, domain.NewError(domain.CodeProviderUnavailable, c.safe(err))
	}
	if int64(len(body)) > c.opts.MaxResponseBytes {
		return nil, false, domain.NewError(domain.CodeProviderBadResponse, ErrResponseTooLarge)
	}

	resp := &Response{Status: hresp.StatusCode, Header: hresp.Header, Body: body}
	switch {
	case resp.Status >= 200 && resp.Status < 300:
		return resp, false, nil
	case resp.Status == http.StatusTooManyRequests:
		return resp, true, domain.NewError(domain.CodeProviderRateLimited, &StatusError{resp.Status})
	case resp.Status >= 500:
		return resp, true, domain.NewError(domain.CodeProviderUnavailable, &StatusError{resp.Status})
	default:
		// 3xx (редиректы запрещены) и 4xx: повтор ничего не изменит.
		return resp, false, domain.NewError(domain.CodeProviderBadResponse, &StatusError{resp.Status})
	}
}

// delay считает паузу перед следующей попыткой: экспонента с джиттером,
// для 429 учитывается Retry-After в секундах, но не больше MaxDelay.
func (c *HTTPClient) delay(attempt int, resp *Response) time.Duration {
	d := c.opts.BaseDelay << (attempt - 1)
	if d <= 0 || d > c.opts.MaxDelay {
		d = c.opts.MaxDelay
	}
	// Джиттер: от половины до полной паузы.
	d = d/2 + time.Duration(c.opts.Rand()*float64(d/2))
	if resp != nil && resp.Status == http.StatusTooManyRequests {
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			if ra := time.Duration(s) * time.Second; ra > d {
				d = min(ra, c.opts.MaxDelay)
			}
		}
	}
	return d
}

func (c *HTTPClient) scrub(s string) string { return c.opts.Redactor.Scrub(s) }

// safe возвращает ошибку без адреса запроса и без ключей API. url.Error
// содержит полный адрес вместе с ключом, поэтому берётся только его причина.
func (c *HTTPClient) safe(err error) error {
	inner := unwrapURL(err)
	return &safeError{msg: c.scrub(inner.Error()), cause: inner}
}

func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// safeError хранит очищенный текст. Причина доступна для errors.Is, но её
// собственный текст наружу не попадает.
type safeError struct {
	msg   string
	cause error
}

func (e *safeError) Error() string        { return e.msg }
func (e *safeError) Is(target error) bool { return errors.Is(e.cause, target) }

// ctxError переводит завершение контекста в код ошибки.
func ctxError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.NewError(domain.CodeTimeout, err)
	}
	return domain.NewError(domain.CodeCanceled, err)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
