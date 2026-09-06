package main

import (
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/jLemmings/DiveVault-Importer/internal/settings"
	"runtime"
	"testing"
)

func TestDesktopStateAndSettings(t *testing.T) {
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
	case "darwin":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	a := test.NewApp()
	defer a.Quit()
	a.Settings().SetTheme(newDiveVaultTheme())
	d := &desktop{app: a, window: a.NewWindow("test"), config: settings.Settings{BackendURL: "https://example.com", Vendor: "Mares", Product: "Smart Air", Port: "COM8", Language: "de"}, labels: map[*widget.Label]string{}, cards: map[*widget.Card]string{}, models: map[string][]string{"Mares": {"Smart", "Smart Air"}, "Suunto": {"D4"}}}
	d.build()
	d.vendor.Options = []string{"Mares", "Suunto"}
	d.vendor.SetSelected("Mares")
	if d.product.Selected != "Smart Air" || d.preferredPort != "COM8" {
		t.Fatalf("saved selection lost: %s %s", d.product.Selected, d.preferredPort)
	}
	if !d.login.Disabled() || !d.sync.Disabled() {
		t.Fatal("login/sync enabled without detection")
	}
	d.port.Options = []string{"COM8"}
	d.port.SetSelected("COM8")
	if d.login.Disabled() || !d.sync.Disabled() {
		t.Fatal("detection state incorrect")
	}
	d.token = "secret"
	d.update()
	if d.sync.Disabled() {
		t.Fatal("authenticated sync disabled")
	}
	d.server.SetText("https://other.example.com")
	if d.token != "" || !d.sync.Disabled() {
		t.Fatal("token persisted across backends")
	}
	d.vendor.SetSelected("Suunto")
	if d.port.Selected != "" || !d.login.Disabled() {
		t.Fatal("device change retained detection")
	}
	d.language.SetSelected("Français")
	if d.login.Text != translator("fr")("SIGN IN") {
		t.Fatal("translation not applied")
	}
}
