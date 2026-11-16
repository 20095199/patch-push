package repo

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	jiraDescriptionInvalidPattern = `[^A-Za-z0-9._-]`
	forbiddenBranchCharacters     = " ~^:?*[\\"
)

var jiraDescriptionInvalid = regexp.MustCompile(jiraDescriptionInvalidPattern)

// Config is the top-level configuration loaded from config.yaml.
type Config struct {
	Repo  map[string]*Repo `yaml:"repo"`
	Patch *Patch           `yaml:"patch"`
	Push  PushDefaults     `yaml:"push"`
	Pull  PullDefaults     `yaml:"pull"`
}

// PushDefaults holds default values for the push subcommand flags.
type PushDefaults struct {
	Model    string `yaml:"model"`
	DryRun   bool   `yaml:"dry_run"`
	Yes      bool   `yaml:"yes"`
	Continue bool   `yaml:"continue"`
	Abort    bool   `yaml:"abort"`
}

// PullDefaults holds default values for the pull subcommand flag.
type PullDefaults struct {
	Delete bool `yaml:"delete"`
}

// Repo describes a local git repository and the remote URL used to build MR links.
type Repo struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	Path string `yaml:"path"`
}

// Patch describes the Jira change to propagate.
type Patch struct {
	DevBranch       string   `yaml:"dev_branch"`
	TargetBranches  []string `yaml:"tgt_branchs"`
	JiraID          string   `yaml:"jira_id"`
	JiraDescription string   `yaml:"jira_desc"`
	Repos           []string `yaml:"repos"`
}

// LoadConfig reads, parses and validates the configuration file at path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %q: %w", path, err)
	}
	return &config, nil
}

// Validate checks the required fields and cross-field constraints of the
// configuration. It also fills in each repo's Name from its map key when unset.
func (c *Config) Validate() error {
	if c.Patch == nil {
		return errors.New("missing patch section")
	}
	if err := c.Patch.validate(); err != nil {
		return err
	}
	if err := c.validateRepos(); err != nil {
		return err
	}
	if err := c.validateRepositoryReferences(); err != nil {
		return err
	}
	return c.validateDefaults()
}

func (p *Patch) validate() error {
	if strings.TrimSpace(p.JiraID) == "" {
		return errors.New("patch.jira_id is required")
	}
	if strings.TrimSpace(p.JiraDescription) == "" {
		return errors.New("patch.jira_desc is required")
	}
	if err := ValidateJiraDescription(p.JiraDescription); err != nil {
		return fmt.Errorf("patch.jira_desc: %w", err)
	}
	if strings.TrimSpace(p.DevBranch) == "" {
		return errors.New("patch.dev_branch is required")
	}
	if err := ValidateBranchName(p.DevBranch); err != nil {
		return fmt.Errorf("patch.dev_branch: %w", err)
	}
	return p.validateTargetBranches()
}

func (p *Patch) validateTargetBranches() error {
	if len(p.TargetBranches) == 0 {
		return errors.New("patch.tgt_branchs must contain at least one branch")
	}
	for _, branch := range p.TargetBranches {
		if err := ValidateBranchName(branch); err != nil {
			return fmt.Errorf("patch.tgt_branchs: %w", err)
		}
		if branch == p.DevBranch {
			return fmt.Errorf("patch.tgt_branchs contains the dev branch %q", branch)
		}
	}
	return nil
}

func (c *Config) validateRepos() error {
	if len(c.Repo) == 0 {
		return errors.New("repo section is empty")
	}
	for key, repository := range c.Repo {
		if repository == nil {
			return fmt.Errorf("repo %q is empty", key)
		}
		if repository.Name == "" {
			repository.Name = key
		}
		if strings.TrimSpace(repository.Path) == "" {
			return fmt.Errorf("repo %q: path is required", key)
		}
		if strings.TrimSpace(repository.URL) == "" {
			return fmt.Errorf("repo %q: url is required", key)
		}
	}
	return nil
}

func (c *Config) validateRepositoryReferences() error {
	for _, name := range c.Patch.Repos {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := c.Repo[name]; !ok {
			return fmt.Errorf("repos: unknown repo %q", name)
		}
	}
	return nil
}

func (c *Config) validateDefaults() error {
	if c.Push.Model != "" && c.Push.Model != "n" && c.Push.Model != "a" {
		return fmt.Errorf("push.model %q must be n or a", c.Push.Model)
	}
	if c.Push.Continue && c.Push.Abort {
		return errors.New("push.continue and push.abort cannot both be true")
	}
	return nil
}

// SelectRepos resolves repo keys to Repo entries, preserving the caller's
// order and dropping blanks and duplicates.
func (c *Config) SelectRepos(keys []string) ([]*Repo, error) {
	if len(keys) == 0 {
		return nil, errors.New("no repo specified, use -r")
	}
	seen := make(map[string]bool, len(keys))
	repositories := make([]*Repo, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		repository, ok := c.Repo[key]
		if !ok {
			return nil, fmt.Errorf("repo %q not found in config", key)
		}
		seen[key] = true
		repositories = append(repositories, repository)
	}
	if len(repositories) == 0 {
		return nil, errors.New("no valid repo specified")
	}
	return repositories, nil
}

// ValidateJiraDescription rejects characters that are illegal in git branch names.
func ValidateJiraDescription(description string) error {
	if jiraDescriptionInvalid.MatchString(description) {
		return fmt.Errorf("%q contains characters not allowed in a branch name (use letters, digits, '.', '_' or '-')", description)
	}
	return nil
}

// ValidateBranchName applies the subset of git's ref-name rules relevant to
// branch names generated or consumed by this tool.
func ValidateBranchName(name string) error {
	if name == "" {
		return errors.New("branch name is empty")
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("branch name %q must not start with '-'", name)
	}
	if strings.ContainsAny(name, forbiddenBranchCharacters) {
		return fmt.Errorf("branch name %q contains a forbidden character", name)
	}
	if strings.Contains(name, "..") || strings.Contains(name, "//") {
		return fmt.Errorf("branch name %q contains an invalid sequence", name)
	}
	if strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") {
		return fmt.Errorf("branch name %q has an invalid suffix", name)
	}
	return nil
}
