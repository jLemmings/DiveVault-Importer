// Package nativeruntime materializes embedded DLLs for the Windows system loader.
package nativeruntime

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Extract publishes a complete, content-addressed set atomically. Concurrent app
// instances reuse the same verified files without overwriting loaded libraries.
func Extract(bundle fs.FS, root string) (string, error) {
	entries, err := fs.ReadDir(bundle, ".")
	if err != nil {
		return "", err
	}
	files := map[string][]byte{}
	hash := sha256.New()
	library := ""
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".dll") {
			return "", fmt.Errorf("invalid bundled library %s", name)
		}
		data, err := fs.ReadFile(bundle, name)
		if err != nil {
			return "", err
		}
		if len(data) == 0 {
			return "", fmt.Errorf("empty bundled library %s", name)
		}
		files[name] = data
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(data))
		_, _ = hash.Write(data)
		if name == "libdivecomputer-0.dll" || name == "libdivecomputer.dll" {
			if library != "" {
				return "", fmt.Errorf("multiple libdivecomputer DLLs bundled")
			}
			library = name
		}
	}
	if library == "" {
		return "", fmt.Errorf("embedded libdivecomputer DLL is missing")
	}
	target := filepath.Join(root, fmt.Sprintf("%x", hash.Sum(nil)))
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		if err := os.MkdirAll(root, 0700); err != nil {
			return "", err
		}
		stage, err := os.MkdirTemp(root, ".extract-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(stage)
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(stage, name), data, 0600); err != nil {
				return "", err
			}
		}
		if err := os.Rename(stage, target); err != nil {
			if _, statErr := os.Stat(target); statErr != nil {
				return "", err
			}
		}
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("invalid runtime cache directory %s", target)
	}
	for name, want := range files {
		path := filepath.Join(target, name)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("invalid cached runtime file %s", path)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if !bytes.Equal(got, want) {
			return "", fmt.Errorf("runtime cache damaged; remove %s and restart DiveSync", target)
		}
	}
	return filepath.Abs(filepath.Join(target, library))
}
