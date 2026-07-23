// Package doctor checks the health of the local extctl installation: config
// validity, required secrets, external tooling, and referenced file paths.
// All checks are read-only — doctor never mutates config, state, or the
// target repo checkout.
package doctor

import "fmt"

// Severity classifies a Finding.
type Severity int

const (
	OK Severity = iota
	WARN
	ERROR
)

func (s Severity) String() string {
	switch s {
	case ERROR:
		return "ERROR"
	case WARN:
		return "WARN"
	default:
		return "OK"
	}
}

// Section groups related findings for display, in report order.
type Section string

const (
	SectionConfig              Section = "Config"
	SectionSecrets             Section = "Secrets"
	SectionTools               Section = "External tools"
	SectionPaths               Section = "Paths"
	SectionCheckout            Section = "Target repo checkout"
	SectionMarketplaceCheckout Section = "Marketplace repo checkout"
)

// Finding is a single check result.
type Finding struct {
	Section  Section
	Severity Severity
	Message  string
}

// Report is the full set of findings from a doctor run.
type Report struct {
	Findings []Finding
}

func (r *Report) add(s Section, sev Severity, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{Section: s, Severity: sev, Message: fmt.Sprintf(format, args...)})
}

// HasErrors reports whether any ERROR-level finding exists — the sole input
// to the process exit code.
func (r *Report) HasErrors() bool {
	for _, f := range r.Findings {
		if f.Severity == ERROR {
			return true
		}
	}
	return false
}

// Run performs every doctor check against the config file at cfgPath and the
// current working directory. Run never returns a Go error itself — a
// missing/invalid config file, a missing binary, etc. are all reported as
// Findings, so the caller always gets a complete, printable Report and reads
// the pass/fail verdict from Report.HasErrors(). This is why doctor must NOT
// go through rootCmd's PersistentPreRunE (which hard-fails on config.Load
// error) — doctor needs to run and report cleanly even when extctl.yaml is
// broken or missing.
func Run(cfgPath string) *Report {
	r := &Report{}
	cfg := checkConfig(r, cfgPath) // nil if the file is missing/unparsable
	checkSecrets(r)
	checkTools(r, cfg)
	checkPaths(r, cfg)
	checkCheckout(r, cfg)
	checkMarketplaceCheckout(r, cfg)
	return r
}
