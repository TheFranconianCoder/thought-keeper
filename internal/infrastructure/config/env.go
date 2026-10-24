package config

import (
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// LoadDotEnv loads the first existing .env files from the given paths. Existing
// environment variables are never overridden, so a real environment always wins —
// this is a development convenience only. It returns the files it loaded.
func LoadDotEnv(paths ...string) []string {
	var loaded []string
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(expandHome(p))
		if err != nil {
			abs = expandHome(p)
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		if err := godotenv.Load(p); err == nil {
			loaded = append(loaded, p)
		}
	}
	return loaded
}

// DotEnvCandidates returns the default .env locations: the working directory, the
// directory of the config file (when given) and the app's config directory.
func DotEnvCandidates(configPath string) []string {
	list := []string{".env"}
	if configPath != "" {
		list = append(list, filepath.Join(filepath.Dir(expandHome(configPath)), ".env"))
	}
	list = append(list, filepath.Join(configDir(), "thought-keeper", ".env"))
	return list
}
