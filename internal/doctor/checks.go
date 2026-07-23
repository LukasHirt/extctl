package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/LukasHirt/extctl/internal/config"
)

// lookPath, runDockerComposeVersion, and runGhAuthStatus are overridable for
// tests so tool-presence checks don't depend on the actual host environment.
var (
	lookPath = func(name string) error {
		_, err := exec.LookPath(name)
		return err
	}
	runDockerComposeVersion = func() error {
		return exec.Command("docker", "compose", "version").Run()
	}
	runGhAuthStatus = func() error {
		return exec.Command("gh", "auth", "status").Run()
	}
)

// buildSchema derives every yaml key-path config.Config recognizes via
// reflection, so it can never drift out of sync with the struct itself —
// unlike a hand-maintained list, which is exactly the kind of thing that
// already drifted (the example config's top-level "scaffold" block has no
// matching Config field at all).
func buildSchema() map[string]bool {
	schema := map[string]bool{}
	collectYAMLKeys(reflect.TypeFor[config.Config](), "", schema)
	return schema
}

func collectYAMLKeys(t reflect.Type, prefix string, out map[string]bool) {
	if t.Kind() != reflect.Struct {
		return
	}
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		out[path] = true

		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			collectYAMLKeys(ft, path, out)
		}
	}
}

// findUnknownKeys recursively walks a generically-decoded YAML document and
// returns every key-path not present in schema, sorted for stable output.
// Once a path is flagged as unknown, its children are NOT descended into —
// an unrecognized "scaffold:" block produces one finding ("scaffold"), not
// one per nested key.
func findUnknownKeys(raw map[string]any, schema map[string]bool, prefix string) []string {
	var unknown []string
	for k, v := range raw {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if !schema[path] {
			unknown = append(unknown, path)
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			unknown = append(unknown, findUnknownKeys(nested, schema, path)...)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func checkConfig(r *Report, cfgPath string) *config.Config {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		r.add(SectionConfig, ERROR, "%s: not found or unreadable: %v", cfgPath, err)
		return nil
	}

	var rawMap map[string]any
	if err := yaml.Unmarshal(raw, &rawMap); err != nil {
		r.add(SectionConfig, ERROR, "%s: invalid YAML: %v", cfgPath, err)
		return nil
	}
	r.add(SectionConfig, OK, "%s parses as valid YAML", cfgPath)

	for _, path := range findUnknownKeys(rawMap, buildSchema(), "") {
		r.add(SectionConfig, WARN, "unknown config key %q (not recognized by extctl — silently ignored)", path)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		// Should be unreachable given the two checks above already passed,
		// but keep it defensive rather than panicking.
		r.add(SectionConfig, ERROR, "%s: %v", cfgPath, err)
		return nil
	}

	checkRequiredField(r, "jira.base_url", cfg.Jira.BaseURL)
	checkRequiredField(r, "jira.project", cfg.Jira.Project)
	checkRequiredField(r, "target_repo.remote", cfg.TargetRepo.Remote)
	checkRequiredField(r, "marketplace_repo.remote", cfg.MarketplaceRepo.Remote)

	return cfg
}

func checkRequiredField(r *Report, name, value string) {
	if value == "" {
		r.add(SectionConfig, ERROR, "%s is required but empty", name)
		return
	}
	r.add(SectionConfig, OK, "%s is set", name)
}

func checkSecrets(r *Report) {
	if _, err := config.JiraEmail(); err != nil {
		r.add(SectionSecrets, ERROR, "%v", err)
	} else {
		r.add(SectionSecrets, OK, "EXTCTL_JIRA_EMAIL is set")
	}
	if _, err := config.JiraToken(); err != nil {
		r.add(SectionSecrets, ERROR, "%v", err)
	} else {
		r.add(SectionSecrets, OK, "EXTCTL_JIRA_TOKEN is set")
	}
}

func checkTools(r *Report, cfg *config.Config) {
	binaries := []string{"git", "gh", "claude", "docker", "pnpm"}
	found := map[string]bool{}
	for _, name := range binaries {
		if err := lookPath(name); err != nil {
			r.add(SectionTools, ERROR, "%s not found on PATH", name)
			found[name] = false
		} else {
			r.add(SectionTools, OK, "%s found on PATH", name)
			found[name] = true
		}
	}

	if found["docker"] {
		if err := runDockerComposeVersion(); err != nil {
			r.add(SectionTools, ERROR, "docker compose plugin not available: %v", err)
		} else {
			r.add(SectionTools, OK, "docker compose plugin available")
		}
	}

	if found["gh"] {
		if err := runGhAuthStatus(); err != nil {
			r.add(SectionTools, WARN, "gh auth status failed — not authenticated: %v", err)
		} else {
			r.add(SectionTools, OK, "gh auth status: authenticated")
		}
	}

	// media.enabled defaults to true (applyDefaults always sets a non-nil
	// pointer via config.Load), so a nil cfg — config failed to load — is
	// treated as "assume enabled" to avoid silently skipping the check.
	mediaEnabled := cfg == nil || cfg.Media.Enabled == nil || *cfg.Media.Enabled
	if !mediaEnabled {
		r.add(SectionTools, OK, "media disabled — ffmpeg not required")
		return
	}
	if err := lookPath("ffmpeg"); err != nil {
		r.add(SectionTools, WARN, "ffmpeg not found on PATH — demo videos will be skipped (media.enabled=true; screenshots still work)")
	} else {
		r.add(SectionTools, OK, "ffmpeg found on PATH")
	}
}

func checkPaths(r *Report, cfg *config.Config) {
	if cfg == nil {
		return
	}

	prompts := map[string]string{
		"prompts.gen_specs":               cfg.Prompts.GenSpecs,
		"prompts.plan":                    cfg.Prompts.Plan,
		"prompts.derive_stages":           cfg.Prompts.DeriveStages,
		"prompts.build_stage":             cfg.Prompts.BuildStage,
		"prompts.build_summary":           cfg.Prompts.BuildSummary,
		"prompts.repair":                  cfg.Prompts.Repair,
		"prompts.rebase_repair":           cfg.Prompts.RebaseRepair,
		"prompts.revise":                  cfg.Prompts.Revise,
		"prompts.infer_tags":              cfg.Prompts.InferTags,
		"prompts.marketplace_screenshots": cfg.Prompts.MarketplaceScreenshots,
	}
	// Sort keys for stable output.
	keys := make([]string, 0, len(prompts))
	for k := range prompts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		checkFileExists(r, k, prompts[k])
	}

	checkFileExists(r, "idea_pool", cfg.IdeaPool)
	checkDirExists(r, "scaffold_dir", cfg.ScaffoldDir)
}

func checkFileExists(r *Report, name, path string) {
	if _, err := os.Stat(path); err != nil {
		r.add(SectionPaths, ERROR, "%s (%s): %v", name, path, err)
		return
	}
	r.add(SectionPaths, OK, "%s exists", path)
}

func checkDirExists(r *Report, name, path string) {
	info, err := os.Stat(path)
	if err != nil {
		r.add(SectionPaths, ERROR, "%s (%s): %v", name, path, err)
		return
	}
	if !info.IsDir() {
		r.add(SectionPaths, ERROR, "%s (%s) exists but is not a directory", name, path)
		return
	}
	r.add(SectionPaths, OK, "%s exists", path)
}

func checkCheckout(r *Report, cfg *config.Config) {
	if cfg == nil {
		return
	}
	checkCheckoutAt(r, SectionCheckout, cfg.TargetRepo.Checkout)
}

func checkMarketplaceCheckout(r *Report, cfg *config.Config) {
	if cfg == nil {
		return
	}
	checkCheckoutAt(r, SectionMarketplaceCheckout, cfg.MarketplaceRepo.Checkout)
}

func checkCheckoutAt(r *Report, section Section, checkout string) {
	info, err := os.Stat(checkout)
	if err != nil {
		if os.IsNotExist(err) {
			r.add(section, OK, "%s does not exist yet — will be created via `gh repo clone` on first run", checkout)
			return
		}
		r.add(section, ERROR, "%s: %v", checkout, err)
		return
	}
	if !info.IsDir() {
		r.add(section, ERROR, "%s exists but is not a directory", checkout)
		return
	}
	if _, err := os.Stat(filepath.Join(checkout, ".git")); err != nil {
		r.add(section, ERROR, "%s exists but has no .git entry — not a valid git working tree", checkout)
		return
	}
	r.add(section, OK, "%s exists and looks like a valid git working tree", checkout)
}
