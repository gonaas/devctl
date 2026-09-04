package discovery

import (
	"testing"

	"github.com/gonaas/devctl/internal/adapters"
	"github.com/gonaas/devctl/internal/registry"
)

// First match wins, which is why declaration order is load-bearing.
var rules = []registry.ProductRule{
	{Prefix: "/home/dev/tree/carveout", Product: ""},
	{Prefix: "/home/dev/product", Product: "product"},
	{Prefix: "/home/dev/tree", Product: "tree"},
}

func TestPrefixRuleClaimsDescendants(t *testing.T) {
	for _, path := range []string{"/home/dev/product", "/home/dev/product/repo"} {
		product, ok := ProductFor(path, rules)
		if !ok || product != "product" {
			t.Errorf("%s: got %q ok=%v", path, product, ok)
		}
	}
}

func TestCarveOutIsNotAbsorbedByTheTreeAroundIt(t *testing.T) {
	// The carve-out is declared before the tree enclosing it. Reversing the two
	// would silently file it under the wrong product.
	for _, path := range []string{"/home/dev/tree/carveout", "/home/dev/tree/carveout/nested"} {
		if _, ok := ProductFor(path, rules); ok {
			t.Errorf("%s must be its own product", path)
		}
	}
	if product, ok := ProductFor("/home/dev/tree/other", rules); !ok || product != "tree" {
		t.Errorf("sibling under the tree: got %q ok=%v", product, ok)
	}
}

func TestReorderingTheRulesBreaksTheCarveOut(t *testing.T) {
	reordered := []registry.ProductRule{rules[2], rules[0], rules[1]}
	if product, ok := ProductFor("/home/dev/tree/carveout", reordered); !ok || product != "tree" {
		t.Errorf("expected the carve-out to be swallowed, got %q ok=%v", product, ok)
	}
}

func TestUnmatchedPathHasNoProduct(t *testing.T) {
	if product, ok := ProductFor("/home/elsewhere/repo", rules); !ok || product != "" {
		t.Errorf("got %q ok=%v", product, ok)
	}
}

func TestSiblingSharingANamePrefixIsNotClaimed(t *testing.T) {
	// /home/dev/treehouse must not match the /home/dev/tree rule.
	if product, ok := ProductFor("/home/dev/treehouse", rules); !ok || product != "" {
		t.Errorf("got %q ok=%v", product, ok)
	}
}

func TestDiscoverySurvivesAnEmptyRegistry(t *testing.T) {
	result := Discover(registry.Registry{}, adapters.Set{}, nil)
	if len(result.Repositories) != 0 || len(result.Findings) != 0 || len(result.SourceStatus) != 0 {
		t.Errorf("an empty registry must discover nothing: %+v", result)
	}
}
