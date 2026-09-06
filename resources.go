package main

import (
	"embed"
	"encoding/json"
	"strings"
)

//go:embed assets/logo.png assets/logo.ico assets/logo_header.png translations/*.json VERSION
var resources embed.FS

func version() string { b, _ := resources.ReadFile("VERSION"); return strings.TrimSpace(string(b)) }
func translator(language string) func(string) string {
	b, _ := resources.ReadFile("translations/" + language + ".json")
	messages := map[string]string{}
	_ = json.Unmarshal(b, &messages)
	return func(key string) string {
		if value := messages[key]; value != "" {
			return value
		}
		return key
	}
}
