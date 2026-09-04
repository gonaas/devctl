package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RunInstall links this binary onto PATH. An occupied destination is only
// replaced when asked, because the path is shared with everything else on PATH.
func RunInstall(arguments []string, stdout io.Writer) error {
	set := newFlagSet("install", stdout)
	apply := set.Bool("apply", false, "create the link")
	replace := set.Bool("replace", false, "replace an existing destination")
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

	// The destination can already be this very binary, which is what happens
	// when it was built straight onto PATH. Replacing it would leave a symlink
	// pointing at itself, so there is nothing to do and nothing to destroy.
	if resolved, evalErr := filepath.EvalSymlinks(destination); evalErr == nil && resolved == source {
		fmt.Fprintf(stdout, "Already installed: %s is this binary\n", destination)
		return nil
	}

	occupant := ""
	if info, statErr := os.Lstat(destination); statErr == nil {
		// A directory is never something this command meant to stand in for, so
		// --replace does not reach it.
		if info.IsDir() {
			return fmt.Errorf("%s is a directory; not replaced", destination)
		}
		occupant = describeOccupant(destination, info)
		if !*replace {
			return fmt.Errorf("%s already exists (%s); re-run with --replace to replace it", destination, occupant)
		}
	}

	if !*apply {
		if occupant == "" {
			fmt.Fprintf(stdout, "Dry run: would link %s -> %s. Re-run with --apply.\n", destination, source)
		} else {
			fmt.Fprintf(stdout, "Dry run: would replace %s (%s) -> %s. Re-run with --apply.\n", destination, occupant, source)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if occupant == "" {
		if err := os.Symlink(source, destination); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Linked %s -> %s\n", destination, source)
		return nil
	}

	// Replace through a rename so the destination is never briefly absent:
	// anything resolving it on PATH sees the old link or the new one, never a
	// gap. Symlink cannot overwrite, which is why the link is built beside it.
	staging := destination + ".devctl-incoming"
	if err := os.Remove(staging); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(source, staging); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		os.Remove(staging)
		return err
	}
	fmt.Fprintf(stdout, "Replaced %s (%s) -> %s\n", destination, occupant, source)
	return nil
}

// describeOccupant names what is already sitting on the destination, so the
// refusal and the dry run distinguish a stale link from a real binary.
func describeOccupant(destination string, info os.FileInfo) string {
	if info.Mode()&os.ModeSymlink == 0 {
		return "regular file"
	}
	existing, err := os.Readlink(destination)
	if err != nil {
		return "symlink"
	}
	return "symlink to " + existing
}
