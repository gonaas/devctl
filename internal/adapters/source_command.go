package adapters

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gonaas/devctl/internal/process"
	"github.com/gonaas/devctl/internal/registry"
)

// commandSource is a generic source for tools that do expose a stable machine
// contract.
//
// Nothing in the shipped registry uses this yet. It exists so that a tool which
// grows a real JSON mode becomes a registry entry rather than a code change.
type commandSource struct {
	definition registry.ProjectSource
	once       sync.Once
	cached     Availability
}

func newCommandSource(definition registry.ProjectSource) ProjectSource {
	return &commandSource{definition: definition}
}

func (c *commandSource) Name() string { return c.definition.Name }

func (c *commandSource) Available() Availability {
	c.once.Do(func() {
		switch {
		case c.definition.Binary == "":
			c.cached = Availability{Reason: "no binary declared"}
		case !process.Available(c.definition.Binary):
			c.cached = Availability{Reason: c.definition.Binary + " not on PATH"}
		case c.definition.OutputFormat != "json":
			format := c.definition.OutputFormat
			if format == "" {
				format = "(none)"
			}
			c.cached = Availability{Reason: "unsupported output_format: " + format}
		default:
			c.cached = Availability{Usable: true}
		}
	})
	return c.cached
}

func (c *commandSource) Projects() []ProjectRecord {
	if !c.Available().Usable {
		return nil
	}
	result := process.Run(
		append([]string{c.definition.Binary}, c.definition.Arguments...),
		process.Options{Timeout: 30 * time.Second},
	)
	if !result.OK() {
		return nil
	}
	var payload []struct {
		Project    string `json:"project"`
		Directory  string `json:"directory"`
		Weight     int    `json:"weight"`
		LastActive string `json:"last_active"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return nil
	}
	var records []ProjectRecord
	for _, entry := range payload {
		if entry.Project == "" || entry.Directory == "" {
			continue
		}
		records = append(records, ProjectRecord{
			Project:    entry.Project,
			Directory:  entry.Directory,
			Weight:     entry.Weight,
			LastActive: entry.LastActive,
			Source:     c.definition.Name,
		})
	}
	return records
}
