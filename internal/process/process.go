// Package process runs external commands with list arguments and a timeout.
//
// Nothing here builds a shell command line, so caller-controlled values can
// never be interpreted as shell syntax.
package process

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds every command. Go's own context handles this, which
// matters because macOS ships no timeout(1).
const DefaultTimeout = 20 * time.Second

// neutralEnvironment removes pagers and colour, which make output unparseable
// and can block waiting on a terminal.
var neutralEnvironment = map[string]string{
	"GIT_PAGER":           "cat",
	"PAGER":               "cat",
	"NO_COLOR":            "1",
	"CLICOLOR":            "0",
	"TERM":                "dumb",
	"GIT_TERMINAL_PROMPT": "0",
	"GIT_OPTIONAL_LOCKS":  "0",
}

// Result is the outcome of one command, including failures and timeouts.
type Result struct {
	Arguments []string
	Code      int
	Stdout    string
	Stderr    string
	TimedOut  bool
}

// OK reports whether the command completed with a zero exit status.
func (r Result) OK() bool { return r.Code == 0 && !r.TimedOut }

// Lines returns non-empty stdout lines with trailing whitespace removed.
func (r Result) Lines() []string {
	var out []string
	for _, line := range strings.Split(r.Stdout, "\n") {
		trimmed := strings.TrimRight(line, "\r\n\t ")
		if strings.TrimSpace(trimmed) != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// FirstLine returns the first non-empty stdout line, or an empty string.
func (r Result) FirstLine() string {
	lines := r.Lines()
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

// Options tunes a single invocation.
type Options struct {
	Dir     string
	Timeout time.Duration
}

// lookPath is a seam so tests can pretend a binary is missing.
var lookPath = exec.LookPath

// Available reports whether a binary can be found on PATH.
func Available(binary string) bool {
	_, err := lookPath(binary)
	return err == nil
}

// Run executes a command and captures its output. A missing binary or a timeout
// becomes a Result rather than an error, so callers can degrade instead of
// unwinding.
func Run(arguments []string, options Options) Result {
	if len(arguments) == 0 {
		return Result{Code: 2, Stderr: "refusing to run an empty command"}
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
	command.Dir = options.Dir
	command.Env = environment()

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()

	result := Result{
		Arguments: arguments,
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.Code = 124
		result.TimedOut = true
		if result.Stderr == "" {
			result.Stderr = arguments[0] + ": timed out"
		}
	case err == nil:
		result.Code = 0
	default:
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.Code = exitError.ExitCode()
		} else {
			result.Code = 127
			if result.Stderr == "" {
				result.Stderr = arguments[0] + ": " + err.Error()
			}
		}
	}
	return result
}

func environment() []string {
	base := os.Environ()
	filtered := base[:0:0]
	for _, entry := range base {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, override := neutralEnvironment[name]; override {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	for name, value := range neutralEnvironment {
		filtered = append(filtered, name+"="+value)
	}
	return filtered
}

// Git runs git by name so PATH wrappers and their environment are honoured.
func Git(arguments []string, options Options) Result {
	return Run(append([]string{"git"}, arguments...), options)
}

// GitVersion returns git's version string, or an empty string when git cannot
// run. Worth checking once at startup: every enumeration goes through git, and
// a git that cannot run makes an empty result look identical to a clean one.
func GitVersion() string {
	result := Git([]string{"--version"}, Options{Timeout: 10 * time.Second})
	if !result.OK() {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}
