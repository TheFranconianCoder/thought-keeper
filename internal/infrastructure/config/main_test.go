package config

import (
	"os"
	"testing"
)

// TestMain keeps the package's tests away from the real home: a config that leaves vault.root at its
// default resolves to ~/thought-keeper, and reading or writing that would touch a developer's vault.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "tk-config-home-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(home) //nolint:errcheck // the test process exits either way
	os.Exit(code)
}
