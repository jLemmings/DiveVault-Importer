package main

// These tests replace http.DefaultTransport so the hard-coded download URL is
// served from memory. No network access is needed, and the archive contents can
// be crafted to exercise the extractor's path-traversal defences.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type entry struct {
	name     string
	typeflag byte
	body     string
	mode     int64
	link     string
}

func tarball(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: mode, Size: int64(len(e.body)), Linkname: e.link, Format: tar.FormatPAX}
		if e.typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// serve makes the next download return body with the given status and runs in a
// fresh working directory. It returns a pointer to the request counter.
func serve(t *testing.T, status int, body []byte) *int {
	t.Helper()
	t.Chdir(t.TempDir())
	calls := 0
	prev := http.DefaultTransport
	http.DefaultTransport = roundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://libdivecomputer.org/releases/libdivecomputer-0.9.0.tar.gz" {
			t.Errorf("unexpected download URL %s", r.URL)
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = prev })
	return &calls
}

func validArchive(t *testing.T, extra ...entry) []byte {
	return tarball(t, append([]entry{
		{name: "libdivecomputer-0.9.0/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "libdivecomputer-0.9.0/include/libdivecomputer/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "libdivecomputer-0.9.0/include/libdivecomputer/parser.h", typeflag: tar.TypeReg, body: "/* parser */"},
		{name: "libdivecomputer-0.9.0/configure", typeflag: tar.TypeReg, body: "#!/bin/sh", mode: 0o755},
	}, extra...)...)
}

func assertNoVendorTree(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("vendor/libdivecomputer-0.9.0"); !os.IsNotExist(err) {
		t.Fatal("partial source tree published after a failed extraction")
	}
	if entries, _ := filepath.Glob("vendor/.download-*"); len(entries) != 0 {
		t.Fatalf("staging directories left behind: %v", entries)
	}
}

func TestFetchExtractsArchive(t *testing.T) {
	calls := serve(t, http.StatusOK, validArchive(t))
	if err := fetch(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("vendor/libdivecomputer-0.9.0/include/libdivecomputer/parser.h")
	if err != nil || string(b) != "/* parser */" {
		t.Fatalf("header=%q err=%v", b, err)
	}
	if entries, _ := filepath.Glob("vendor/.download-*"); len(entries) != 0 {
		t.Fatalf("staging left behind: %v", entries)
	}
	if *calls != 1 {
		t.Fatalf("downloads=%d", *calls)
	}
	// Second run must be a no-op and never hit the network.
	if err := fetch(); err != nil || *calls != 1 {
		t.Fatalf("re-fetch err=%v downloads=%d", err, *calls)
	}
}

func TestFetchHTTPFailure(t *testing.T) {
	serve(t, http.StatusNotFound, []byte("nope"))
	if err := fetch(); err == nil || !strings.Contains(err.Error(), "download") {
		t.Fatalf("err=%v", err)
	}
	assertNoVendorTree(t)
}

func TestFetchRejectsNonGzip(t *testing.T) {
	serve(t, http.StatusOK, []byte("<html>captive portal</html>"))
	if err := fetch(); err == nil {
		t.Fatal("non-gzip body accepted")
	}
	assertNoVendorTree(t)
}

func TestFetchRejectsTruncatedArchive(t *testing.T) {
	full := validArchive(t)
	serve(t, http.StatusOK, full[:len(full)/2])
	if err := fetch(); err == nil {
		t.Fatal("truncated archive accepted")
	}
	assertNoVendorTree(t)
}

func TestSecurityFetchRejectsMaliciousEntries(t *testing.T) {
	cases := map[string]entry{
		"parent traversal":      {name: "../evil.txt", typeflag: tar.TypeReg, body: "pwned"},
		"nested traversal":      {name: "libdivecomputer-0.9.0/../../evil.txt", typeflag: tar.TypeReg, body: "pwned"},
		"absolute path":         {name: "/tmp/evil.txt", typeflag: tar.TypeReg, body: "pwned"},
		"windows drive path":    {name: "C:/evil.txt", typeflag: tar.TypeReg, body: "pwned"},
		"outside top directory": {name: "other/evil.txt", typeflag: tar.TypeReg, body: "pwned"},
		"prefix lookalike":      {name: "libdivecomputer-0.9.0-evil/x", typeflag: tar.TypeReg, body: "pwned"},
		"symlink":               {name: "libdivecomputer-0.9.0/link", typeflag: tar.TypeSymlink, link: "/etc/passwd"},
		"hardlink":              {name: "libdivecomputer-0.9.0/hard", typeflag: tar.TypeLink, link: "../../evil"},
		"device node":           {name: "libdivecomputer-0.9.0/dev", typeflag: tar.TypeChar},
		"fifo":                  {name: "libdivecomputer-0.9.0/fifo", typeflag: tar.TypeFifo},
		"duplicate file":        {name: "libdivecomputer-0.9.0/configure", typeflag: tar.TypeReg, body: "overwrite"},
		"backslash traversal":   {name: `libdivecomputer-0.9.0\..\..\evil.txt`, typeflag: tar.TypeReg, body: "pwned"},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			if name == "backslash traversal" && runtime.GOOS != "windows" {
				// On POSIX a backslash is an ordinary filename character, so the
				// entry is a harmless file inside the tree.
				t.Skip("backslash is not a separator on this OS")
			}
			serve(t, http.StatusOK, validArchive(t, bad))
			if err := fetch(); err == nil {
				t.Fatalf("malicious entry %q accepted", bad.name)
			}
			assertNoVendorTree(t)
			for _, p := range []string{"evil.txt", "../evil.txt", "../../evil.txt"} {
				if _, err := os.Stat(p); err == nil {
					t.Fatalf("file written outside staging: %s", p)
				}
			}
		})
	}
}

func TestSecurityFetchStripsSpecialModeBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not meaningful on Windows")
	}
	serve(t, http.StatusOK, validArchive(t, entry{name: "libdivecomputer-0.9.0/suid", typeflag: tar.TypeReg, body: "x", mode: 0o6777}))
	if err := fetch(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat("vendor/libdivecomputer-0.9.0/suid")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		t.Fatalf("special mode bits preserved: %v", fi.Mode())
	}
}
