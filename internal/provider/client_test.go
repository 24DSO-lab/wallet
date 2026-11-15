package provider_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-lab/wallet/internal/domain"
	"github.com/24DSO-lab/wallet/internal/logging"
	"github.com/24DSO-lab/wallet/internal/provider"
)

// sleeper записывает паузы вместо ожидания: тесты идут мгновенно.
type sleeper struct {
	mu     sync.Mutex
	sleeps []time.Duration
}

func (s *sleeper) Sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.sleeps = append(s.sleeps, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleeper) list() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.sleeps...)
}

func newClient(t *testing.T, s *sleeper, opts provider.Options) *provider.HTTPClient {
	t.Helper()
	opts.Sleep = s.Sleep
	opts.Rand = func() float64 { return 0 } // пауза ровно половина расчётной
	c, err := provider.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func code(err error) domain.ErrorCode { return domain.CodeOf(err) }

func TestSuccess(t *testing.T) {
	var gotPath, gotQuery, gotHeader, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotMethod = r.URL.Path, r.URL.RawQuery, r.Method
		gotHeader = r.Header.Get("X-Test")
		w.Header().Set("X-Reply", "yes")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := newClient(t, &sleeper{}, provider.Options{
		Endpoints: []string{srv.URL + "/api/"}, // хвостовой слеш не удваивается
		Header:    http.Header{"X-Test": {"static"}},
	})
	resp, err := c.Do(context.Background(), provider.Request{
		Path:  "/address/abc/utxo",
		Query: map[string][]string{"a": {"1"}, "b": {"x y"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 || string(resp.Body) != `{"ok":true}` || resp.Header.Get("X-Reply") != "yes" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if gotMethod != "GET" || gotPath != "/api/address/abc/utxo" || gotQuery != "a=1&b=x+y" || gotHeader != "static" {
		t.Errorf("server saw %s %s ?%s header=%q", gotMethod, gotPath, gotQuery, gotHeader)
	}
}

func TestRequestHeaderOverridesStatic(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get("X-Test") }))
	defer srv.Close()

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}, Header: http.Header{"X-Test": {"static"}}})
	if _, err := c.Do(context.Background(), provider.Request{Header: http.Header{"X-Test": {"override"}}}); err != nil {
		t.Fatal(err)
	}
	if got != "override" {
		t.Errorf("header = %q", got)
	}
}

func TestRetryGETWithBackoff(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "fine")
	}))
	defer srv.Close()

	s := &sleeper{}
	c := newClient(t, s, provider.Options{Endpoints: []string{srv.URL}, BaseDelay: 400 * time.Millisecond, MaxDelay: 10 * time.Second})
	resp, err := c.Do(context.Background(), provider.Request{})
	if err != nil || string(resp.Body) != "fine" {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
	// Rand=0: пауза равна половине расчётной, расчётная удваивается.
	want := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond}
	if got := s.list(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("sleeps = %v, want %v", got, want)
	}
}

func TestBackoffIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()

	s := &sleeper{}
	c := newClient(t, s, provider.Options{Endpoints: []string{srv.URL}, MaxAttempts: 6, BaseDelay: time.Second, MaxDelay: 2 * time.Second})
	_, _ = c.Do(context.Background(), provider.Request{})
	for _, d := range s.list() {
		if d > 2*time.Second {
			t.Errorf("pause %v exceeds MaxDelay", d)
		}
	}
}

func TestJitterStaysInRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()

	for _, r := range []float64{0, 0.5, 0.999} {
		s := &sleeper{}
		c, err := provider.New(provider.Options{Endpoints: []string{srv.URL}, MaxAttempts: 2, BaseDelay: time.Second, Sleep: s.Sleep, Rand: func() float64 { return r }})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = c.Do(context.Background(), provider.Request{})
		d := s.list()[0]
		if d < 500*time.Millisecond || d > time.Second {
			t.Errorf("rand=%v: pause %v outside [0.5s, 1s]", r, d)
		}
	}
}

func TestPOSTIsNotRetriedAndHasNoFallback(t *testing.T) {
	var primary, backup atomic.Int32
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { primary.Add(1); w.WriteHeader(503) }))
	defer p.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backup.Add(1) }))
	defer b.Close()

	s := &sleeper{}
	c := newClient(t, s, provider.Options{Endpoints: []string{p.URL, b.URL}})
	_, err := c.Do(context.Background(), provider.Request{Method: "POST", Body: []byte("rawtx")})
	if code(err) != domain.CodeProviderUnavailable {
		t.Fatalf("code = %q (%v)", code(err), err)
	}
	if primary.Load() != 1 || backup.Load() != 0 || len(s.list()) != 0 {
		t.Errorf("primary=%d backup=%d sleeps=%v: a send must not be repeated or rerouted", primary.Load(), backup.Load(), s.list())
	}
}

func TestIdempotentPOSTIsRetriedWithSameBody(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.WriteHeader(502)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}})
	if _, err := c.Do(context.Background(), provider.Request{Method: "POST", Body: []byte(`{"method":"eth_blockNumber"}`), Idempotent: true}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || bodies[0] != `{"method":"eth_blockNumber"}` {
		t.Errorf("bodies = %q", bodies)
	}
}

func TestFallbackAfterPrimaryExhausted(t *testing.T) {
	var primary, backup atomic.Int32
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { primary.Add(1); w.WriteHeader(500) }))
	defer p.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backup.Add(1); _, _ = io.WriteString(w, "from backup") }))
	defer b.Close()

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{p.URL, b.URL}, MaxAttempts: 2})
	resp, err := c.Do(context.Background(), provider.Request{Path: "/x"})
	if err != nil || string(resp.Body) != "from backup" {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	if primary.Load() != 2 || backup.Load() != 1 {
		t.Errorf("primary=%d backup=%d, want 2 and 1", primary.Load(), backup.Load())
	}
}

func TestClientErrorIsNotRetriedAndBodyIsReturned(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad-txns-inputs-missingorspent"}`)
	}))
	defer srv.Close()

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL, srv.URL}})
	resp, err := c.Do(context.Background(), provider.Request{})
	if code(err) != domain.CodeProviderBadResponse {
		t.Fatalf("code = %q", code(err))
	}
	var se *provider.StatusError
	if !errors.As(err, &se) || se.Status != 400 {
		t.Errorf("expected StatusError 400, got %v", err)
	}
	if resp == nil || !strings.Contains(string(resp.Body), "missingorspent") {
		t.Errorf("response body must be available to the adapter: %+v", resp)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, a 4xx must not be retried", calls.Load())
	}
}

func TestRateLimitedHonorsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(429)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	s := &sleeper{}
	c := newClient(t, s, provider.Options{Endpoints: []string{srv.URL}, BaseDelay: 100 * time.Millisecond, MaxDelay: 5 * time.Second})
	if _, err := c.Do(context.Background(), provider.Request{}); err != nil {
		t.Fatal(err)
	}
	if got := s.list(); len(got) != 1 || got[0] != 3*time.Second {
		t.Errorf("sleeps = %v, want [3s]", got)
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(429)
			return
		}
	}))
	defer srv.Close()

	s := &sleeper{}
	c := newClient(t, s, provider.Options{Endpoints: []string{srv.URL}, MaxDelay: 2 * time.Second})
	if _, err := c.Do(context.Background(), provider.Request{}); err != nil {
		t.Fatal(err)
	}
	if got := s.list(); len(got) != 1 || got[0] != 2*time.Second {
		t.Errorf("sleeps = %v, want [2s]", got)
	}
}

func TestRateLimitedCodeWhenExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer srv.Close()

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}})
	if _, err := c.Do(context.Background(), provider.Request{}); code(err) != domain.CodeProviderRateLimited {
		t.Errorf("code = %q", code(err))
	}
}

func TestResponseSizeLimit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, strings.Repeat("x", 101))
	}))
	defer srv.Close()

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}, MaxResponseBytes: 100})
	_, err := c.Do(context.Background(), provider.Request{})
	if !errors.Is(err, provider.ErrResponseTooLarge) || code(err) != domain.CodeProviderBadResponse {
		t.Fatalf("err = %v (code %q)", err, code(err))
	}
	if calls.Load() != 1 {
		t.Errorf("an oversized response must not be retried, calls=%d", calls.Load())
	}

	// Ровно по лимиту проходит.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 100)) }))
	defer srv2.Close()
	c2 := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv2.URL}, MaxResponseBytes: 100})
	if resp, err := c2.Do(context.Background(), provider.Request{}); err != nil || len(resp.Body) != 100 {
		t.Errorf("exact-limit body: %v, %v", resp, err)
	}
}

func TestAttemptTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}, Timeout: 50 * time.Millisecond, MaxAttempts: 2})
	start := time.Now()
	_, err := c.Do(context.Background(), provider.Request{})
	if code(err) != domain.CodeProviderUnavailable {
		t.Errorf("code = %q (%v)", code(err), err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("timeout did not apply per attempt")
	}
}

func TestContextCanceled(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}})
	_, err := c.Do(ctx, provider.Request{})
	if code(err) != domain.CodeCanceled || !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v (code %q)", err, code(err))
	}
	if calls.Load() != 0 {
		t.Errorf("a canceled context must not reach the server, calls=%d", calls.Load())
	}
}

func TestContextDeadlineCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}, Timeout: time.Minute})
	_, err := c.Do(ctx, provider.Request{})
	if code(err) != domain.CodeTimeout {
		t.Errorf("code = %q (%v)", code(err), err)
	}
}

func TestErrorsDoNotLeakKeysOrURLs(t *testing.T) {
	const key = "k_live_9c1d5e7f2b"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL + "/v2/" + key
	srv.Close() // соединение будет отклонено, а url.Error содержит полный адрес

	c := newClient(t, &sleeper{}, provider.Options{
		Endpoints: []string{base},
		Redactor:  logging.NewRedactor(key),
	})
	_, err := c.Do(context.Background(), provider.Request{Path: "/call", Query: map[string][]string{"apikey": {key}}})
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "/v2/") {
		t.Errorf("error leaks the key or URL: %v", err)
	}
	for u := errors.Unwrap(err); u != nil; u = errors.Unwrap(u) {
		if strings.Contains(u.Error(), key) {
			t.Errorf("wrapped error leaks the key: %v", u)
		}
	}
	if code(err) != domain.CodeProviderUnavailable {
		t.Errorf("code = %q", code(err))
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var evil atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { evil.Add(1) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}, Header: http.Header{"X-Api-Key": {"secret-key-123"}}})
	resp, err := c.Do(context.Background(), provider.Request{})
	if code(err) != domain.CodeProviderBadResponse || resp == nil || resp.Status != 302 {
		t.Errorf("resp=%v err=%v", resp, err)
	}
	if evil.Load() != 0 {
		t.Error("the redirect target was contacted")
	}
}

func TestBadPathRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("must not reach the server") }))
	defer srv.Close()

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}})
	// Путь без ведущего слеша мог бы дописаться к хосту ("host" + "@evil").
	for _, p := range []string{"x", "@evil.example/x", ".evil.example"} {
		if _, err := c.Do(context.Background(), provider.Request{Path: p}); !errors.Is(err, provider.ErrBadRequest) {
			t.Errorf("path %q: err = %v", p, err)
		}
	}
}

func TestNewValidatesEndpoints(t *testing.T) {
	bad := [][]string{
		nil,
		{""},
		{"not a url"},
		{"/relative"},
		{"http://example.com"},
		{"ftp://example.com"},
		{"https://user:pass@example.com"},
		{"https://"},
		{"https://ok.example", "http://remote.example"},
	}
	for _, eps := range bad {
		if _, err := provider.New(provider.Options{Endpoints: eps}); err == nil {
			t.Errorf("endpoints %q must be rejected", eps)
		}
	}
	good := [][]string{
		{"https://mempool.space/api"},
		{"https://a.example", "https://b.example/v2/key"},
		{"http://127.0.0.1:8080"},
		{"http://localhost:8080"},
		{"http://[::1]:8080"},
	}
	for _, eps := range good {
		if _, err := provider.New(provider.Options{Endpoints: eps}); err != nil {
			t.Errorf("endpoints %q: %v", eps, err)
		}
	}
}

func TestMinIntervalSpacesRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	s := &sleeper{}
	c := newClient(t, s, provider.Options{Endpoints: []string{srv.URL}, MinInterval: time.Hour})
	for i := 0; i < 3; i++ {
		if _, err := c.Do(context.Background(), provider.Request{}); err != nil {
			t.Fatal(err)
		}
	}
	// Первый запрос идёт сразу, остальные ждут своего слота.
	if got := s.list(); len(got) != 2 {
		t.Errorf("sleeps = %v, want two waits", got)
	}
}

func TestConcurrentUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer srv.Close()

	c := newClient(t, &sleeper{}, provider.Options{Endpoints: []string{srv.URL}, MinInterval: time.Nanosecond})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Do(context.Background(), provider.Request{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func FuzzNewEndpoint(f *testing.F) {
	for _, s := range []string{"https://a.example", "http://127.0.0.1", "http://evil.example", "", "https://u:p@a.example", "ftp://x", "https://[::1]"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		c, err := provider.New(provider.Options{Endpoints: []string{raw}})
		if err != nil {
			return
		}
		// Принятый адрес: https или http на loopback, без учётных данных.
		_ = c
		u, perr := url.Parse(raw)
		if perr != nil || u.Host == "" || u.User != nil {
			t.Fatalf("accepted malformed endpoint %q", raw)
		}
		if u.Scheme != "https" {
			ip := net.ParseIP(u.Hostname())
			if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
				t.Fatalf("accepted insecure endpoint %q", raw)
			}
		}
	})
}
