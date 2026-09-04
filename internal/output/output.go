// Package output renders reports as plain text tables or a versioned JSON envelope.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	schemaPrefix  = "devctl"
	schemaVersion = "1"
)

// Envelope wraps a payload in a versioned, tool-neutral schema.
//
// The schema is the contract any consumer reads. It names no vendor, so changing
// the surrounding stack never changes what a consumer has to parse.
func Envelope(kind string, payload map[string]any) map[string]any {
	wrapped := map[string]any{
		"schema":       fmt.Sprintf("%s/%s/%s", schemaPrefix, kind, schemaVersion),
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}
	for key, value := range payload {
		wrapped[key] = value
	}
	return wrapped
}

// EmitJSON prints one JSON document for machine consumption.
func EmitJSON(stdout io.Writer, kind string, payload map[string]any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(Envelope(kind, payload))
}

// Table renders a left-aligned table sized to its content.
//
// Deliberately without colour or box drawing: this output is read as often by a
// pipeline as by a person.
func Table(headers []string, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	widths := make([]int, len(headers))
	for index, header := range headers {
		widths[index] = len(header)
	}
	for _, row := range rows {
		for index, cell := range row {
			if index < len(widths) && len(cell) > widths[index] {
				widths[index] = len(cell)
			}
		}
	}

	var builder strings.Builder
	writeRow := func(cells []string) {
		parts := make([]string, 0, len(cells))
		for index, cell := range cells {
			width := 0
			if index < len(widths) {
				width = widths[index]
			}
			parts = append(parts, cell+strings.Repeat(" ", max(0, width-len(cell))))
		}
		builder.WriteString(strings.TrimRight(strings.Join(parts, "  "), " "))
		builder.WriteString("\n")
	}

	writeRow(headers)
	separators := make([]string, len(widths))
	for index, width := range widths {
		separators[index] = strings.Repeat("-", width)
	}
	writeRow(separators)
	for _, row := range rows {
		writeRow(row)
	}
	return strings.TrimRight(builder.String(), "\n")
}

// HumanSize renders a byte count compactly, or a dash when it is unknown.
func HumanSize(value int64) string {
	if value < 0 {
		return "-"
	}
	size := float64(value)
	units := []string{"B", "K", "M", "G", "T"}
	for index, unit := range units {
		if size < 1024 || index == len(units)-1 {
			if unit == "B" {
				return fmt.Sprintf("%.0fB", size)
			}
			return fmt.Sprintf("%.1f%s", size, unit)
		}
		size /= 1024
	}
	return fmt.Sprintf("%.1fT", size)
}

// Age renders a unix timestamp as a coarse age, or a dash when unset.
func Age(timestamp int64) string {
	if timestamp == 0 {
		return "-"
	}
	days := int(time.Since(time.Unix(timestamp, 0)).Hours() / 24)
	switch {
	case days < 1:
		return "today"
	case days < 30:
		return fmt.Sprintf("%dd", days)
	case days < 365:
		return fmt.Sprintf("%dmo", days/30)
	default:
		return fmt.Sprintf("%dy", days/365)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
