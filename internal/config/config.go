package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Timezone              string     `yaml:"timezone"`
	FreshCandidatesPerDay int        `yaml:"fresh_candidates_per_day"`
	TargetRepo            TargetRepo `yaml:"target_repo"`
	DefaultBranch         string     `yaml:"default_branch"`
	Jira                  Jira       `yaml:"jira"`
	Claude                Claude     `yaml:"claude"`
	Decay                 Decay      `yaml:"decay"`
	Prompts               Prompts    `yaml:"prompts"`
	IdeaPool      string   `yaml:"idea_pool"`
	RunsDir       string   `yaml:"runs_dir"`
	DeliveredYAML string   `yaml:"delivered_yaml"`
	ScaffoldDir   string   `yaml:"scaffold_dir"`
	Scaffold      Scaffold `yaml:"scaffold"`
}

type TargetRepo struct {
	Remote   string `yaml:"remote"`
	Checkout string `yaml:"checkout"`
}

type Jira struct {
	BaseURL         string `yaml:"base_url"`
	Project         string `yaml:"project"`
	CandidateStatus string `yaml:"candidate_status"`
	PickStatus      string `yaml:"pick_status"`
	DeclineStatus   string `yaml:"decline_status"`
	BuildStatus string `yaml:"build_status"` // status to set when the PR is merged
	PollIntervalMin int    `yaml:"poll_interval_min"`
	// Token read from EXTCTL_JIRA_TOKEN env var, not stored in config file
}

type Claude struct {
	VersionPin        string         `yaml:"version_pin"`
	SpecGenMaxTurns   int            `yaml:"spec_gen_max_turns"`
	BuildMaxTurns     map[string]int `yaml:"build_max_turns"`
	MaxRepairAttempts int            `yaml:"max_repair_attempts"`
	BudgetUSDPerBuild float64        `yaml:"budget_usd_per_build"`
	BudgetUSDPerDay   float64        `yaml:"budget_usd_per_day"`
}

type Decay struct {
	MaxAppearances int    `yaml:"max_appearances"`
	Action         string `yaml:"action"` // backlog | decline | ask
}

type Prompts struct {
	GenSpecs string `yaml:"gen_specs"`
	Build    string `yaml:"build"`
	Continue string `yaml:"continue"`
	Repair   string `yaml:"repair"`
	Revise   string `yaml:"revise"`
}

// Scaffold controls how the extension template is sourced from the skeleton repo.
type Scaffold struct {
	// Source is the Git URL of the skeleton repository.
	Source string `yaml:"source"`
	// Exclude is a list of path prefixes (relative to the skeleton root) to skip
	// when copying files into scaffold/. Paths ending in "/" match directories.
	// Defaults are set by applyDefaults(); override here to add/remove entries.
	Exclude []string `yaml:"exclude"`
}

// Defaults applied when fields are zero-valued.
func (c *Config) applyDefaults() {
	if c.Timezone == "" {
		c.Timezone = "Europe/Prague"
	}
	if c.FreshCandidatesPerDay == 0 {
		c.FreshCandidatesPerDay = 3
	}
	if c.DefaultBranch == "" {
		c.DefaultBranch = "main"
	}
	if c.Jira.CandidateStatus == "" {
		c.Jira.CandidateStatus = "Needs Approval"
	}
	if c.Jira.PickStatus == "" {
		c.Jira.PickStatus = "Doing"
	}
	if c.Jira.DeclineStatus == "" {
		c.Jira.DeclineStatus = "Not Doing"
	}
	if c.Jira.BuildStatus == "" {
		c.Jira.BuildStatus = "Done"
	}
	if c.Jira.PollIntervalMin == 0 {
		c.Jira.PollIntervalMin = 10
	}
	if c.Claude.SpecGenMaxTurns == 0 {
		c.Claude.SpecGenMaxTurns = 20
	}
	if len(c.Claude.BuildMaxTurns) == 0 {
		c.Claude.BuildMaxTurns = map[string]int{"S": 50, "M": 60, "L": 80}
	}
	if c.Claude.MaxRepairAttempts == 0 {
		c.Claude.MaxRepairAttempts = 3
	}
	if c.Claude.BudgetUSDPerBuild == 0 {
		c.Claude.BudgetUSDPerBuild = 8
	}
	if c.Claude.BudgetUSDPerDay == 0 {
		c.Claude.BudgetUSDPerDay = 20
	}
	if c.Decay.MaxAppearances == 0 {
		c.Decay.MaxAppearances = 3
	}
	if c.Decay.Action == "" {
		c.Decay.Action = "backlog"
	}
	if c.Prompts.GenSpecs == "" {
		c.Prompts.GenSpecs = "prompts/gen-specs.md"
	}
	if c.Prompts.Build == "" {
		c.Prompts.Build = "prompts/build-extension.md"
	}
	if c.Prompts.Continue == "" {
		c.Prompts.Continue = "prompts/continue-build.md"
	}
	if c.Prompts.Repair == "" {
		c.Prompts.Repair = "prompts/repair.md"
	}
	if c.Prompts.Revise == "" {
		c.Prompts.Revise = "prompts/revise.md"
	}
	if c.IdeaPool == "" {
		c.IdeaPool = "idea-pool.yaml"
	}
	if c.RunsDir == "" {
		c.RunsDir = "runs"
	}
	if c.DeliveredYAML == "" {
		c.DeliveredYAML = "runs/delivered.yaml"
	}
	if c.ScaffoldDir == "" {
		c.ScaffoldDir = "scaffold"
	}
	if c.Scaffold.Source == "" {
		c.Scaffold.Source = "https://github.com/owncloud/web-app-skeleton"
	}
	if len(c.Scaffold.Exclude) == 0 {
		c.Scaffold.Exclude = defaultScaffoldExclude
	}
}

// defaultScaffoldExclude is the out-of-the-box exclusion list for scaffold fetch.
// It skips CI/CD infra, community docs, lock files, and files that extctl
// owns (template-var files and our custom additions).
var defaultScaffoldExclude = []string{
	// Git and CI
	".git/",
	".github/",
	// Local dev infra (paths are skeleton-specific)
	"dev/",
	"docker-compose.yml",
	".vscode/",
	// Package lock — regenerated per extension
	"pnpm-lock.yaml",
	// Community/repo metadata
	"LICENSE",
	"README.md",
	"CHANGELOG.md",
	"CONTRIBUTING.md",
	"CODE_OF_CONDUCT.md",
	"SECURITY.md",
	"SUPPORT.md",
	".release_note",
	// Template-var files owned by extctl scaffold (not sourced from skeleton as-is)
	"src/",
	"package.json",
	"vite.config.ts",
}

// LoadDotEnv reads a .env file and sets any unset environment variables found
// in it. Shell-exported vars always win — we never overwrite an existing value.
// Lines starting with # and blank lines are ignored. The expected format is
// KEY=VALUE or KEY="VALUE" (quotes are stripped).
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // .env is optional
		}
		return fmt.Errorf("open .env %s: %w", path, err)
	}
	defer f.Close()

	var lineNum int
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1)
	line := make([]byte, 0, 256)

	// Read byte-by-byte to avoid importing bufio.
	for {
		n, readErr := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[0])
		}
		if readErr != nil {
			break
		}
	}

	for _, b := range buf {
		if b == '\n' {
			lineNum++
			s := strings.TrimSpace(string(line))
			line = line[:0]
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			idx := strings.IndexByte(s, '=')
			if idx < 1 {
				return fmt.Errorf(".env %s line %d: expected KEY=VALUE", path, lineNum)
			}
			key := strings.TrimSpace(s[:idx])
			val := strings.TrimSpace(s[idx+1:])
			// Strip surrounding quotes.
			if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
				val = val[1 : len(val)-1]
			}
			if os.Getenv(key) == "" {
				if err := os.Setenv(key, val); err != nil {
					return fmt.Errorf(".env set %s: %w", key, err)
				}
			}
		} else {
			line = append(line, b)
		}
	}
	// Handle file not ending in newline.
	if s := strings.TrimSpace(string(line)); s != "" && !strings.HasPrefix(s, "#") {
		idx := strings.IndexByte(s, '=')
		if idx < 1 {
			return fmt.Errorf(".env %s: expected KEY=VALUE", path)
		}
		key := strings.TrimSpace(s[:idx])
		val := strings.TrimSpace(s[idx+1:])
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, val); err != nil {
				return fmt.Errorf(".env set %s: %w", key, err)
			}
		}
	}
	return nil
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults()
	return &cfg, nil
}

// JiraToken reads the Jira API token from the environment.
// Never stored in the config file.
func JiraToken() (string, error) {
	t := os.Getenv("EXTCTL_JIRA_TOKEN")
	if t == "" {
		return "", fmt.Errorf("EXTCTL_JIRA_TOKEN is not set")
	}
	return t, nil
}

// JiraEmail reads the Atlassian account email from the environment.
// Required for Jira Cloud Basic auth alongside the API token.
func JiraEmail() (string, error) {
	e := os.Getenv("EXTCTL_JIRA_EMAIL")
	if e == "" {
		return "", fmt.Errorf("EXTCTL_JIRA_EMAIL is not set")
	}
	return e, nil
}
