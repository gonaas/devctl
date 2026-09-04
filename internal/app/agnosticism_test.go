package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every product name this stack currently happens to use. If any of these appears
// in the tool's logic, replacing the stack becomes a code change instead of a
// registry edit, which is exactly what this project promises not to require.
//
// "cursor" is deliberately absent: it is a standard database and terminal term
// long before it is a product, so scanning for it only produces noise.
var vendorTokens = []string{
	"engram", "gentle-ai", "gentleai", "claude", "codex",
	"opencode", "copilot", "gemini", "anthropic",
}

// Files allowed to name a vendor: the adapter package implements one concrete
// provider each, and the registry carries the data that names them.
var exemptPaths = map[string]bool{
	"internal/adapters/forge_github.go":    true,
	"internal/adapters/runtime_compose.go": true,
	"internal/registry/registry.go":        true,
	"internal/registry/builtin.go":         true,
	"internal/registry/tools.toml":         true,
	"internal/app/agnosticism_test.go":     true,
}

func vendorPattern() *regexp.Regexp {
	escaped := make([]string, 0, len(vendorTokens))
	for _, token := range vendorTokens {
		escaped = append(escaped, regexp.QuoteMeta(token))
	}
	// Whole words only, so ordinary English is not flagged.
	return regexp.MustCompile(`(?i)(^|[^\w-])(` + strings.Join(escaped, "|") + `)($|[^\w-])`)
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(directory, "go.mod")); statErr == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not locate the module root")
		}
		directory = parent
	}
}

// TestNoVendorTokenInLogic guards the layering promise with a check rather than
// with good intentions. Without it, the agnosticism claim is only a wish.
func TestNoVendorTokenInLogic(t *testing.T) {
	root := moduleRoot(t)
	pattern := vendorPattern()
	var offenders []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil || exemptPaths[filepath.ToSlash(relative)] {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for number, line := range strings.Split(string(raw), "\n") {
			if match := pattern.FindStringSubmatch(line); match != nil {
				offenders = append(offenders, filepath.ToSlash(relative)+":"+
					itoa(number+1)+": "+match[2]+" in "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("vendor names must live in the registry, not in the logic:\n%s",
			strings.Join(offenders, "\n"))
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func TestHelpNamesNoVendor(t *testing.T) {
	var builder strings.Builder
	printHelp(&builder, "1.2.3")
	if match := vendorPattern().FindStringSubmatch(builder.String()); match != nil {
		t.Errorf("the help text names a vendor: %q", match[2])
	}
	if !strings.Contains(builder.String(), "1.2.3") {
		t.Error("help must report the running version")
	}
}
