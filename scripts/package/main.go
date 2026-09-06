// Build a desktop bundle containing the native libdivecomputer runtime.
package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
func copyFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func build() error {
	// A fresh staging directory prevents stale libraries from entering releases.
	if err := os.MkdirAll("dist", 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("dist", ".package-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	base := filepath.Join(stage, "DiveSync")
	binDir, libDir := base, base
	exe := "DiveSync"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if runtime.GOOS == "darwin" {
		base = filepath.Join(stage, "DiveSync.app")
		binDir = filepath.Join(base, "Contents", "MacOS")
		libDir = filepath.Join(base, "Contents", "Frameworks")
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(libDir, 0755); err != nil {
		return err
	}
	args := []string{"build", "-mod=readonly", "-trimpath", "-o", filepath.Join(binDir, exe)}
	if runtime.GOOS == "windows" {
		cleanup, err := prepareWindowsRuntime()
		if err != nil {
			return err
		}
		defer cleanup()
		args = append(args, "-tags=bundled", "-ldflags=-H=windowsgui")
	}
	args = append(args, ".")
	if err := run("go", args...); err != nil {
		return err
	}
	var libs []string
	switch runtime.GOOS {
	case "windows":
		// Native libraries were embedded before compilation.
	case "linux":
		libs, err = filepath.Glob("vendor/native/lib/libdivecomputer.so*")
	case "darwin":
		libs, err = filepath.Glob("vendor/native/lib/libdivecomputer*.dylib")
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	if err != nil {
		return err
	}
	if len(libs) == 0 && runtime.GOOS != "windows" {
		return fmt.Errorf("no native runtime found; build libdivecomputer first")
	}
	for _, lib := range libs {
		if err := copyFile(lib, filepath.Join(libDir, filepath.Base(lib))); err != nil {
			return err
		}
	}
	if runtime.GOOS == "windows" {
		// Reject any accidentally introduced non-system static DLL imports.
		if err := windowsDependencies(binDir); err != nil {
			return err
		}
		remaining, err := filepath.Glob(filepath.Join(binDir, "*.dll"))
		if err != nil {
			return err
		}
		if len(remaining) > 0 {
			return fmt.Errorf("executable still requires external DLLs: %v", remaining)
		}
	}
	if runtime.GOOS == "darwin" {
		if err := macBundle(base, binDir, libDir); err != nil {
			return err
		}
	}
	metaDir := base
	if runtime.GOOS == "darwin" {
		metaDir = filepath.Join(base, "Contents", "Resources")
		if err := os.MkdirAll(metaDir, 0755); err != nil {
			return err
		}
	}
	for _, name := range []string{"LICENSE", "VERSION"} {
		if err := copyFile(name, filepath.Join(metaDir, name)); err != nil {
			return err
		}
	}
	if err := copyFile("vendor/libdivecomputer-0.9.0/COPYING", filepath.Join(metaDir, "LICENSE-libdivecomputer")); err != nil {
		return err
	}
	if err := copyFile("go.mod", filepath.Join(metaDir, "go.mod")); err != nil {
		return err
	}
	if err := moduleLicenses(metaDir); err != nil {
		return err
	}
	version, err := os.ReadFile("VERSION")
	if err != nil {
		return err
	}
	archive := filepath.Join("dist", fmt.Sprintf("DiveSync-%s-%s-%s.zip", runtime.GOOS, runtime.GOARCH, strings.TrimSpace(string(version))))
	if runtime.GOOS == "windows" {
		standalone := strings.TrimSuffix(archive, ".zip") + ".exe"
		if err := copyFile(filepath.Join(binDir, exe), standalone); err != nil {
			return err
		}
	}
	if runtime.GOOS == "darwin" {
		if err := run("codesign", "--force", "--deep", "--sign", "-", base); err != nil {
			return err
		}
		return run("ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", base, archive)
	}
	return zipTree(base, archive)
}

// Embed all native dependencies, not just the top-level libdivecomputer DLL.
func prepareWindowsRuntime() (func(), error) {
	const dir = "internal/divecomputer/runtime"
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, fmt.Errorf("prepare generated embed directory (another package build may be running): %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	libs, err := filepath.Glob("vendor/native/bin/*.dll")
	if err != nil {
		cleanup()
		return nil, err
	}
	if len(libs) == 0 {
		cleanup()
		return nil, fmt.Errorf("no native DLLs found; build libdivecomputer first")
	}
	for _, lib := range libs {
		if err := copyFile(lib, filepath.Join(dir, filepath.Base(lib))); err != nil {
			cleanup()
			return nil, err
		}
	}
	if err := windowsDependencies(dir); err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}
func windowsDependencies(dir string) error {
	queue, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		name := strings.ToLower(filepath.Base(file))
		if seen[name] {
			continue
		}
		seen[name] = true
		out, err := exec.Command("objdump", "-p", file).Output()
		if err != nil {
			return fmt.Errorf("inspect %s: %w", file, err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			parts := strings.Fields(line)
			if len(parts) != 3 || parts[0] != "DLL" || parts[1] != "Name:" {
				continue
			}
			dll := parts[2]
			dest := filepath.Join(dir, dll)
			if _, err := os.Stat(dest); err == nil {
				queue = append(queue, dest)
				continue
			}
			if strings.HasPrefix(strings.ToLower(dll), "api-ms-") || strings.HasPrefix(strings.ToLower(dll), "ext-ms-") {
				continue
			}
			if _, err := os.Stat(filepath.Join(os.Getenv("SystemRoot"), "System32", dll)); err == nil {
				continue
			}
			var found string
			for _, path := range filepath.SplitList(os.Getenv("PATH")) {
				candidate := filepath.Join(path, dll)
				if _, err := os.Stat(candidate); err == nil {
					found = candidate
					break
				}
			}
			if found == "" {
				// MSYS2 packages may ship hidapi under transport-specific names.
				for _, alt := range windowsDependencyAlternatives(dll) {
					for _, path := range filepath.SplitList(os.Getenv("PATH")) {
						candidate := filepath.Join(path, alt)
						if _, err := os.Stat(candidate); err == nil {
							found = candidate
							break
						}
					}
					if found != "" {
						break
					}
				}
			}
			if found == "" {
				return fmt.Errorf("missing runtime dependency %s (required by %s)", dll, file)
			}
			if err := copyFile(found, dest); err != nil {
				return err
			}
			queue = append(queue, dest)
		}
	}
	return nil
}

func windowsDependencyAlternatives(dll string) []string {
	switch strings.ToLower(dll) {
	case "libhidapi-0.dll":
		return []string{"libhidapi-hidraw-0.dll", "libhidapi-libusb-0.dll", "libhidapi.dll"}
	default:
		return nil
	}
}
func macBundle(base, binDir, libDir string) error {
	plist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleName</key><string>DiveSync</string><key>CFBundleDisplayName</key><string>DiveSync</string><key>CFBundleIdentifier</key><string>ch.divevault.importer</string><key>CFBundleExecutable</key><string>DiveSync</string><key>CFBundlePackageType</key><string>APPL</string><key>NSHighResolutionCapable</key><true/></dict></plist>`
	if err := os.WriteFile(filepath.Join(base, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		return err
	}
	queue := []string{filepath.Join(binDir, "DiveSync")}
	libs, _ := filepath.Glob(filepath.Join(libDir, "*"))
	queue = append(queue, libs...)
	seen := map[string]bool{}
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		if seen[file] {
			continue
		}
		seen[file] = true
		out, err := exec.Command("otool", "-L", file).Output()
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(out), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			dep := fields[0]
			if strings.HasPrefix(dep, "/usr/lib/") || strings.HasPrefix(dep, "/System/") || strings.HasPrefix(dep, "@") {
				continue
			}
			name := filepath.Base(dep)
			dest := filepath.Join(libDir, name)
			if _, err := os.Stat(dest); os.IsNotExist(err) {
				if err := copyFile(dep, dest); err != nil {
					return err
				}
			}
			queue = append(queue, dest)
			if err := run("install_name_tool", "-change", dep, "@rpath/"+name, file); err != nil {
				return err
			}
		}
		if strings.HasSuffix(file, ".dylib") {
			if err := run("install_name_tool", "-id", "@rpath/"+filepath.Base(file), file); err != nil {
				return err
			}
		}
	}
	return nil
}

func moduleLicenses(base string) error {
	cmd := exec.Command("go", "list", "-mod=readonly", "-m", "-json", "all")
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var module struct {
			Path, Version, Dir string
			Main               bool
		}
		if err := decoder.Decode(&module); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if module.Main || module.Dir == "" {
			continue
		}
		entries, err := os.ReadDir(module.Dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := strings.ToUpper(entry.Name())
			if entry.IsDir() || !(strings.HasPrefix(name, "LICENSE") || strings.HasPrefix(name, "LICENCE") || strings.HasPrefix(name, "COPYING") || strings.HasPrefix(name, "NOTICE")) {
				continue
			}
			dest := filepath.Join(base, "licenses", filepath.FromSlash(module.Path)+"@"+module.Version, entry.Name())
			if err := copyFile(filepath.Join(module.Dir, entry.Name()), dest); err != nil {
				return err
			}
		}
	}
}
func zipTree(root, dest string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	z := zip.NewWriter(f)
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filepath.Dir(root), path)
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		h.Method = zip.Deflate
		w, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
	zipErr := z.Close()
	closeErr := f.Close()
	if walkErr != nil {
		return walkErr
	}
	if zipErr != nil {
		return zipErr
	}
	return closeErr
}
