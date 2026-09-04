package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeCarriesAVersionedNeutralSchema(t *testing.T) {
	wrapped := Envelope("worktrees", map[string]any{"count": 3})
	schema, _ := wrapped["schema"].(string)
	if schema != "devctl/worktrees/1" {
		t.Errorf("schema = %q", schema)
	}
	if wrapped["generated_at"] == "" {
		t.Error("the envelope must be timestamped")
	}
	if wrapped["count"] != 3 {
		t.Error("the payload must survive wrapping")
	}
}

func TestEmitJSONProducesOneParsableDocument(t *testing.T) {
	var out strings.Builder
	if err := EmitJSON(&out, "doctor", map[string]any{"items": []string{"a"}}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if decoded["schema"] != "devctl/doctor/1" {
		t.Errorf("schema = %v", decoded["schema"])
	}
}

func TestTableSizesColumnsToContentAndTrimsTrailingSpace(t *testing.T) {
	rendered := Table([]string{"A", "LONGHEADER"}, [][]string{
		{"a-very-long-cell", "x"},
		{"b", "y"},
	})
	lines := strings.Split(rendered, "\n")
	if len(lines) != 4 {
		t.Fatalf("want header, rule and two rows, got %d lines", len(lines))
	}
	for _, line := range lines {
		if strings.HasSuffix(line, " ") {
			t.Errorf("trailing whitespace in %q", line)
		}
	}
	if !strings.Contains(lines[0], "LONGHEADER") {
		t.Error("headers must survive")
	}
	if !strings.HasPrefix(lines[1], "----") {
		t.Errorf("second line must be the rule, got %q", lines[1])
	}
}

func TestTableOfNothingRendersNothing(t *testing.T) {
	if Table([]string{"A"}, nil) != "" {
		t.Error("an empty table must render as an empty string")
	}
}

func TestHumanSizeMarksTheUnknownRatherThanGuessing(t *testing.T) {
	if HumanSize(-1) != "-" {
		t.Errorf("unknown size = %q", HumanSize(-1))
	}
	if got := HumanSize(512); got != "512B" {
		t.Errorf("512 bytes = %q", got)
	}
	if got := HumanSize(1536); got != "1.5K" {
		t.Errorf("1536 bytes = %q", got)
	}
}

func TestAgeMarksTheUnknownRatherThanGuessing(t *testing.T) {
	if Age(0) != "-" {
		t.Errorf("unset timestamp = %q", Age(0))
	}
}
