package config

import (
	"fmt"
	"os"

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
	IdeaPool              string     `yaml:"idea_pool"`
	RunsDir               string     `yaml:"runs_dir"`
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
	BuildStatus     string `yaml:"build_status"`
	PollIntervalMin int    `yaml:"poll_interval_min"`
	// Token read from EXTCTL_JIRA_TOKEN env var, not stored in config file
}

type Claude struct {
	VersionPin        string         `yaml:"version_pin"`
	SpecGenMaxTurns   int            `yaml:"spec_gen_max_turns"`
	BuildMaxTurns     map[string]int `yaml:"build_max_turns"`
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
	Repair   string `yaml:"repair"`
	Revise   string `yaml:"revise"`
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
		c.Jira.BuildStatus = "In Review"
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
