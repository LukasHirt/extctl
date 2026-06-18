package claude

import (
	"fmt"
	"strings"
)

// ParsedCandidate holds the fields extracted from a ## CANDIDATE block.
type ParsedCandidate struct {
	ID             string
	Title          string
	Problem        string
	ExtensionPoint string
	Sketch         string
	WhyNow         string
	Effort         string
	Evidence       string
	Raw            string // the full block, preserved for the Jira body
}

// ParseCandidates extracts all ## CANDIDATE blocks from the model output text.
// It is strict: any block missing id, title, extension_point, sketch, or effort
// is returned as an error so the caller can decide whether to abort or warn.
func ParseCandidates(text string) ([]ParsedCandidate, error) {
	const marker = "## CANDIDATE"
	var candidates []ParsedCandidate

	// Split on the marker so each chunk is one candidate block.
	parts := strings.Split(text, marker)
	for _, part := range parts[1:] { // parts[0] is everything before the first marker
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		raw := marker + "\n" + part
		c, err := parseBlock(part)
		if err != nil {
			return nil, fmt.Errorf("malformed candidate block: %w\nraw block:\n%s", err, raw)
		}
		c.Raw = raw
		candidates = append(candidates, c)
	}
	return candidates, nil
}

// parseBlock parses a single candidate block (the text after "## CANDIDATE\n").
// Fields follow YAML-block-scalar style:
//
//	id: some-id
//	title: Some Title
//	problem: |
//	  Multi-line
//	  text here
func parseBlock(block string) (ParsedCandidate, error) {
	var c ParsedCandidate
	lines := strings.Split(block, "\n")

	type field struct {
		key   *string
		names []string
	}
	fields := []field{
		{&c.ID, []string{"id"}},
		{&c.Title, []string{"title"}},
		{&c.Problem, []string{"problem"}},
		{&c.ExtensionPoint, []string{"extension_point", "extension_points"}},
		{&c.Sketch, []string{"sketch"}},
		{&c.WhyNow, []string{"why_now"}},
		{&c.Effort, []string{"effort"}},
		{&c.Evidence, []string{"evidence"}},
	}

	// Build a lookup: lowercase key name → pointer
	lookup := map[string]*string{}
	for _, f := range fields {
		for _, name := range f.names {
			lookup[name] = f.key
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Skip blank lines and the CANDIDATE header itself if still present.
		if trimmed == "" || trimmed == "## CANDIDATE" {
			i++
			continue
		}

		// Key: value  or  key: |
		colonIdx := strings.Index(trimmed, ":")
		if colonIdx < 0 {
			i++
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:colonIdx]))
		rest := strings.TrimSpace(trimmed[colonIdx+1:])

		target, ok := lookup[key]
		if !ok {
			i++
			continue
		}

		if rest == "|" {
			// Block scalar: collect indented lines that follow.
			i++
			var buf []string
			for i < len(lines) {
				l := lines[i]
				if strings.TrimSpace(l) == "" {
					// Blank line may be part of the block or end it.
					// Peek ahead: if the next non-blank line is less indented, we're done.
					buf = append(buf, "")
					i++
					continue
				}
				// Indented continuation?
				if len(l) > 0 && (l[0] == ' ' || l[0] == '\t') {
					buf = append(buf, strings.TrimSpace(l))
					i++
				} else {
					break
				}
			}
			// Trim trailing blank lines from buf.
			for len(buf) > 0 && buf[len(buf)-1] == "" {
				buf = buf[:len(buf)-1]
			}
			*target = strings.Join(buf, "\n")
		} else {
			// Inline value.
			*target = rest
			i++
		}
	}

	// Validate required fields.
	required := []struct {
		name  string
		value string
	}{
		{"id", c.ID},
		{"title", c.Title},
		{"extension_point", c.ExtensionPoint},
		{"sketch", c.Sketch},
		{"effort", c.Effort},
	}
	for _, r := range required {
		if r.value == "" {
			return ParsedCandidate{}, fmt.Errorf("missing required field %q", r.name)
		}
	}

	// Normalise effort to uppercase S/M/L.
	c.Effort = strings.ToUpper(strings.TrimSpace(c.Effort))
	if c.Effort != "S" && c.Effort != "M" && c.Effort != "L" {
		return ParsedCandidate{}, fmt.Errorf("effort must be S, M, or L, got %q", c.Effort)
	}

	return c, nil
}
