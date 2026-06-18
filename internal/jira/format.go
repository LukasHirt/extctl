package jira

import (
	"fmt"
	"strings"

	"github.com/LukasHirt/extctl/internal/claude"
)

// FormatIssueBody renders a ParsedCandidate into a Jira issue description.
// Uses Jira wiki markup (Server/DC compatible).
func FormatIssueBody(c claude.ParsedCandidate, appearances int, origin string) string {
	var b strings.Builder

	section := func(heading, content string) {
		if content == "" {
			return
		}
		fmt.Fprintf(&b, "h3. %s\n\n%s\n\n", heading, content)
	}

	section("Problem", c.Problem)

	fmt.Fprintf(&b, "h3. Extension Point\n\n{{%s}}\n\n", c.ExtensionPoint)

	section("Sketch", c.Sketch)
	section("Why Now", c.WhyNow)
	section("Evidence", c.Evidence)

	fmt.Fprintf(&b, "h3. Metadata\n\n")
	fmt.Fprintf(&b, "* *Effort:* %s\n", c.Effort)
	fmt.Fprintf(&b, "* *Appearances:* %d\n", appearances)
	fmt.Fprintf(&b, "* *Origin:* %s\n", origin)
	fmt.Fprintf(&b, "* *ID:* {{%s}}\n", c.ID)

	return b.String()
}

// IssueSummary returns the standard issue summary line for a candidate.
func IssueSummary(title string) string {
	return fmt.Sprintf("[ext-candidate] %s", title)
}
