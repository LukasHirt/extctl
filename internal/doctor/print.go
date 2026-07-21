package doctor

import (
	"fmt"
	"io"
)

// Print renders a Report as a human-readable text report grouped by section,
// ending in a pass/fail summary line.
func Print(r *Report, w io.Writer) {
	var errCount, warnCount int
	var lastSection Section
	for _, f := range r.Findings {
		if f.Section != lastSection {
			_, _ = fmt.Fprintf(w, "\n%s\n", f.Section)
			lastSection = f.Section
		}
		_, _ = fmt.Fprintf(w, "  [%-5s] %s\n", f.Severity, f.Message)
		switch f.Severity {
		case ERROR:
			errCount++
		case WARN:
			warnCount++
		}
	}
	_, _ = fmt.Fprintln(w)
	if errCount > 0 {
		_, _ = fmt.Fprintf(w, "FAIL — %d error(s), %d warning(s)\n", errCount, warnCount)
	} else {
		_, _ = fmt.Fprintf(w, "PASS — %d warning(s)\n", warnCount)
	}
}
