package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseVersionPrefersEnvironment(t *testing.T) {
	t.Setenv("DIVESYNC_VERSION", " 1.2.3-dev.abc1234 ")
	got, err := releaseVersion()
	if err != nil || got != "1.2.3-dev.abc1234" {
		t.Fatalf("releaseVersion() = %q, %v", got, err)
	}
}

func TestReleaseVersionFallsBackToFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("4.5.6\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("DIVESYNC_VERSION", "")
	got, err := releaseVersion()
	if err != nil || got != "4.5.6" {
		t.Fatalf("releaseVersion() = %q, %v", got, err)
	}
}

func TestReleaseVersionRejectsUnsafeValues(t *testing.T) {
	for _, v := range []string{"1.2", "v1.2.3", "1.2.3 -X main.x=y", "1.2.3+build", "../1.2.3", "1.2.3-"} {
		t.Setenv("DIVESYNC_VERSION", v)
		if got, err := releaseVersion(); err == nil {
			t.Errorf("releaseVersion() accepted %q as %q", v, got)
		}
	}
}

func TestInfoPlistUsesNumericBundleVersion(t *testing.T) {
	plist := infoPlist("0.3.0-dev.abc1234")
	for _, want := range []string{
		"<key>CFBundleShortVersionString</key><string>0.3.0</string>",
		"<key>CFBundleVersion</key><string>0.3.0</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %s", want)
		}
	}
	if strings.Contains(plist, "dev") {
		t.Error("plist leaks pre-release suffix into bundle version")
	}
}
