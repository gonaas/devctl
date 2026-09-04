// Package config reports declared tool health and drift between skill copies.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gonaas/devctl/internal/process"
	"github.com/gonaas/devctl/internal/registry"
)

const skillMarker = "SKILL.md"

// Drift states a skill can be in relative to an agent runtime.
const (
	Identical      = "identical"
	RepoOnly       = "repo-only"
	InstalledOnly  = "installed-only"
	RepoNewer      = "repo-newer"
	InstalledNewer = "installed-newer"
)

// ToolHealth is one declared tool's installation and health result.
type ToolHealth struct {
	Name      string `json:"name"`
	Binary    string `json:"binary"`
	Installed bool   `json:"installed"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
}

// SkillDrift is how one skill compares between two locations.
type SkillDrift struct {
	Skill  string `json:"skill"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// DriftSummary aggregates drift for one agent runtime.
type DriftSummary struct {
	Agent   string       `json:"agent"`
	Root    string       `json:"root"`
	Present bool         `json:"present"`
	Entries []SkillDrift `json:"entries"`
}

// Counts returns how many skills fall into each drift state.
func (d DriftSummary) Counts() map[string]int {
	totals := map[string]int{}
	for _, entry := range d.Entries {
		totals[entry.State]++
	}
	return totals
}

func readJSONPath(payload any, dotted string) (string, bool) {
	current := payload
	for _, segment := range strings.Split(dotted, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		value, present := object[segment]
		if !present {
			return "", false
		}
		current = value
	}
	switch typed := current.(type) {
	case string:
		return typed, true
	case nil:
		return "", false
	default:
		return fmt.Sprint(typed), true
	}
}

// ProbeTool runs one tool's declared health probe and normalises the outcome.
//
// Tools that publish structured output are read through their declared status
// path. Tools that publish none are judged by exit status alone, which is stated
// rather than guessed at.
func ProbeTool(definition registry.Tool) ToolHealth {
	if !process.Available(definition.Binary) {
		return ToolHealth{definition.Name, definition.Binary, false, "absent", "not on PATH"}
	}

	result := process.Run(
		append([]string{definition.Binary}, definition.Health...),
		process.Options{Timeout: 60 * time.Second},
	)

	if definition.HealthFormat == registry.HealthExit {
		if result.OK() {
			return ToolHealth{definition.Name, definition.Binary, true, "ok", ""}
		}
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit %d", result.Code)
		}
		return ToolHealth{definition.Name, definition.Binary, true, "degraded", strings.SplitN(detail, "\n", 2)[0]}
	}

	var payload any
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return ToolHealth{definition.Name, definition.Binary, true, "unreadable", "health output is not valid JSON"}
	}
	value, found := readJSONPath(payload, definition.StatusPath)
	if !found {
		return ToolHealth{definition.Name, definition.Binary, true, "unreadable",
			fmt.Sprintf("no value at %q", definition.StatusPath)}
	}
	for _, ok := range definition.OkValues {
		if strings.EqualFold(ok, value) {
			return ToolHealth{definition.Name, definition.Binary, true, "ok", ""}
		}
	}
	return ToolHealth{definition.Name, definition.Binary, true, value,
		fmt.Sprintf("%s = %s", definition.StatusPath, value)}
}

// ToolHealthAll probes every declared tool, in declaration order.
func ToolHealthAll(reg registry.Registry) []ToolHealth {
	results := make([]ToolHealth, 0, len(reg.Tools))
	for _, tool := range reg.Tools {
		results = append(results, ProbeTool(tool))
	}
	return results
}

func skillDirectories(root string) map[string]string {
	found := map[string]string{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return found
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if info, statErr := os.Stat(filepath.Join(path, skillMarker)); statErr == nil && !info.IsDir() {
			found[entry.Name()] = path
		}
	}
	return found
}

// directoriesIdentical compares by content, never by timestamp.
//
// A copy-based installer resets mtimes without changing a single byte, so
// timestamps report drift that does not exist.
func directoriesIdentical(left, right string) bool {
	leftFiles, leftErr := fileMap(left)
	rightFiles, rightErr := fileMap(right)
	if leftErr != nil || rightErr != nil || len(leftFiles) != len(rightFiles) {
		return false
	}
	for name, leftPath := range leftFiles {
		rightPath, present := rightFiles[name]
		if !present {
			return false
		}
		leftContent, err := os.ReadFile(leftPath)
		if err != nil {
			return false
		}
		rightContent, err := os.ReadFile(rightPath)
		if err != nil {
			return false
		}
		if !bytes.Equal(leftContent, rightContent) {
			return false
		}
	}
	return true
}

func fileMap(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			files[relative] = path
		}
		return nil
	})
	return files, err
}

func newestModification(root string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, statErr := entry.Info(); statErr == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
		return nil
	})
	return newest
}

// SkillDriftAll compares repository skills against every declared skills root.
//
// Read-only by design. Resolving drift means choosing which copy wins, and that
// choice can destroy work, so it stays a separate deliberate action.
func SkillDriftAll(reg registry.Registry, repositorySkills string) []DriftSummary {
	source := skillDirectories(repositorySkills)
	summaries := make([]DriftSummary, 0, len(reg.Agents))

	for _, agent := range reg.Agents {
		summary := DriftSummary{Agent: agent.Name, Root: agent.SkillsRoot, Entries: []SkillDrift{}}
		if info, err := os.Stat(agent.SkillsRoot); err != nil || !info.IsDir() {
			summaries = append(summaries, summary)
			continue
		}
		summary.Present = true

		installed := skillDirectories(agent.SkillsRoot)
		names := map[string]bool{}
		for name := range source {
			names[name] = true
		}
		for name := range installed {
			names[name] = true
		}
		ordered := make([]string, 0, len(names))
		for name := range names {
			ordered = append(ordered, name)
		}
		sort.Strings(ordered)

		for _, name := range ordered {
			sourcePath, inRepository := source[name]
			installedPath, inAgent := installed[name]
			switch {
			case inRepository && !inAgent:
				summary.Entries = append(summary.Entries, SkillDrift{Skill: name, State: RepoOnly})
			case inAgent && !inRepository:
				detail := ""
				if info, err := os.Lstat(installedPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
					detail = "symlink"
				}
				summary.Entries = append(summary.Entries, SkillDrift{Skill: name, State: InstalledOnly, Detail: detail})
			case directoriesIdentical(sourcePath, installedPath):
				summary.Entries = append(summary.Entries, SkillDrift{Skill: name, State: Identical})
			default:
				state := RepoNewer
				if newestModification(installedPath).After(newestModification(sourcePath)) {
					state = InstalledNewer
				}
				summary.Entries = append(summary.Entries, SkillDrift{Skill: name, State: state})
			}
		}
		summaries = append(summaries, summary)
	}
	return summaries
}
