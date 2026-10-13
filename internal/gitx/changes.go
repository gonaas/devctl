package gitx

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FileStatus is how one path changed inside a commit range.
type FileStatus string

// The status vocabulary is deliberately smaller than git's: these are the shapes a
// reader has to distinguish, so an unrecognised code is passed through rather than
// folded into a status it might not be.
const (
	StatusAdded      FileStatus = "added"
	StatusModified   FileStatus = "modified"
	StatusDeleted    FileStatus = "deleted"
	StatusRenamed    FileStatus = "renamed"
	StatusCopied     FileStatus = "copied"
	StatusTypeChange FileStatus = "type-changed"
)

const (
	nulSeparator       = "\x00"
	binaryCountMarker  = "-"
	rangeCommandBudget = 60 * time.Second
)

// FileChange is one path changed in a commit range.
//
// Added and Deleted are meaningful only when CountsUnavailable is false: git
// reports a binary file as "-", and an unavailable count must never reach a reader
// as a real zero.
type FileChange struct {
	Path              string
	PreviousPath      string
	Status            FileStatus
	Binary            bool
	CountsUnavailable bool
	Added             int
	Deleted           int
}

// MergeBase returns the commit where a reference and HEAD diverged.
//
// This is the range a pull request compares. Plain base..HEAD would also report
// commits that landed on the base after the fork, which is why the commit is
// resolved explicitly instead of being spelled with three dots: the caller can
// then report exactly what was compared.
func MergeBase(directory, reference string) (string, error) {
	if strings.TrimSpace(reference) == "" {
		return "", fmt.Errorf("no reference was given to compare against")
	}
	result := gitAt(directory, 30*time.Second, "merge-base", reference, "HEAD")
	if !result.OK() {
		return "", fmt.Errorf("no merge base between %s and HEAD: %s", reference, strings.TrimSpace(result.Stderr))
	}
	base := result.FirstLine()
	if base == "" {
		return "", fmt.Errorf("no merge base between %s and HEAD", reference)
	}
	return base, nil
}

// ChangedFiles lists every path changed between two commits, with its status and
// its line counts. Renames stay one entry, because a rename reported as a deletion
// plus an addition is the most misleading thing a range summary can do.
func ChangedFiles(directory, from, to string) ([]FileChange, error) {
	names := gitAt(directory, rangeCommandBudget, diffArguments(from, to, "--name-status", "-z")...)
	if !names.OK() {
		return nil, fmt.Errorf("listing changed paths between %s and %s failed: %s", from, to, strings.TrimSpace(names.Stderr))
	}
	counts := gitAt(directory, rangeCommandBudget, diffArguments(from, to, "--numstat", "-z")...)
	if !counts.OK() {
		return nil, fmt.Errorf("counting changes between %s and %s failed: %s", from, to, strings.TrimSpace(counts.Stderr))
	}
	return joinChanges(parseNameStatus(names.Stdout), parseNumstat(counts.Stdout)), nil
}

// Patch returns the unified diff of a commit range.
func Patch(directory, from, to string) (string, error) {
	result := gitAt(directory, rangeCommandBudget, diffArguments(from, to)...)
	if !result.OK() {
		return "", fmt.Errorf("diffing %s and %s failed: %s", from, to, strings.TrimSpace(result.Stderr))
	}
	return result.Stdout, nil
}

// diffArguments are the determinism guarantees every range diff in this file
// needs.
//
// An external diff driver or a textconv filter configured by the user would change
// the output format this package parses, and rename detection is forced because
// the reported shape depends on it: with renames off, a move silently becomes a
// deletion plus an addition.
func diffArguments(from, to string, extra ...string) []string {
	arguments := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames"}
	arguments = append(arguments, extra...)
	return append(arguments, from, to)
}

type nameEntry struct {
	code string
	old  string
	path string
}

type countEntry struct {
	old     string
	path    string
	added   string
	deleted string
}

// parseNameStatus reads `git diff --name-status -z`: one status per entry, then the
// path, except for a rename or copy, which carries the old path before the new one.
func parseNameStatus(raw string) []nameEntry {
	fields := strings.Split(raw, nulSeparator)
	entries := make([]nameEntry, 0, len(fields)/2)
	for index := 0; index < len(fields); {
		code := fields[index]
		index++
		if code == "" {
			continue
		}
		if index >= len(fields) {
			break
		}
		if strings.HasPrefix(code, "R") || strings.HasPrefix(code, "C") {
			if index+1 >= len(fields) {
				break
			}
			entries = append(entries, nameEntry{code: code, old: fields[index], path: fields[index+1]})
			index += 2
			continue
		}
		entries = append(entries, nameEntry{code: code, path: fields[index]})
		index++
	}
	return entries
}

// parseNumstat reads `git diff --numstat -z`: counts and path separated by tabs,
// except for a rename or copy, which leaves the path empty and then carries the old
// path before the new one.
func parseNumstat(raw string) []countEntry {
	fields := strings.Split(raw, nulSeparator)
	entries := make([]countEntry, 0, len(fields)/2)
	for index := 0; index < len(fields); {
		entry := fields[index]
		index++
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		count := countEntry{added: parts[0], deleted: parts[1], path: parts[2]}
		if parts[2] == "" {
			if index+1 >= len(fields) {
				break
			}
			count.old = fields[index]
			count.path = fields[index+1]
			index += 2
		}
		entries = append(entries, count)
	}
	return entries
}

// joinChanges correlates the two listings by path. The name listing is
// authoritative for membership and status; the count listing only supplies numbers.
func joinChanges(names []nameEntry, counts []countEntry) []FileChange {
	byKey := make(map[string]countEntry, len(counts))
	for _, count := range counts {
		byKey[entryKey(count.old, count.path)] = count
	}

	changes := make([]FileChange, 0, len(names))
	for _, name := range names {
		change := FileChange{
			Path:         name.path,
			PreviousPath: name.old,
			Status:       statusOf(name.code),
		}
		count, found := byKey[entryKey(name.old, name.path)]
		switch {
		case !found:
			change.CountsUnavailable = true
		case count.added == binaryCountMarker || count.deleted == binaryCountMarker:
			change.Binary = true
			change.CountsUnavailable = true
		default:
			change.Added = countOrZero(count.added)
			change.Deleted = countOrZero(count.deleted)
		}
		changes = append(changes, change)
	}
	return changes
}

func entryKey(old, path string) string {
	return old + nulSeparator + path
}

func statusOf(code string) FileStatus {
	switch {
	case code == "A":
		return StatusAdded
	case code == "M":
		return StatusModified
	case code == "D":
		return StatusDeleted
	case strings.HasPrefix(code, "R"):
		return StatusRenamed
	case strings.HasPrefix(code, "C"):
		return StatusCopied
	case code == "T":
		return StatusTypeChange
	default:
		return FileStatus(strings.ToLower(code))
	}
}

func countOrZero(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}
