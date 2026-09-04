package process

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestArgumentsAreNeverInterpretedByAShell(t *testing.T) {
	// A value that would be catastrophic if a shell ever saw it must arrive at
	// the program as one ordinary argument.
	payload := "; rm -rf /tmp/should-never-happen"
	result := Run([]string{"echo", payload}, Options{})
	if !result.OK() {
		t.Fatalf("echo failed: %s", result.Stderr)
	}
	if strings.TrimSpace(result.Stdout) != payload {
		t.Errorf("argument was transformed: %q", result.Stdout)
	}
}

func TestMissingBinaryBecomesAResultNotAPanic(t *testing.T) {
	result := Run([]string{"devctl-no-such-binary-exists"}, Options{})
	if result.OK() {
		t.Fatal("a missing binary must not report success")
	}
	if result.Stderr == "" {
		t.Error("a missing binary must explain itself")
	}
}

func TestEmptyCommandIsRefused(t *testing.T) {
	if result := Run(nil, Options{}); result.OK() {
		t.Error("an empty command must never be run")
	}
}

func TestTimeoutIsReportedRatherThanHanging(t *testing.T) {
	start := time.Now()
	result := Run([]string{"sleep", "30"}, Options{Timeout: 150 * time.Millisecond})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the timeout did not fire: waited %s", elapsed)
	}
	if !result.TimedOut || result.OK() {
		t.Errorf("want a timed-out result, got %+v", result)
	}
}

func TestNonZeroExitIsCapturedWithItsCode(t *testing.T) {
	result := Run([]string{"sh", "-c", "exit 3"}, Options{})
	if result.Code != 3 {
		t.Errorf("want exit 3, got %d", result.Code)
	}
	if result.OK() {
		t.Error("a non-zero exit must not report success")
	}
}

func TestPagerAndColourAreNeutralised(t *testing.T) {
	// A pager left set would block on a terminal; colour would corrupt parsing.
	t.Setenv("GIT_PAGER", "less")
	t.Setenv("NO_COLOR", "")
	result := Run([]string{"sh", "-c", "echo $GIT_PAGER:$NO_COLOR"}, Options{})
	if got := strings.TrimSpace(result.Stdout); got != "cat:1" {
		t.Errorf("environment was not neutralised: %q", got)
	}
}

func TestLinesSkipsBlanksAndTrimsTrailingSpace(t *testing.T) {
	result := Result{Stdout: "one   \n\n  \ntwo\n"}
	lines := result.Lines()
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Errorf("got %q", lines)
	}
	if result.FirstLine() != "one" {
		t.Errorf("FirstLine = %q", result.FirstLine())
	}
	if (Result{}).FirstLine() != "" {
		t.Error("FirstLine of empty output must be empty")
	}
}

func TestGitVersionReportsEmptyWhenGitCannotRun(t *testing.T) {
	original := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	defer t.Setenv("PATH", original)
	if got := GitVersion(); got != "" {
		t.Errorf("want an empty version when git is absent, got %q", got)
	}
}
