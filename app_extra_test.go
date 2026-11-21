package main

import (
	"encoding/json"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/jLemmings/DiveVault-Importer/internal/importer"
	"github.com/jLemmings/DiveVault-Importer/internal/settings"
)

func isolateConfig(t *testing.T) string {
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
	return dir
}

func newTestDesktop(t *testing.T, cfg settings.Settings) *desktop {
	t.Helper()
	isolateConfig(t)
	a := test.NewApp()
	t.Cleanup(a.Quit)
	d := &desktop{app: a, window: a.NewWindow("test"), config: cfg, labels: map[*widget.Label]string{}, cards: map[*widget.Card]string{}, models: map[string][]string{"Mares": {"Smart Air"}}}
	d.build()
	return d
}

func loadTranslations(t *testing.T) map[string]map[string]string {
	t.Helper()
	files, err := filepath.Glob("translations/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no translations: %v", err)
	}
	all := map[string]map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		all[strings.TrimSuffix(filepath.Base(f), ".json")] = m
	}
	return all
}

var placeholder = regexp.MustCompile(`\{[a-z_]+\}`)

func placeholders(s string) []string {
	p := placeholder.FindAllString(s, -1)
	sort.Strings(p)
	return p
}

func TestTranslationsAreCompleteAndConsistent(t *testing.T) {
	all := loadTranslations(t)
	en, ok := all["en"]
	if !ok {
		t.Fatal("en.json missing")
	}
	for _, lang := range []string{"de", "fr"} {
		if _, ok := all[lang]; !ok {
			t.Fatalf("%s.json missing", lang)
		}
	}
	for lang, m := range all {
		for key := range en {
			v, ok := m[key]
			if !ok {
				t.Errorf("%s: missing key %q", lang, key)
				continue
			}
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: empty value for %q", lang, key)
			}
			if got, want := placeholders(v), placeholders(en[key]); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("%s: %q placeholders %v, want %v", lang, key, got, want)
			}
		}
		for key := range m {
			if _, ok := en[key]; !ok {
				t.Errorf("%s: key %q not present in en.json", lang, key)
			}
		}
	}
}

func TestAllUIStringsHaveTranslations(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	keys := regexp.MustCompile(`d\.(?:tr|label|card)\("((?:[^"\\]|\\.)*)"`).FindAllStringSubmatch(string(src), -1)
	if len(keys) < 10 {
		t.Fatalf("only found %d UI strings; did the source layout change?", len(keys))
	}
	all := loadTranslations(t)
	for _, k := range keys {
		for lang, m := range all {
			if _, ok := m[k[1]]; !ok {
				t.Errorf("%s: UI string %q has no translation", lang, k[1])
			}
		}
	}
}

func TestTranslatorFallbacks(t *testing.T) {
	if got := translator("en")("SIGN IN"); got == "" || got == "SIGN IN" {
		t.Fatalf("English translation missing: %q", got)
	}
	if got := translator("de")("not-a-key"); got != "not-a-key" {
		t.Fatalf("unknown key = %q", got)
	}
	if got := translator("xx")("SIGN IN"); got != "SIGN IN" {
		t.Fatalf("unknown language = %q", got)
	}
}

func TestSecurityTranslatorCannotEscapeTranslationsDirectory(t *testing.T) {
	for _, lang := range []string{"../VERSION", "../assets/logo", "../../go", "en/../de", "/etc/passwd", "..\\VERSION", "en.json\x00"} {
		tr := translator(lang)
		if got := tr("probe"); got != "probe" {
			t.Errorf("translator(%q) loaded unexpected data: %q", lang, got)
		}
	}
}

func TestEmbeddedResources(t *testing.T) {
	for _, name := range []string{"assets/logo.png", "assets/logo.ico", "assets/logo_header.png", "translations/en.json", "translations/de.json", "translations/fr.json", "VERSION"} {
		b, err := resources.ReadFile(name)
		if err != nil || len(b) == 0 {
			t.Errorf("%s not embedded: %v", name, err)
		}
	}
	png, _ := resources.ReadFile("assets/logo.png")
	if !strings.HasPrefix(string(png), "\x89PNG") {
		t.Error("logo.png is not a PNG")
	}
}

func TestVersionMatchesFile(t *testing.T) {
	b, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if version() != strings.TrimSpace(string(b)) {
		t.Fatalf("version()=%q", version())
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`).MatchString(version()) {
		t.Fatalf("VERSION %q is not semantic", version())
	}
}

func TestThemePalette(t *testing.T) {
	th := newDiveVaultTheme()
	if got := th.Color(theme.ColorNameBackground, theme.VariantLight); got != (color.NRGBA{0, 21, 37, 255}) {
		t.Fatalf("background=%v", got)
	}
	// Unmapped colours must always come from the dark variant regardless of OS theme.
	want := theme.DarkTheme().Color(theme.ColorNameSuccess, theme.VariantDark)
	if got := th.Color(theme.ColorNameSuccess, theme.VariantLight); got != want {
		t.Fatalf("fallback=%v want %v", got, want)
	}
	for _, name := range []fyne.ThemeColorName{theme.ColorNameForeground, theme.ColorNamePrimary, theme.ColorNameError, theme.ColorNameButton, theme.ColorNameInputBackground} {
		if _, _, _, a := th.Color(name, theme.VariantLight).RGBA(); a == 0 {
			t.Errorf("%s is fully transparent", name)
		}
	}
}

func TestProgressAndResultText(t *testing.T) {
	d := &desktop{tr: translator("en")}
	total := 42
	if got := d.progressText(importer.Result{Imported: 3, Skipped: 1}); !strings.Contains(got, "3") || !strings.Contains(got, "1") || strings.Contains(got, "{") {
		t.Errorf("progress=%q", got)
	}
	if got := d.resultText(importer.Result{ExistingTotal: &total}); !strings.Contains(got, "42") || strings.Contains(got, "{") {
		t.Errorf("no-new result=%q", got)
	}
	if got := d.resultText(importer.Result{Imported: 5, Skipped: 2, ExistingTotal: &total}); !strings.Contains(got, "5") || strings.Contains(got, "42") || strings.Contains(got, "{") {
		t.Errorf("result=%q", got)
	}
	if got := d.resultText(importer.Result{}); strings.Contains(got, "{") {
		t.Errorf("result without total=%q", got)
	}
	for _, lang := range []string{"de", "fr"} {
		d.tr = translator(lang)
		if got := d.resultText(importer.Result{Imported: 1}); strings.Contains(got, "{") {
			t.Errorf("%s result has unreplaced placeholder: %q", lang, got)
		}
	}
}

func TestBusyStateDisablesControlsAndCancels(t *testing.T) {
	d := newTestDesktop(t, settings.Settings{BackendURL: "https://example.com", Vendor: "Mares", Product: "Smart Air", Language: "en"})
	d.vendor.Options = []string{"Mares"}
	d.vendor.SetSelected("Mares")
	d.port.Options = []string{"COM3"}
	d.port.SetSelected("COM3")
	d.token = "tok"
	d.update()
	if d.sync.Disabled() || d.login.Disabled() || d.scan.Disabled() || !d.cancelButton.Disabled() {
		t.Fatal("idle state incorrect")
	}
	cancelled := false
	d.cancel = func() { cancelled = true }
	d.setBusy(true)
	for name, w := range map[string]fyne.Disableable{"vendor": d.vendor, "product": d.product, "port": d.port, "server": d.server, "language": d.language, "scan": d.scan, "login": d.login, "sync": d.sync} {
		if !w.Disabled() {
			t.Errorf("%s enabled while busy", name)
		}
	}
	if d.cancelButton.Disabled() || !d.progress.Visible() {
		t.Fatal("cancel/progress not available while busy")
	}
	d.setBusy(false)
	if !cancelled || d.cancel != nil || d.progress.Visible() {
		t.Fatal("leaving busy state did not release the context")
	}
}

func TestLoginDisabledWithoutBackendURL(t *testing.T) {
	d := newTestDesktop(t, settings.Settings{BackendURL: "https://example.com", Vendor: "Mares", Product: "Smart Air", Language: "en"})
	d.port.Options = []string{"COM3"}
	d.port.SetSelected("COM3")
	d.server.SetText("   ")
	if d.config.BackendURL != "" || !d.login.Disabled() {
		t.Fatal("login enabled without backend URL")
	}
}

func TestSecurityInvalidBackendURLIsRejectedBeforeNetwork(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", "https://user:pass@example.com", "ftp://example.com"} {
		d := newTestDesktop(t, settings.Settings{BackendURL: u, Vendor: "Mares", Product: "Smart Air", Language: "en"})
		d.startLogin()
		if d.busy || d.cancel != nil {
			t.Errorf("%s: login started", u)
		}
		if !strings.Contains(d.status.Text, "valid HTTP") {
			t.Errorf("%s: status=%q", u, d.status.Text)
		}
		d.token = "tok"
		d.startSync()
		if d.busy || d.cancel != nil {
			t.Errorf("%s: sync started", u)
		}
	}
}

func TestSecurityTokenIsNeverPersisted(t *testing.T) {
	const token = "very-secret-bearer-token"
	d := newTestDesktop(t, settings.Settings{BackendURL: "https://example.com", Vendor: "Mares", Product: "Smart Air", Port: "COM1", Language: "en"})
	d.token = token
	d.persist()
	d.port.Options = []string{"COM2"}
	d.port.SetSelected("COM2")
	p, _ := settings.Path()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), token) {
		t.Fatal("bearer token written to settings file")
	}
}

func TestSecurityTokenDroppedWhenBackendChanges(t *testing.T) {
	d := newTestDesktop(t, settings.Settings{BackendURL: "https://example.com", Vendor: "Mares", Product: "Smart Air", Language: "en"})
	d.token = "tok"
	// Even a cosmetic edit must drop the token so it is never sent to a different host.
	d.server.SetText("https://example.com.evil.test")
	if d.token != "" {
		t.Fatal("token kept after backend URL change")
	}
}

func TestLoggerWritesPrivateLogFile(t *testing.T) {
	dir := isolateConfig(t)
	prev := appLogger
	t.Cleanup(func() { appLogger = prev })
	closeLog, err := initLogger()
	if err != nil {
		t.Fatal(err)
	}
	logf("hello %s", "world")
	closeLog()
	root, _ := os.UserConfigDir()
	if !strings.HasPrefix(root, dir) {
		t.Fatalf("config dir not isolated: %s", root)
	}
	path := filepath.Join(root, "DiveVault Importer", "logs", "client.log")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "hello world") || !strings.Contains(string(b), "log initialized") {
		t.Fatalf("log=%q", b)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("log file mode %o readable by others", fi.Mode().Perm())
		}
	}
}

func TestLogfWithoutLoggerIsNoop(t *testing.T) {
	prev := appLogger
	appLogger = nil
	t.Cleanup(func() { appLogger = prev })
	logf("no logger %v", errors.New("x"))
}
