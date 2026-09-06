package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Settings struct {
	BackendURL string `json:"backend_url"`
	Vendor     string `json:"vendor"`
	Product    string `json:"product"`
	Port       string `json:"port"`
	Language   string `json:"language"`
}

func Path() (string, error) {
	root, err := os.UserConfigDir()
	return filepath.Join(root, "DiveVault Importer", "settings.json"), err
}
func Load() Settings {
	s := Settings{BackendURL: "https://divevault.local.joshuahemmings.ch", Vendor: "Mares", Product: "Smart Air", Language: Language(os.Getenv("LANG"))}
	p, err := Path()
	if err != nil {
		return s
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return s
	}
	// Decode into a copy so malformed files cannot partially overwrite defaults.
	candidate := s
	if json.Unmarshal(b, &candidate) == nil {
		s = candidate
	}
	s.Language = Language(s.Language)
	return s
}
func Language(s string) string {
	s = strings.ToLower(s)
	if strings.HasPrefix(s, "de") {
		return "de"
	}
	if strings.HasPrefix(s, "fr") {
		return "fr"
	}
	return "en"
}
func Save(s Settings) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0600)
}
