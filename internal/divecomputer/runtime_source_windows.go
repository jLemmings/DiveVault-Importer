//go:build windows && !bundled

package divecomputer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Development builds may use libusb/hidapi from the configured toolchain PATH.
const runtimeLoadFlags = 0x8 // LOAD_WITH_ALTERED_SEARCH_PATH

// Source builds load the explicitly built native runtime; release builds embed it.
func runtimePath() (string, error) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..", "vendor", "native", "bin")
	for _, name := range []string{"libdivecomputer-0.dll", "libdivecomputer.dll"} {
		path, err := filepath.Abs(filepath.Join(root, name))
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("libdivecomputer runtime not found in %s; build the native library first", root)
}
