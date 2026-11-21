package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCopyFileCopiesContentAndCreatesParents(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("payload"), 0o750); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "a", "b", "dest.bin")
	if err := copyFile(src, dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != "payload" {
		t.Fatalf("%q %v", b, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(dest)
		if fi.Mode().Perm() != 0o750 {
			t.Fatalf("mode=%o", fi.Mode().Perm())
		}
	}
	// Overwrite truncates the previous content.
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "x" {
		t.Fatalf("not truncated: %q", b)
	}
	if err := copyFile(filepath.Join(dir, "missing"), dest); err == nil {
		t.Fatal("missing source accepted")
	}
}

func TestZipTreeLayoutIsRelativeAndSafe(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "DiveSync")
	files := map[string]string{"DiveSync": "bin", "lib/libdivecomputer.so": "lib", "licenses/x/LICENSE": "mit"}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(dir, "out.zip")
	if err := zipTree(root, dest); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	got := map[string]string{}
	for _, f := range z.File {
		if strings.Contains(f.Name, "\\") || strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "..") || !filepath.IsLocal(filepath.FromSlash(f.Name)) {
			t.Fatalf("unsafe archive entry name %q", f.Name)
		}
		if !strings.HasPrefix(f.Name, "DiveSync/") {
			t.Fatalf("entry %q not under bundle directory", f.Name)
		}
		if f.Method != zip.Deflate {
			t.Errorf("%s not deflated", f.Name)
		}
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[strings.TrimPrefix(f.Name, "DiveSync/")] = string(b)
	}
	if len(got) != len(files) {
		t.Fatalf("entries=%v", got)
	}
	for name, body := range files {
		if got[name] != body {
			t.Errorf("%s=%q want %q", name, got[name], body)
		}
	}
}

func TestZipTreeMissingRoot(t *testing.T) {
	dir := t.TempDir()
	if err := zipTree(filepath.Join(dir, "missing"), filepath.Join(dir, "out.zip")); err == nil {
		t.Fatal("missing root accepted")
	}
}

func TestWindowsDependencySearchDirs(t *testing.T) {
	t.Setenv("PATH", strings.Join([]string{"one", "", "two", "one"}, string(os.PathListSeparator)))
	dirs := windowsDependencySearchDirs("runtime")
	if len(dirs) == 0 || dirs[0] != "runtime" {
		t.Fatalf("runtime directory must be searched first: %v", dirs)
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		if d == "" {
			t.Fatal("empty search dir")
		}
		if seen[d] {
			t.Fatalf("duplicate search dir %q in %v", d, dirs)
		}
		seen[d] = true
	}
	for _, want := range []string{"one", "two", `C:\msys64\mingw64\bin`} {
		if !seen[want] {
			t.Errorf("missing %q in %v", want, dirs)
		}
	}
}

func TestWindowsDependencyAlternatives(t *testing.T) {
	if alts := windowsDependencyAlternatives("LIBHIDAPI-0.DLL"); len(alts) == 0 {
		t.Fatal("hidapi alternatives missing")
	}
	if alts := windowsDependencyAlternatives("libusb-1.0.dll"); alts != nil {
		t.Fatalf("unexpected alternatives %v", alts)
	}
}
