package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// CandidateStatus mirrors the lifecycle of a spec candidate.
type CandidateStatus string

const (
	StatusNeedsApproval CandidateStatus = "needs_approval"
	StatusPicked        CandidateStatus = "picked"
	StatusDeclined      CandidateStatus = "declined"
	StatusDecayed       CandidateStatus = "decayed"    // hit max appearances
	StatusBacklogged    CandidateStatus = "backlogged"  // promoted to backlog after decay
	StatusRejected      CandidateStatus = "rejected"   // permanently invalid, never repropose
)

// Candidate is a single spec candidate in the daily slate.
type Candidate struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	JiraKey     string          `json:"jira_key"`
	JiraURL     string          `json:"jira_url"`
	Status      CandidateStatus `json:"status"`
	Appearances int             `json:"appearances"`       // 1 = first day offered
	Origin      string          `json:"origin"`            // "generated" | "carryover" | "manual"
	FirstDate   string          `json:"first_date"`        // YYYY-MM-DD, day first offered
	Effort        string          `json:"effort"`            // S | M | L
	SpecMD        string          `json:"spec_md"`           // full ## CANDIDATE block
	IssueComments  string          `json:"issue_comments,omitempty"`  // formatted Jira comments fetched at pick time
	RejectedReason string          `json:"rejected_reason,omitempty"` // reason provided when discarding during spec review
}

// Slate is the state file for one workday: runs/<date>/slate.json
type Slate struct {
	Date       string      `json:"date"`        // YYYY-MM-DD
	Candidates []Candidate `json:"candidates"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
	ReviewDone bool        `json:"review_done,omitempty"` // true once the interactive spec review has completed
}

func slateDir(runsDir, date string) string {
	return filepath.Join(runsDir, date)
}

func slatePath(runsDir, date string) string {
	return filepath.Join(slateDir(runsDir, date), "slate.json")
}

// Load reads the slate for the given date. Returns nil, nil if it doesn't exist.
func Load(runsDir, date string) (*Slate, error) {
	path := slatePath(runsDir, date)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open slate %s: %w", path, err)
	}
	defer f.Close()

	var s Slate
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode slate %s: %w", path, err)
	}
	return &s, nil
}

// Save writes the slate to disk, creating the directory if needed.
func Save(runsDir string, s *Slate) error {
	dir := slateDir(runsDir, s.Date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	s.UpdatedAt = time.Now()

	path := slatePath(runsDir, s.Date)
	f, err := os.CreateTemp(dir, "slate-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("encode slate: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename slate: %w", err)
	}
	return nil
}

// LoadAll reads all slates under runsDir and returns them sorted oldest-first.
// Directories that don't contain a slate.json are silently skipped.
func LoadAll(runsDir string) ([]*Slate, error) {
	entries, err := os.ReadDir(runsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read runs dir %s: %w", runsDir, err)
	}

	var slates []*Slate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := Load(runsDir, e.Name())
		if err != nil {
			return nil, err
		}
		if s != nil {
			slates = append(slates, s)
		}
	}
	return slates, nil
}

// Carryovers returns candidates from previous slates that are still open
// (status == needs_approval) and have not yet hit maxAppearances.
// The returned slice is ordered oldest-first.
func Carryovers(slates []*Slate, today string, maxAppearances int) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	for _, s := range slates {
		if s.Date >= today {
			continue
		}
		for _, c := range s.Candidates {
			if seen[c.ID] {
				continue
			}
			if c.Status == StatusNeedsApproval && c.Appearances < maxAppearances {
				seen[c.ID] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// DeliveredIDs returns the set of candidate IDs that have ever been picked or
// rejected, plus any IDs in the provided deliveredYAML map (pre-pipeline
// manually-built extensions). All are used as a deduplication guard in spec
// generation.
func DeliveredIDs(slates []*Slate, deliveredYAML map[string]bool) map[string]bool {
	out := map[string]bool{}
	for id := range deliveredYAML {
		out[id] = true
	}
	for _, s := range slates {
		for _, c := range s.Candidates {
			if c.Status == StatusPicked || c.Status == StatusRejected {
				out[c.ID] = true
			}
		}
	}
	return out
}

// deliveredEntry is one record in runs/delivered.yaml.
type deliveredEntry struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
}

// LoadDelivered reads runs/delivered.yaml and returns the set of IDs.
// Returns an empty map if the file does not exist.
func LoadDelivered(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read delivered.yaml %s: %w", path, err)
	}
	var entries []deliveredEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse delivered.yaml %s: %w", path, err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.ID] = true
	}
	return out, nil
}
