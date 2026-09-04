package adapters

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gonaas/devctl/internal/process"
)

const (
	projectLabel    = "com.docker.compose.project"
	workingDirLabel = "com.docker.compose.project.working_dir"
)

// composeRuntime reports named volumes and stacks, read-only, and proposes
// nothing.
//
// Removing a worktree does not remove the services it started. Named volumes in
// particular outlive the checkout and are usually larger than it.
type composeRuntime struct {
	once   sync.Once
	cached Availability
}

// NewComposeRuntime returns the one service-runtime adapter shipped today.
func NewComposeRuntime() Runtime { return &composeRuntime{} }

func (c *composeRuntime) Name() string { return "compose" }

func (c *composeRuntime) Available() Availability {
	c.once.Do(func() {
		if !process.Available("docker") {
			c.cached = Availability{Reason: "docker not on PATH"}
			return
		}
		probe := process.Run(
			[]string{"docker", "version", "--format", "{{.Server.Version}}"},
			process.Options{Timeout: 15 * time.Second},
		)
		if probe.OK() {
			c.cached = Availability{Usable: true}
			return
		}
		c.cached = Availability{Reason: "docker daemon unreachable"}
	})
	return c.cached
}

func parseLabels(raw string) map[string]string {
	labels := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		name, value, found := strings.Cut(pair, "=")
		if found {
			labels[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return labels
}

func (c *composeRuntime) stackDirectories() map[string]string {
	result := process.Run(
		[]string{"docker", "compose", "ls", "--all", "--format", "json"},
		process.Options{Timeout: 30 * time.Second},
	)
	directories := map[string]string{}
	if !result.OK() {
		return directories
	}
	var payload []struct {
		Name        string `json:"Name"`
		ConfigFiles string `json:"ConfigFiles"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return directories
	}
	for _, entry := range payload {
		if entry.Name == "" || entry.ConfigFiles == "" {
			continue
		}
		first := strings.TrimSpace(strings.Split(entry.ConfigFiles, ",")[0])
		if first != "" {
			directories[entry.Name] = filepath.Dir(first)
		}
	}
	return directories
}

func (c *composeRuntime) Resources() []RuntimeResource {
	if !c.Available().Usable {
		return nil
	}
	listing := process.Run(
		[]string{"docker", "volume", "ls", "--format", "{{json .}}"},
		process.Options{Timeout: 30 * time.Second},
	)
	if !listing.OK() {
		return nil
	}

	directories := c.stackDirectories()
	var resources []RuntimeResource
	for _, line := range listing.Lines() {
		var entry struct {
			Name   string `json:"Name"`
			Labels string `json:"Labels"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Name == "" {
			continue
		}
		labels := parseLabels(entry.Labels)
		stack := labels[projectLabel]
		bound := labels[workingDirLabel]
		if bound == "" {
			bound = directories[stack]
		}
		resources = append(resources, RuntimeResource{
			Name:      entry.Name,
			Kind:      "volume",
			BoundPath: bound,
			Detail:    stack,
		})
	}
	return resources
}
