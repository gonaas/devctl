package registry

import (
	_ "embed"
	"os"
	"path/filepath"
)

// defaultRegistry is compiled into the binary. A distributed binary has no
// repository beside it, so the shipped defaults must travel with it.
//
//go:embed tools.toml
var defaultRegistry []byte

// UserConfigPath is where a user-owned registry overrides the built-in one.
func UserConfigPath(home string) string {
	if configured := os.Getenv("XDG_CONFIG_HOME"); configured != "" {
		return filepath.Join(configured, "devctl", "tools.toml")
	}
	return filepath.Join(home, ".config", "devctl", "tools.toml")
}

// Default returns the embedded registry contents, for `devctl config registry`.
func Default() []byte { return defaultRegistry }

// builtin loads the user's own registry when present, otherwise the embedded
// defaults. Precedence, highest first: an explicit path, DEVCTL_REGISTRY, the
// user config file, the embedded defaults.
func builtin(home string) (Registry, error) {
	userPath := UserConfigPath(home)
	if raw, err := os.ReadFile(userPath); err == nil {
		registry, parseErr := parse(raw, home)
		if parseErr != nil {
			return Registry{}, parseErr
		}
		registry.SourcePath = userPath
		return registry, nil
	}
	registry, err := parse(defaultRegistry, home)
	if err != nil {
		return Registry{}, err
	}
	registry.SourcePath = "(built in)"
	return registry, nil
}
