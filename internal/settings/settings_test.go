package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func isolated(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
	case "darwin":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
}
func TestLegacySettings(t *testing.T) {
	isolated(t)
	s := Settings{BackendURL: "https://example.com", Vendor: "Mares", Product: "Smart Air", Port: "COM8", Language: "de"}
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	p, _ := Path()
	if filepath.Base(filepath.Dir(p)) != "DiveVault Importer" {
		t.Fatal(p)
	}
	if got := Load(); got != s {
		t.Fatalf("got %+v want %+v", got, s)
	}
	b, _ := os.ReadFile(p)
	if string(b) == "" {
		t.Fatal("empty settings")
	}
}
func TestMalformedSettingsUseDefaults(t *testing.T) {
	isolated(t)
	p, _ := Path()
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	_ = os.WriteFile(p, []byte(`{"backend_url":"broken","port":123}`), 0600)
	got := Load()
	if got.BackendURL == "broken" || got.Product != "Smart Air" {
		t.Fatal(got)
	}
}
