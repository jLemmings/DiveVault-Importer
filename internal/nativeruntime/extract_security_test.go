package nativeruntime

// Security tests: the runtime cache lives in a user-writable directory and is
// loaded by the Windows system loader, so tampering, symlink planting and
// malformed bundles must all be rejected.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

func TestReturnedPathIsAbsoluteInsideRoot(t *testing.T) {
	root := t.TempDir()
	path, err := Extract(fixture(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) || filepath.Base(path) != "libdivecomputer-0.dll" {
		t.Fatalf("path=%q", path)
	}
	absRoot, _ := filepath.Abs(root)
	rel, err := filepath.Rel(absRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Count(filepath.ToSlash(rel), "/") != 1 {
		t.Fatalf("library %q escapes cache root %q", path, absRoot)
	}
}

func TestAlternativeLibraryNameAndCaseInsensitiveSuffix(t *testing.T) {
	bundle := fstest.MapFS{"libdivecomputer.dll": {Data: []byte("lib")}, "LIBUSB-1.0.DLL": {Data: []byte("dep")}}
	path, err := Extract(bundle, t.TempDir())
	if err != nil || filepath.Base(path) != "libdivecomputer.dll" {
		t.Fatalf("%q %v", path, err)
	}
}

func TestSecurityRejectsMalformedBundles(t *testing.T) {
	for name, bundle := range map[string]fstest.MapFS{
		"non-dll file":      {"libdivecomputer-0.dll": {Data: []byte("x")}, "payload.exe": {Data: []byte("x")}},
		"script file":       {"libdivecomputer-0.dll": {Data: []byte("x")}, "run.bat": {Data: []byte("x")}},
		"nested directory":  {"libdivecomputer-0.dll": {Data: []byte("x")}, "sub/evil.dll": {Data: []byte("x")}},
		"two libraries":     {"libdivecomputer-0.dll": {Data: []byte("x")}, "libdivecomputer.dll": {Data: []byte("y")}},
		"empty dependency":  {"libdivecomputer-0.dll": {Data: []byte("x")}, "libusb.dll": {Data: []byte{}}},
		"dll-named dir":     {"libdivecomputer-0.dll": {Data: []byte("x")}, "trick.dll/inner": {Data: []byte("x")}},
		"lookalike library": {"libdivecomputer-1.dll": {Data: []byte("x")}},
		"only suffix 'dll'": {"libdivecomputer-0dll": {Data: []byte("x")}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := Extract(bundle, root); err == nil {
				t.Fatal("malformed bundle accepted")
			}
			if entries, _ := os.ReadDir(root); len(entries) != 0 {
				t.Fatalf("files written for rejected bundle: %v", entries)
			}
		})
	}
}

func TestSecurityMissingCachedDependencyRejected(t *testing.T) {
	root := t.TempDir()
	path, err := Extract(fixture(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(path), "libusb.dll")); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(fixture(), root); err == nil {
		t.Fatal("cache with missing dependency accepted")
	}
}

func TestSecurityTamperedDependencyRejected(t *testing.T) {
	root := t.TempDir()
	path, err := Extract(fixture(), root)
	if err != nil {
		t.Fatal(err)
	}
	// Same length, different content: a size-only check would miss this.
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "libusb.dll"), []byte("DEPENDENCY"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Extract(fixture(), root)
	if err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("tampered dependency accepted: %v", err)
	}
}

func TestSecurityCacheTargetAsFileRejected(t *testing.T) {
	root := t.TempDir()
	path, err := Extract(fixture(), root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("planted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(fixture(), root); err == nil {
		t.Fatal("regular file accepted as cache directory")
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable (needs Developer Mode or admin): %v", err)
		}
		t.Fatal(err)
	}
}

func TestSecuritySymlinkedCacheDirectoryRejected(t *testing.T) {
	root := t.TempDir()
	path, err := Extract(fixture(), root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	// An attacker prepares a directory with byte-identical files elsewhere and
	// swaps the cache entry for a symlink pointing at it.
	attacker := t.TempDir()
	for name, file := range fixture() {
		if err := os.WriteFile(filepath.Join(attacker, name), file.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, attacker, dir)
	if _, err := Extract(fixture(), root); err == nil {
		t.Fatal("symlinked cache directory accepted")
	}
}

func TestSecuritySymlinkedCacheFileRejected(t *testing.T) {
	root := t.TempDir()
	path, err := Extract(fixture(), root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "libdivecomputer-0.dll")
	if err := os.WriteFile(outside, []byte("native-runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, path)
	if _, err := Extract(fixture(), root); err == nil {
		t.Fatal("symlinked library accepted even though content matches")
	}
}

func TestSecurityNoStagingLeftovers(t *testing.T) {
	root := t.TempDir()
	for range 3 {
		if _, err := Extract(fixture(), root); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".extract-") {
			t.Fatalf("staging directory left behind: %s", e.Name())
		}
	}
}

func TestSecurityCachePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	root := filepath.Join(t.TempDir(), "runtime")
	path, err := Extract(fixture(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root, filepath.Dir(path), path} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o022 != 0 {
			t.Errorf("%s mode %o is writable by group/others", p, fi.Mode().Perm())
		}
	}
}
