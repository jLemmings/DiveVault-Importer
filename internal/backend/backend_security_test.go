package backend

// Security-focused tests for the backend client: credential handling, request
// smuggling via user-controlled values, TLS verification, redirect handling and
// bounded error output.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSecurityTLSCertificatesAreVerified(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer s.Close()
	c, err := New(s.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fingerprint(context.Background(), "Mares", "Smart Air"); err == nil {
		t.Fatal("self-signed certificate accepted; TLS verification must stay enabled")
	}
	if hits.Load() != 0 {
		t.Fatal("request (and bearer token) reached an untrusted TLS server")
	}
}

func TestSecurityRedirectsAreNeverFollowed(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		var leaked atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/api/dives", status)
		}))
		c, _ := New(origin.URL, "secret")
		ctx := context.Background()
		if err := c.SaveFingerprint(ctx, "a", "b", []byte{1}); err == nil {
			t.Errorf("%d: PUT redirect treated as success", status)
		}
		if _, err := c.Insert(ctx, map[string]any{"x": 1}); err == nil {
			t.Errorf("%d: POST redirect treated as success", status)
		}
		if _, err := c.StartLogin(ctx); err == nil {
			t.Errorf("%d: login redirect treated as success", status)
		}
		if c.Count(ctx) != nil {
			t.Errorf("%d: count followed redirect", status)
		}
		origin.Close()
		target.Close()
		if leaked.Load() != 0 {
			t.Errorf("%d: redirect followed to foreign host", status)
		}
	}
}

func TestSecurityHeaderInjectionViaTokenIsRejected(t *testing.T) {
	var received atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.Header.Get("X-Injected") != "" {
			t.Error("header injected through token")
		}
	}))
	defer s.Close()
	for _, token := range []string{"secret\r\nX-Injected: 1", "secret\nX-Injected: 1", "secret\rX-Injected: 1", "sec\x00ret"} {
		c, _ := New(s.URL, token)
		if _, err := c.Fingerprint(context.Background(), "Mares", "Smart Air"); err == nil {
			t.Errorf("token %q was sent", token)
		}
	}
	if received.Load() != 0 {
		t.Fatalf("%d requests with malformed Authorization reached the server", received.Load())
	}
}

func TestSecurityQueryValuesCannotInjectParameters(t *testing.T) {
	vendor := "Mares&product=evil&admin=true"
	product := "Smart Air#/../../admin?x=1"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/device-state" {
			t.Errorf("path changed to %q", r.URL.Path)
		}
		q := r.URL.Query()
		if len(q) != 2 || q.Get("vendor") != vendor || q.Get("product") != product || q.Has("admin") {
			t.Errorf("query manipulated: %v", q)
		}
		_, _ = w.Write([]byte(`{"fingerprint_hex":""}`))
	}))
	defer s.Close()
	c, _ := New(s.URL, "")
	if _, err := c.Fingerprint(context.Background(), vendor, product); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityWaitLoginCodeCannotInjectParameters(t *testing.T) {
	code := "abc&code=other#frag"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query()["code"]; len(got) != 1 || got[0] != code {
			t.Errorf("code parameter manipulated: %v", got)
		}
		_, _ = w.Write([]byte(`{"status":"denied"}`))
	}))
	defer s.Close()
	c, _ := New(s.URL, "")
	if _, err := c.WaitLogin(context.Background(), code); err == nil {
		t.Fatal("denied login accepted")
	}
}

func TestSecurityApprovalURLStaysOnBackend(t *testing.T) {
	c, _ := New("https://vault.example.com", "")
	prefix := "https://vault.example.com/#settings/cli-auth/"
	for _, code := range []string{"../../evil", "x?redirect=https://evil.example", "x#y", "//evil.example", "a b", "%2e%2e", "javascript:alert(1)"} {
		got := c.ApprovalURL(code)
		if !strings.HasPrefix(got, prefix) {
			t.Fatalf("ApprovalURL(%q) = %q", code, got)
		}
		suffix := strings.TrimPrefix(got, prefix)
		if strings.ContainsAny(suffix, "/?# ") {
			t.Errorf("ApprovalURL(%q) has unescaped delimiters: %q", code, suffix)
		}
		u, err := url.Parse(got)
		if err != nil || u.Host != "vault.example.com" || u.Scheme != "https" {
			t.Errorf("ApprovalURL(%q) points elsewhere: %q", code, got)
		}
		if decoded, _ := url.PathUnescape(suffix); decoded != code {
			t.Errorf("ApprovalURL(%q) lost information: %q", code, decoded)
		}
	}
}

func TestSecurityErrorOutputIsBounded(t *testing.T) {
	huge := strings.Repeat("A", 1<<20)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(huge))
	}))
	defer s.Close()
	c, _ := New(s.URL, "")
	_, err := c.Fingerprint(context.Background(), "Mares", "Smart Air")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(err.Error()) > 4096+256 {
		t.Fatalf("error message not bounded: %d bytes", len(err.Error()))
	}
}

func TestSecurityErrorsDoNotLeakToken(t *testing.T) {
	const token = "super-secret-token-value"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"detail":"invalid credentials"}`))
	}))
	c, _ := New(s.URL, token)
	ctx := context.Background()
	errs := []error{}
	_, err := c.Fingerprint(ctx, "Mares", "Smart Air")
	errs = append(errs, err)
	errs = append(errs, c.SaveFingerprint(ctx, "Mares", "Smart Air", []byte{1}))
	_, err = c.Insert(ctx, map[string]any{})
	errs = append(errs, err)
	s.Close()
	// Connection failures must not echo the token either.
	_, err = c.Fingerprint(ctx, "Mares", "Smart Air")
	errs = append(errs, err)
	for i, err := range errs {
		if err == nil {
			t.Fatalf("call %d: expected error", i)
		}
		if strings.Contains(err.Error(), token) {
			t.Fatalf("call %d: error leaks bearer token: %v", i, err)
		}
	}
}

func TestSecurityClientHardening(t *testing.T) {
	c, _ := New("https://example.com", "")
	if c.HTTP.Timeout <= 0 {
		t.Fatal("HTTP client has no timeout; a stalled backend would hang the importer")
	}
	if c.HTTP.CheckRedirect == nil {
		t.Fatal("redirect policy missing")
	}
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	if err := c.HTTP.CheckRedirect(req, nil); err != http.ErrUseLastResponse {
		t.Fatalf("redirect policy = %v", err)
	}
}

func FuzzNormalizeToken(f *testing.F) {
	for _, s := range []string{"", "abc", "Bearer abc", `"Bearer abc"`, "'x'", `"`, "bearer ", " \t\n", "Bearer Bearer x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := NormalizeToken(in)
		if strings.TrimSpace(out) != out {
			t.Fatalf("NormalizeToken(%q) = %q is not trimmed", in, out)
		}
		if len(out) > len(in) {
			t.Fatalf("NormalizeToken(%q) grew to %q", in, out)
		}
		if !strings.Contains(in, out) {
			t.Fatalf("NormalizeToken(%q) = %q is not a substring of the input", in, out)
		}
	})
}

func FuzzNew(f *testing.F) {
	for _, s := range []string{"https://example.com", "http://localhost:8080/a/", "file:///etc/passwd", "https://u:p@h", "https://h?q=1", "https://h#f", "javascript:x", "", "http://[::1]:80"} {
		f.Add(s, "token")
	}
	f.Fuzz(func(t *testing.T, base, token string) {
		c, err := New(base, token)
		if err != nil {
			if c != nil {
				t.Fatal("non-nil client returned with error")
			}
			return
		}
		u, err := url.Parse(c.BaseURL)
		if err != nil {
			t.Fatalf("BaseURL %q does not parse: %v", c.BaseURL, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Fatalf("scheme %q accepted from %q", u.Scheme, base)
		}
		if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			t.Fatalf("unsafe BaseURL %q accepted from %q", c.BaseURL, base)
		}
		// Security invariant: every API endpoint built from BaseURL must stay on
		// the validated scheme and host. (Inputs such as "https://h?" or
		// "https://h/#" currently yield malformed paths, which is a known
		// functional issue, but they must never redirect requests elsewhere.)
		endpoint, err := url.Parse(c.BaseURL + "/api/dives")
		if err != nil || endpoint.Scheme != u.Scheme || endpoint.Host != u.Host || endpoint.User != nil {
			t.Fatalf("endpoint from %q escapes backend host: %v %v", base, endpoint, err)
		}
		if c.Token != NormalizeToken(token) {
			t.Fatalf("token not normalized")
		}
	})
}
