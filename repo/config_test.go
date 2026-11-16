package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validConfig = `
patch:
  dev_branch: ABC-1_feature_dev
  tgt_branchs:
    - qa
    - prod
  jira_id: ABC-1
  jira_desc: feature

repo:
  bot:
    url: https://git.example.com/bot/
    path: /tmp/bot
  common:
    name: common
    url: https://git.example.com/common/
    path: /tmp/common
`

func TestLoadConfigValid(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Patch.JiraID != "ABC-1" || cfg.Patch.JiraDescription != "feature" {
		t.Fatalf("unexpected patch: %+v", cfg.Patch)
	}
	if cfg.Repo["bot"].Name != "bot" {
		t.Fatalf("repo name should default to key, got %q", cfg.Repo["bot"].Name)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	cases := map[string]string{
		"missing patch": `
repo:
  bot: {url: "https://x/", path: "/tmp/bot"}
`,
		"missing jira id": `
patch: {dev_branch: dev, tgt_branchs: [qa], jira_desc: feature}
repo:
  bot: {url: "https://x/", path: "/tmp/bot"}
`,
		"bad desc": `
patch: {dev_branch: dev, tgt_branchs: [qa], jira_id: ABC-1, jira_desc: "has space"}
repo:
  bot: {url: "https://x/", path: "/tmp/bot"}
`,
		"no targets": `
patch: {dev_branch: dev, tgt_branchs: [], jira_id: ABC-1, jira_desc: feature}
repo:
  bot: {url: "https://x/", path: "/tmp/bot"}
`,
		"dev equals target": `
patch: {dev_branch: qa, tgt_branchs: [qa], jira_id: ABC-1, jira_desc: feature}
repo:
  bot: {url: "https://x/", path: "/tmp/bot"}
`,
		"missing repo path": `
patch: {dev_branch: dev, tgt_branchs: [qa], jira_id: ABC-1, jira_desc: feature}
repo:
  bot: {url: "https://x/"}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeConfig(t, body)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestReposOrderAndDedupe(t *testing.T) {
	cfg := &Config{
		Patch: &Patch{TargetBranches: []string{"qa"}},
		Repo: map[string]*Repo{
			"a": {Name: "a", Path: "/tmp/a", URL: "https://x/a"},
			"b": {Name: "b", Path: "/tmp/b", URL: "https://x/b"},
		},
	}
	repos, err := cfg.SelectRepos([]string{"b", "a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0].Name != "b" || repos[1].Name != "a" {
		t.Fatalf("unexpected order: %+v", repos)
	}
	if _, err := cfg.SelectRepos([]string{"missing"}); err == nil {
		t.Fatal("expected unknown repo error")
	}
}

func TestValidateBranchName(t *testing.T) {
	valid := []string{"qa", "prod", "ABC-1_feature_qa", "release/1.2"}
	for _, b := range valid {
		if err := ValidateBranchName(b); err != nil {
			t.Errorf("%q should be valid: %v", b, err)
		}
	}
	invalid := []string{"", "-bad", "has space", "a..b", "a//b", "bad/", "x.lock", "a:b", "a~b"}
	for _, b := range invalid {
		if err := ValidateBranchName(b); err == nil {
			t.Errorf("%q should be invalid", b)
		}
	}
}

func TestValidateJiraDesc(t *testing.T) {
	if err := ValidateJiraDescription("example_feature"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"has space", "a/b", "中文", "a:b"} {
		if err := ValidateJiraDescription(d); err == nil {
			t.Errorf("%q should be invalid", d)
		}
	}
	if !strings.Contains(validConfig, "jira_desc: feature") {
		t.Fatal("fixture drift")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	body := `
patch:
  dev_branch: ABC-1_feature_dev
  tgt_branchs: [qa]
  jira_id: ABC-1
  jira_desc: feature
  repos: [bot, common]

push:
  model: a
  dry_run: true
  yes: true

pull:
  delete: true

repo:
  bot: {url: "https://x/bot/", path: /tmp/bot}
  common: {url: "https://x/common/", path: /tmp/common}
`
	cfg, err := LoadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Patch.Repos) != 2 || cfg.Patch.Repos[1] != "common" {
		t.Fatalf("unexpected repos: %v", cfg.Patch.Repos)
	}
	if cfg.Push.Model != "a" || !cfg.Push.DryRun || !cfg.Push.Yes {
		t.Fatalf("unexpected push defaults: %+v", cfg.Push)
	}
	if !cfg.Pull.Delete {
		t.Fatal("pull.delete not parsed")
	}
}

func TestLoadConfigRejectsBadDefaults(t *testing.T) {
	cases := map[string]string{
		"unknown repo in repos": `
patch:
  dev_branch: dev
  tgt_branchs: [qa]
  jira_id: ABC-1
  jira_desc: feature
  repos: [nope]
repo:
  bot: {url: "https://x/", path: /tmp/bot}
`,
		"bad model": validConfig + `
push: {model: x}
`,
		"continue and abort": validConfig + `
push: {continue: true, abort: true}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeConfig(t, body)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestResolvePushSettingsPrecedence(t *testing.T) {
	cfg := &Config{
		Patch: &Patch{Repos: []string{"bot"}},
		Push:  PushDefaults{Model: "a", DryRun: true, Yes: true, Continue: false},
	}

	got := ResolvePushSettings(PushSettings{Model: "n"}, cfg, map[string]bool{})
	if len(got.Repos) != 1 || got.Repos[0] != "bot" {
		t.Fatalf("repos from config = %v", got.Repos)
	}
	if got.Model != "a" || !got.DryRun || !got.Yes {
		t.Fatalf("config defaults not applied: %+v", got)
	}

	explicit := map[string]bool{"r": true, "m": true, "dry-run": true}
	got = ResolvePushSettings(PushSettings{Repos: []string{"common"}, Model: "n", DryRun: false}, cfg, explicit)
	if len(got.Repos) != 1 || got.Repos[0] != "common" {
		t.Fatalf("cli repos should win: %v", got.Repos)
	}
	if got.Model != "n" || got.DryRun {
		t.Fatalf("cli flags should win: %+v", got)
	}
	if !got.Yes {
		t.Fatal("non-explicit config default should still apply")
	}
}

func TestResolvePullSettingsPrecedence(t *testing.T) {
	cfg := &Config{Patch: &Patch{Repos: []string{"bot"}}, Pull: PullDefaults{Delete: true}}

	got := ResolvePullSettings(PullSettings{}, cfg, map[string]bool{})
	if got.Repos[0] != "bot" || !got.Delete {
		t.Fatalf("config defaults not applied: %+v", got)
	}

	got = ResolvePullSettings(PullSettings{Repos: []string{"common"}, Delete: false}, cfg, map[string]bool{"r": true, "d": true})
	if got.Repos[0] != "common" || got.Delete {
		t.Fatalf("cli flags should win: %+v", got)
	}
}
