// Fetch the pinned upstream source without requiring Python.
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := fetch(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func fetch() error {
	const root = "vendor/libdivecomputer-0.9.0"
	if _, err := os.Stat(root + "/include/libdivecomputer/parser.h"); err == nil {
		return nil
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Get("https://libdivecomputer.org/releases/libdivecomputer-0.9.0.tar.gz")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", response.Status)
	}
	gz, err := gzip.NewReader(response.Body)
	if err != nil {
		return err
	}
	defer gz.Close()
	if err := os.MkdirAll("vendor", 0755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp("vendor", ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) || !strings.HasPrefix(h.Name, "libdivecomputer-0.9.0/") {
			return fmt.Errorf("unsafe archive path: %s", h.Name)
		}
		dest := filepath.Join(staging, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, os.FileMode(h.Mode)&0777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported archive entry: %s", h.Name)
		}
	}
	return os.Rename(filepath.Join(staging, "libdivecomputer-0.9.0"), root)
}
