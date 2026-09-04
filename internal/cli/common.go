// Package cli implements every command surface.
//
// Every handler has the shape RunX(arguments []string, stdout io.Writer) error,
// so output is injected and the whole CLI is testable without touching globals.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/gonaas/devctl/internal/adapters"
	"github.com/gonaas/devctl/internal/process"
	"github.com/gonaas/devctl/internal/registry"
	"github.com/gonaas/devctl/internal/survey"
)

// scanFlags are the options every scanning command shares.
type scanFlags struct {
	roots        multiFlag
	base         string
	product      string
	json         bool
	size         bool
	wantConflict bool
	apply        bool
}

type multiFlag []string

func (m *multiFlag) String() string { return fmt.Sprint(*m) }

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func newFlagSet(name string, stdout io.Writer) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stdout)
	return set
}

func (s *scanFlags) bind(set *flag.FlagSet) {
	set.Var(&s.roots, "root", "directory to scan; repeat for several")
	set.StringVar(&s.base, "base", "", "base ref to compare against; overrides detection")
	set.StringVar(&s.product, "product", "", "restrict to one product")
	set.BoolVar(&s.json, "json", false, "emit a machine-readable document")
}

// requireGit fails loudly when git cannot run, rather than reporting an empty
// world. Every enumeration depends on git, so silence here is the dangerous
// direction.
func requireGit() error {
	if process.GitVersion() == "" {
		return fmt.Errorf("git is not usable on this PATH; every check in this tool depends on it, " +
			"and an empty result would be indistinguishable from a clean one")
	}
	return nil
}

func homeDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// loadRegistry reads the effective registry for this run.
func loadRegistry() (registry.Registry, error) {
	return registry.Load("", homeDirectory())
}

// prepare loads the registry, builds adapters and runs one inspection pass.
func prepare(flags *scanFlags) (registry.Registry, survey.Survey, error) {
	if err := requireGit(); err != nil {
		return registry.Registry{}, survey.Survey{}, err
	}
	reg, err := loadRegistry()
	if err != nil {
		return registry.Registry{}, survey.Survey{}, err
	}
	set := adapters.Build(reg)
	result := survey.Run(reg, set, survey.Options{
		Roots:        flags.roots,
		BaseOverride: flags.base,
		WantConflict: flags.wantConflict,
		WantSize:     flags.size,
		Product:      flags.product,
	})
	return reg, result, nil
}

func usage(stdout io.Writer, lines ...string) error {
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return fmt.Errorf("missing or unknown subcommand")
}
