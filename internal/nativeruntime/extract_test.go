package nativeruntime

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
)

func fixture() fstest.MapFS {
	return fstest.MapFS{"libdivecomputer-0.dll": {Data: []byte("native-runtime")}, "libusb.dll": {Data: []byte("dependency")}}
}
func TestExtractionAndReuse(t *testing.T) {
	root := t.TempDir()
	first, err := Extract(fixture(), root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Extract(fixture(), root)
	if err != nil || first != second {
		t.Fatalf("%s %s %v", first, second, err)
	}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(first), "libusb.dll")); err != nil || string(data) != "dependency" {
		t.Fatalf("dependency missing: %v", err)
	}
	changed := fixture()
	changed["libusb.dll"].Data = []byte("updated dependency")
	third, err := Extract(changed, root)
	if err != nil || first == third {
		t.Fatalf("updated runtime reused old directory: %v", err)
	}
}
func TestConcurrentExtraction(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if _, err := Extract(fixture(), root); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("incomplete extraction directories: %v %v", entries, err)
	}
}
func TestDamagedCacheRejected(t *testing.T) {
	root := t.TempDir()
	path, err := Extract(fixture(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(fixture(), root); err == nil {
		t.Fatal("modified library accepted")
	}
}
func TestMissingLibraryRejected(t *testing.T) {
	for _, bundle := range []fstest.MapFS{{}, {"libusb.dll": {Data: []byte("dependency")}}, {"libdivecomputer-0.dll": {Data: nil}}} {
		if _, err := Extract(bundle, t.TempDir()); err == nil {
			t.Fatal("incomplete bundle accepted")
		}
	}
}
