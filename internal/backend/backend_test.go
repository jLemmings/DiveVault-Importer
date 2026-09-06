package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBackendContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/device-state":
			if r.URL.Query().Get("product") != "Smart Air" || r.URL.Query().Get("vendor") != "Mares" {
				t.Error(r.URL)
			}
			_, _ = w.Write([]byte(`{"fingerprint_hex":"aabb"}`))
		case "PUT /api/device-state":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["fingerprint_hex"] != "ccdd" {
				t.Error(body)
			}
			w.WriteHeader(204)
		case "POST /api/dives":
			_, _ = w.Write([]byte(`{"inserted":true}`))
		case "GET /api/dives":
			if r.URL.Query().Get("include_samples") != "false" {
				t.Error(r.URL)
			}
			_, _ = w.Write([]byte(`{"total":23}`))
		default:
			t.Error(r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := New(server.URL, `"Bearer secret"`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fp, err := c.Fingerprint(ctx, "Mares", "Smart Air")
	if err != nil || len(fp) != 2 || fp[0] != 0xaa {
		t.Fatalf("%x %v", fp, err)
	}
	if err := c.SaveFingerprint(ctx, "Mares", "Smart Air", []byte{0xcc, 0xdd}); err != nil {
		t.Fatal(err)
	}
	if inserted, err := c.Insert(ctx, map[string]any{"dive_uid": "id"}); !inserted || err != nil {
		t.Fatal(err)
	}
	if count := c.Count(ctx); count == nil || *count != 23 {
		t.Fatal(count)
	}
	if requests != 4 {
		t.Fatal(requests)
	}
}
func TestErrorsAndInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{{"unauthorized", `denied`, 401}, {"malformed JSON", `oops`, 200}, {"invalid hex", `{"fingerprint_hex":"xyz"}`, 200}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c, _ := New(s.URL, "")
			if _, err := c.Fingerprint(context.Background(), "Mares", "Smart Air"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
func TestURLAndToken(t *testing.T) {
	for _, u := range []string{"file:///tmp/a", "https://", "http://user:pass@example.com", "https://example.com?x=1"} {
		if _, err := New(u, ""); err == nil {
			t.Fatal(u)
		}
	}
	for _, s := range []string{" secret ", "Bearer secret", "'Bearer secret'", `"secret"`} {
		if NormalizeToken(s) != "secret" {
			t.Fatal(s)
		}
	}
	c, _ := New("https://example.com/", "")
	if c.ApprovalURL("a/b") != "https://example.com/#settings/cli-auth/a%2Fb" {
		t.Fatal(c.ApprovalURL("a/b"))
	}
}
func TestLoginCancellation(t *testing.T) {
	c, _ := New("https://example.com", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := c.WaitLogin(ctx, "code"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestRedirectDoesNotForwardToken(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected redirected request") }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer s.Close()
	c, _ := New(s.URL, "secret")
	if _, err := c.Fingerprint(context.Background(), "a", "b"); err == nil {
		t.Fatal("redirect accepted")
	}
}
