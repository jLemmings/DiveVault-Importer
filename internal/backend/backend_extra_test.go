package backend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func jsonServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}

func TestNewNormalizesBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com":                 "https://example.com",
		"  https://example.com///  ":          "https://example.com",
		"http://localhost:8080/base/":         "http://localhost:8080/base",
		"https://divevault.example.ch/api/v1": "https://divevault.example.ch/api/v1",
	} {
		c, err := New(in, "")
		if err != nil {
			t.Fatalf("New(%q): %v", in, err)
		}
		if c.BaseURL != want {
			t.Errorf("New(%q).BaseURL = %q, want %q", in, c.BaseURL, want)
		}
	}
}

func TestNewRejectsInvalidURLs(t *testing.T) {
	for _, u := range []string{
		"",
		"   ",
		"example.com",
		"//example.com",
		"ftp://example.com",
		"javascript:alert(1)",
		"data:text/html,hi",
		"file://server/share",
		"https://example.com#fragment",
		"https://example.com/?token=abc",
		"https://user@example.com",
		"http://[::1",
		"http://exa mple.com",
	} {
		if c, err := New(u, ""); err == nil {
			t.Errorf("New(%q) accepted, BaseURL=%q", u, c.BaseURL)
		}
	}
}

func TestNormalizeTokenTable(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"   ":              "",
		"abc":              "abc",
		"bearer abc":       "abc",
		"BEARER   abc  ":   "abc",
		`" Bearer abc "`:   "abc",
		`'abc'`:            "abc",
		`'abc"`:            `'abc"`,
		`"`:                `"`,
		"Bearer":           "Bearer",
		"Bearerabc":        "Bearerabc",
		"abc.def-ghi_jkl":  "abc.def-ghi_jkl",
		"\tBearer \tabc\n": "abc",
	} {
		if got := NormalizeToken(in); got != want {
			t.Errorf("NormalizeToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequestHeaders(t *testing.T) {
	var seen []http.Header
	s := jsonServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Clone())
		_, _ = w.Write([]byte(`{}`))
	})
	anon, _ := New(s.URL, "")
	if err := anon.Request(context.Background(), "GET", "/x", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	authed, _ := New(s.URL, "tok")
	if err := authed.Request(context.Background(), "POST", "/x", nil, map[string]any{"a": 1}, nil); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("requests=%d", len(seen))
	}
	if h := seen[0]; h.Get("Authorization") != "" || h.Get("Content-Type") != "" || h.Get("Accept") != "application/json" || h.Get("User-Agent") == "" {
		t.Errorf("anonymous GET headers: %v", h)
	}
	if h := seen[1]; h.Get("Authorization") != "Bearer tok" || h.Get("Content-Type") != "application/json" {
		t.Errorf("authenticated POST headers: %v", h)
	}
}

func TestRequestSendsJSONPayload(t *testing.T) {
	s := jsonServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["dive_uid"] != "Mares:Smart Air:abcd" || body["n"] != float64(3) {
			t.Errorf("body=%v", body)
		}
		_, _ = w.Write([]byte(`{"inserted":false}`))
	})
	c, _ := New(s.URL, "tok")
	inserted, err := c.Insert(context.Background(), map[string]any{"dive_uid": "Mares:Smart Air:abcd", "n": 3})
	if err != nil || inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}
}

func TestRequestUnmarshalablePayload(t *testing.T) {
	var hits atomic.Int32
	s := jsonServer(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
	c, _ := New(s.URL, "")
	if err := c.Request(context.Background(), "POST", "/x", nil, map[string]any{"ch": make(chan int)}, nil); err == nil {
		t.Fatal("expected marshal error")
	}
	if hits.Load() != 0 {
		t.Fatal("request sent despite marshal failure")
	}
}

func TestEmptySuccessBodyIsAccepted(t *testing.T) {
	s := jsonServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	c, _ := New(s.URL, "")
	var out map[string]any
	if err := c.Request(context.Background(), "GET", "/x", nil, nil, &out); err != nil {
		t.Fatal(err)
	}
	inserted, err := c.Insert(context.Background(), map[string]any{})
	if err != nil || inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}
}

func TestNonSuccessStatusesAreErrors(t *testing.T) {
	for _, status := range []int{300, 304, 400, 401, 403, 404, 409, 422, 429, 500, 502, 503} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("details"))
		}))
		c, _ := New(s.URL, "")
		err := c.SaveFingerprint(context.Background(), "Mares", "Smart Air", []byte{1})
		s.Close()
		if err == nil {
			t.Errorf("status %d accepted", status)
			continue
		}
		if !strings.Contains(err.Error(), "PUT /api/device-state") {
			t.Errorf("status %d error lacks context: %v", status, err)
		}
	}
}

func TestFingerprintEmptyAndMissing(t *testing.T) {
	for _, body := range []string{`{}`, `{"fingerprint_hex":""}`, `{"fingerprint_hex":null}`, ``} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		c, _ := New(s.URL, "")
		fp, err := c.Fingerprint(context.Background(), "Mares", "Smart Air")
		s.Close()
		if err != nil || len(fp) != 0 {
			t.Errorf("body %q: fp=%x err=%v", body, fp, err)
		}
	}
}

func TestSaveFingerprintPayload(t *testing.T) {
	s := jsonServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/api/device-state" || r.URL.RawQuery != "" {
			t.Errorf("%s %s", r.Method, r.URL)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 3 || body["vendor"] != "Suunto" || body["product"] != "D4" || body["fingerprint_hex"] != "00ff10" {
			t.Errorf("body=%v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	c, _ := New(s.URL, "tok")
	if err := c.SaveFingerprint(context.Background(), "Suunto", "D4", []byte{0x00, 0xff, 0x10}); err != nil {
		t.Fatal(err)
	}
}

func TestCountInvalidResponsesReturnNil(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{500, `{"total":3}`},
		{401, `{"total":3}`},
		{200, `{"total":-1}`},
		{200, `{}`},
		{200, `{"total":null}`},
		{200, `{"total":"3"}`},
		{200, `not json`},
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c, _ := New(s.URL, "")
		got := c.Count(context.Background())
		s.Close()
		if got != nil {
			t.Errorf("status=%d body=%q: Count=%d, want nil", tc.status, tc.body, *got)
		}
	}
	s := jsonServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("limit") != "1" || q.Get("offset") != "0" || q.Get("include_raw_data") != "false" {
			t.Errorf("query=%v", q)
		}
		_, _ = w.Write([]byte(`{"total":0}`))
	})
	c, _ := New(s.URL, "")
	if got := c.Count(context.Background()); got == nil || *got != 0 {
		t.Fatalf("zero total lost: %v", got)
	}
}

func TestStartLogin(t *testing.T) {
	s := jsonServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/cli-auth/request" {
			t.Errorf("%s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("login request carried credentials")
		}
		_, _ = w.Write([]byte(`{"code":"ABC-123","status":"pending"}`))
	})
	c, _ := New(s.URL, "")
	a, err := c.StartLogin(context.Background())
	if err != nil || a.Code != "ABC-123" || a.Status != "pending" {
		t.Fatalf("%+v %v", a, err)
	}

	empty := jsonServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"status":"pending"}`)) })
	c, _ = New(empty.URL, "")
	if _, err := c.StartLogin(context.Background()); err == nil {
		t.Fatal("empty approval code accepted")
	}

	failing := jsonServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	c, _ = New(failing.URL, "")
	if _, err := c.StartLogin(context.Background()); err == nil {
		t.Fatal("backend failure ignored")
	}
}

// WaitLogin polls every two seconds, so these subtests run in parallel.
func TestWaitLoginOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		responses []string
		status    int
		wantToken string
		wantErr   string
	}{
		{name: "approved", responses: []string{`{"status":"approved","token":"Bearer tok-1"}`}, wantToken: "tok-1"},
		{name: "pending then approved", responses: []string{`{"status":"pending"}`, `{"status":"approved","token":"tok-2"}`}, wantToken: "tok-2"},
		{name: "approved without token", responses: []string{`{"status":"approved","token":"  "}`}, wantErr: "no token"},
		{name: "denied", responses: []string{`{"status":"denied"}`}, wantErr: "denied"},
		{name: "expired", responses: []string{`{"status":"expired"}`}, wantErr: "expired"},
		{name: "rejected", responses: []string{`{"status":"rejected"}`}, wantErr: "rejected"},
		{name: "backend error", status: 500, wantErr: "500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			s := jsonServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/cli-auth/request" || r.URL.Query().Get("code") != "c/ode&x=1" {
					t.Errorf("%s %s", r.Method, r.URL)
				}
				n := int(calls.Add(1)) - 1
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					return
				}
				if n >= len(tc.responses) {
					n = len(tc.responses) - 1
				}
				_, _ = w.Write([]byte(tc.responses[n]))
			})
			c, _ := New(s.URL, "")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			token, err := c.WaitLogin(ctx, "c/ode&x=1")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v, want containing %q", err, tc.wantErr)
				}
				if token != "" {
					t.Fatalf("token %q returned with error", token)
				}
				return
			}
			if err != nil || token != tc.wantToken {
				t.Fatalf("token=%q err=%v", token, err)
			}
		})
	}
}

func TestRequestHonoursContextCancellation(t *testing.T) {
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer s.Close()
	defer close(release)
	c, _ := New(s.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Fingerprint(ctx, "Mares", "Smart Air")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation not honoured promptly")
	}
}

func TestResultDecodingIgnoresUnknownFields(t *testing.T) {
	s := jsonServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"inserted":true,"extra":{"nested":[1,2,3]}}`)
	})
	c, _ := New(s.URL, "")
	if ok, err := c.Insert(context.Background(), map[string]any{}); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
