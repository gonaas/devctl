package gitx

import (
	"os"
	"testing"
)

// TestMain gives this package's git invocations a fixed committer identity.
//
// These tests shell out to a real git binary, and git needs an identity for any
// operation that may create an object — including a merge that is expected to stop
// on a conflict, because git resolves the identity before it starts. Without this
// the suite passes on a machine that happens to have one configured and fails on a
// clean runner, which is how TestInterruptedMergeIsFoundInAMainCheckout stayed red
// for a month while nothing in the tool was broken: the tool never claimed the
// merge had succeeded, the test was reading its own environment.
//
// Only the identity is pinned here. Ambient configuration beyond it is left alone,
// so a test that depends on it still fails loudly rather than silently passing.
func TestMain(m *testing.M) {
	for key, value := range map[string]string{
		"GIT_AUTHOR_NAME":     "devctl test",
		"GIT_AUTHOR_EMAIL":    "test@example.invalid",
		"GIT_COMMITTER_NAME":  "devctl test",
		"GIT_COMMITTER_EMAIL": "test@example.invalid",
	} {
		if err := os.Setenv(key, value); err != nil {
			panic("could not pin the git identity these tests depend on: " + err.Error())
		}
	}
	os.Exit(m.Run())
}
