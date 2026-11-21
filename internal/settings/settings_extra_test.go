package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultsWhenNoFile(t *testing.T) {
	isolated(t)
	t.Setenv("LANG", "fr_CH.UTF-8")
	got := Load()
	if got.BackendURL == "" || got.Vendor != "Mares" || got.Product != "Smart Air" || got.Port != "" || got.Language != "fr" {
		t.Fatalf("defaults=%+v", got)
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	isolated(t)
	write(t, `{"port":"/dev/ttyUSB0","unknown":true}`)
	got := Load()
	if got.Port != "/dev/ttyUSB0" || got.Vendor != "Mares" || got.Product != "Smart Air" || got.BackendURL == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestLanguageFromFileIsNormalized(t *testing.T) {
	isolated(t)
	for stored, want := range map[string]string{"DE-ch": "de", "fr_FR": "fr", "../../etc/passwd": "en", "it": "en", "": "en"} {
		b, _ := json.Marshal(map[string]string{"language": stored})
		write(t, string(b))
		if got := Load().Language; got != want {
			t.Errorf("stored %q loaded as %q, want %q", stored, got, want)
		}
	}
}

func TestLanguageTable(t *testing.T) {
	for in, want := range map[string]string{"": "en", "C": "en", "en_US.UTF-8": "en", "de": "de", "DE_AT": "de", "deutsch": "de", "fr": "fr", "FR_be.utf8": "fr", "es": "en", "xde": "en"} {
		if got := Language(in); got != want {
			t.Errorf("Language(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNullAndEmptyFilesUseDefaults(t *testing.T) {
	isolated(t)
	for _, body := range []string{"", "null", "[]", "{", "\x00\xff"} {
		write(t, body)
		if got := Load(); got.Vendor != "Mares" || got.Product != "Smart Air" {
			t.Errorf("body %q: %+v", body, got)
		}
	}
}

func TestSaveOverwritesAndWritesOnlyKnownKeys(t *testing.T) {
	isolated(t)
	if err := Save(Settings{Port: "COM1"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(Settings{BackendURL: "https://b.example", Port: "COM2", Language: "fr"}); err != nil {
		t.Fatal(err)
	}
	p, _ := Path()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"backend_url": true, "vendor": true, "product": true, "port": true, "language": true}
	for k := range raw {
		if !want[k] {
			t.Errorf("unexpected persisted key %q (secrets such as tokens must never be stored)", k)
		}
	}
	if raw["port"] != "COM2" {
		t.Fatalf("not overwritten: %s", b)
	}
}

func TestSecuritySettingsFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	isolated(t)
	if err := Save(Settings{BackendURL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	p, _ := Path()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("settings file mode %o is readable by other users", perm)
	}
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("settings directory mode %o is accessible by other users", perm)
	}
}

func FuzzLanguage(f *testing.F) {
	for _, s := range []string{"", "de", "fr", "en", "DE_ch", "../x", "\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		switch Language(s) {
		case "en", "de", "fr":
		default:
			t.Fatalf("Language(%q) returned unsupported value", s)
		}
	})
}

func FuzzLoad(f *testing.F) {
	for _, s := range []string{`{}`, `{"language":"de"}`, `{"port":1}`, `null`, `{"backend_url":"x","vendor":"y"}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		isolated(t)
		write(t, body)
		got := Load()
		switch got.Language {
		case "en", "de", "fr":
		default:
			t.Fatalf("unsupported language %q from %q", got.Language, body)
		}
	})
}

func write(t *testing.T, body string) {
	t.Helper()
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
