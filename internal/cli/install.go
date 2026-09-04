package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RunInstall links this binary onto PATH without replacing anything.
func RunInstall(arguments []string, stdout io.Writer) error {
	set := newFlagSet("install", stdout)
	apply := set.Bool("apply", false, "create the link")
	target := set.String("target", "", "destination path; defaults to ~/.local/bin/devctl")
	if err := set.Parse(arguments); err != nil {
		return err
	}

	destination := *target
	if destination == "" {
		destination = filepath.Join(homeDirectory(), ".local", "bin", "devctl")
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}

	if existing, readErr := os.Readlink(destination); readErr == nil {
		if resolved, evalErr := filepath.EvalSymlinks(existing); evalErr == nil && resolved == source {
			fmt.Fprintf(stdout, "Already linked: %s\n", destination)
			return nil
		}
	}
	if _, statErr := os.Lstat(destination); statErr == nil {
		return fmt.Errorf("%s already exists; not replaced", destination)
	}
	if !*apply {
		fmt.Fprintf(stdout, "Dry run: would link %s -> %s. Re-run with --apply.\n", destination, source)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(source, destination); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Linked %s -> %s\n", destination, source)
	return nil
}
