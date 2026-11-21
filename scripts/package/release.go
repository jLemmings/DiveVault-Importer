package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Release builds are configured through the environment so local builds keep
// working without any secrets:
//
//	DIVESYNC_VERSION            version to stamp (default: contents of VERSION)
//	MACOS_SIGN_IDENTITY         Developer ID Application identity; ad-hoc signing when empty
//	MACOS_NOTARY_APPLE_ID       \
//	MACOS_NOTARY_TEAM_ID         > notarize and staple when all three are set
//	MACOS_NOTARY_PASSWORD       /  (app-specific password)
//	WINDOWS_SIGN_CERT_FILE      PFX certificate; Windows binaries stay unsigned when empty
//	WINDOWS_SIGN_CERT_PASSWORD  password for the PFX
//	SIGNTOOL                    signtool.exe path (default: signtool on PATH)
//	SIGN_TIMESTAMP_URL          RFC 3161 timestamp server (default: DigiCert)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

func releaseVersion() (string, error) {
	version := strings.TrimSpace(os.Getenv("DIVESYNC_VERSION"))
	if version == "" {
		b, err := os.ReadFile("VERSION")
		if err != nil {
			return "", err
		}
		version = strings.TrimSpace(string(b))
	}
	if !versionPattern.MatchString(version) {
		return "", fmt.Errorf("invalid version %q: want MAJOR.MINOR.PATCH[-prerelease]", version)
	}
	return version, nil
}

// bundleVersion is the numeric MAJOR.MINOR.PATCH part macOS requires for
// CFBundleShortVersionString and CFBundleVersion.
func bundleVersion(version string) string {
	core, _, _ := strings.Cut(version, "-")
	return core
}

func infoPlist(version string) string {
	v := bundleVersion(version)
	return `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict>` +
		`<key>CFBundleName</key><string>DiveSync</string>` +
		`<key>CFBundleDisplayName</key><string>DiveSync</string>` +
		`<key>CFBundleIdentifier</key><string>ch.divevault.importer</string>` +
		`<key>CFBundleExecutable</key><string>DiveSync</string>` +
		`<key>CFBundlePackageType</key><string>APPL</string>` +
		`<key>CFBundleShortVersionString</key><string>` + v + `</string>` +
		`<key>CFBundleVersion</key><string>` + v + `</string>` +
		`<key>NSHighResolutionCapable</key><true/>` +
		`</dict></plist>`
}

func timestampURL() string {
	if u := os.Getenv("SIGN_TIMESTAMP_URL"); u != "" {
		return u
	}
	return "http://timestamp.digicert.com"
}

func signWindows(binary string) error {
	cert := os.Getenv("WINDOWS_SIGN_CERT_FILE")
	if cert == "" {
		fmt.Println("WINDOWS_SIGN_CERT_FILE not set; leaving Windows executable unsigned")
		return nil
	}
	signtool := os.Getenv("SIGNTOOL")
	if signtool == "" {
		signtool = "signtool"
	}
	args := []string{"sign", "/fd", "SHA256", "/tr", timestampURL(), "/td", "SHA256", "/f", cert}
	if pw := os.Getenv("WINDOWS_SIGN_CERT_PASSWORD"); pw != "" {
		args = append(args, "/p", pw)
	}
	if err := run(signtool, append(args, binary)...); err != nil {
		return fmt.Errorf("sign %s: %w", binary, err)
	}
	return run(signtool, "verify", "/pa", binary)
}

func signAndArchiveMac(app, libDir, binary, archive string) error {
	identity := os.Getenv("MACOS_SIGN_IDENTITY")
	if identity == "" {
		fmt.Println("MACOS_SIGN_IDENTITY not set; using ad-hoc signature (not notarized)")
		if err := run("codesign", "--force", "--deep", "--sign", "-", app); err != nil {
			return err
		}
		return dittoZip(app, archive)
	}
	// Sign inside-out with the hardened runtime, as notarization requires.
	libs, err := filepath.Glob(filepath.Join(libDir, "*"))
	if err != nil {
		return err
	}
	sign := func(path string) error {
		return run("codesign", "--force", "--options", "runtime", "--timestamp", "--sign", identity, path)
	}
	for _, path := range append(libs, binary, app) {
		if err := sign(path); err != nil {
			return fmt.Errorf("codesign %s: %w", path, err)
		}
	}
	if err := run("codesign", "--verify", "--strict", "--verbose=2", app); err != nil {
		return err
	}
	if err := dittoZip(app, archive); err != nil {
		return err
	}
	appleID, team, password := os.Getenv("MACOS_NOTARY_APPLE_ID"), os.Getenv("MACOS_NOTARY_TEAM_ID"), os.Getenv("MACOS_NOTARY_PASSWORD")
	if appleID == "" || team == "" || password == "" {
		fmt.Println("notarization credentials not set; skipping notarization")
		return nil
	}
	if err := run("xcrun", "notarytool", "submit", archive, "--apple-id", appleID, "--team-id", team, "--password", password, "--wait"); err != nil {
		return fmt.Errorf("notarize: %w", err)
	}
	if err := run("xcrun", "stapler", "staple", app); err != nil {
		return err
	}
	// Re-archive so the downloaded app carries the stapled ticket offline.
	if err := os.Remove(archive); err != nil {
		return err
	}
	return dittoZip(app, archive)
}

func dittoZip(app, archive string) error {
	return run("ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", app, archive)
}
