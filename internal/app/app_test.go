package app

import (
	"strings"
	"testing"
)

func TestVersionIsReportedWithoutAPrefix(t *testing.T) {
	cases := map[string]string{
		"v2.5.0": "2.5.0",
		"2.5.0":  "2.5.0",
	}
	for input, want := range cases {
		if got := ResolveVersion(input); got != want {
			t.Errorf("ResolveVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestVersionFallsBackWhenUnstamped(t *testing.T) {
	original := buildInfoReader
	defer func() { buildInfoReader = original }()
	buildInfoReader = func() (*debugBuildInfo, bool) { return nil, false }
	if got := ResolveVersion("dev"); got != "dev" {
		t.Errorf("want dev, got %q", got)
	}
	if got := ResolveVersion(""); got != "dev" {
		t.Errorf("want dev for an empty stamp, got %q", got)
	}
}

func TestDispatchRoutesAndReportsUnknownCommands(t *testing.T) {
	Version = "9.9.9"

	var out strings.Builder
	if err := RunArgs([]string{"version"}, &out); err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(out.String()) != "devctl 9.9.9" {
		t.Errorf("version output: %q", out.String())
	}

	out.Reset()
	if err := RunArgs(nil, &out); err != nil {
		t.Fatalf("bare invocation must print help, not fail: %v", err)
	}
	if !strings.Contains(out.String(), "USAGE") {
		t.Error("bare invocation must print help")
	}

	out.Reset()
	err := RunArgs([]string{"teleport"}, &out)
	if err == nil {
		t.Fatal("an unknown command must be an error")
	}
	if !strings.Contains(err.Error(), "teleport") {
		t.Errorf("the error must name the command: %v", err)
	}
	if !strings.Contains(out.String(), "USAGE") {
		t.Error("an unknown command must still show help")
	}
}
