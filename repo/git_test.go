package repo

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newWorkRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	return dir
}

func commitFile(t *testing.T, dir, name, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-m", message)
	return runGit(t, dir, "rev-parse", "HEAD")
}

func TestMatchesJira(t *testing.T) {
	cases := []struct {
		subject string
		jira    string
		want    bool
	}{
		{"ABC-1: fix", "ABC-1", true},
		{"ABC-1： 中文全角冒号", "ABC-1", true},
		{"[ABC-1] bracket", "ABC-1", true},
		{"ABC-1 no colon", "ABC-1", false},
		{"abc-1: lower case", "ABC-1", true},
		{"OTHER: fix", "ABC-1", false},
		{"", "ABC-1", false},
	}
	for _, c := range cases {
		if got := matchesJira(c.subject, c.jira); got != c.want {
			t.Errorf("matchesJira(%q,%q)=%v want %v", c.subject, c.jira, got, c.want)
		}
	}
}

func TestCommitsForJira(t *testing.T) {
	dir := newWorkRepo(t)
	commitFile(t, dir, "base.txt", "base", "base")

	runGit(t, dir, "checkout", "-b", "dev")
	c1 := commitFile(t, dir, "a.txt", "a", "ABC-1: one")
	commitFile(t, dir, "b.txt", "b", "OTHER: skip")
	commitFile(t, dir, "c.txt", "c", "ABC-1 no colon")
	c2 := commitFile(t, dir, "d.txt", "d", "ABC-1： fullwidth")
	c3 := commitFile(t, dir, "e.txt", "e", "[ABC-1] bracket")
	runGit(t, dir, "checkout", "main")

	g := NewGitRepo(dir, "https://git.example.com/g/p/")
	selection, err := g.CommitsForJira(context.Background(), "ABC-1", "main", "dev")
	if err != nil {
		t.Fatal(err)
	}
	got := commitHashes(selection.Pending)
	want := []string{c1, c2, c3}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pending = %v, want %v (oldest first)", got, want)
	}
	if len(selection.Applied) != 0 {
		t.Fatalf("applied = %v, want none", commitHashes(selection.Applied))
	}
}

func TestCommitsForJiraSkipsPatchEquivalent(t *testing.T) {
	dir := newWorkRepo(t)
	commitFile(t, dir, "base.txt", "base", "base")

	runGit(t, dir, "checkout", "-b", "dev")
	alreadyPicked := commitFile(t, dir, "a.txt", "a", "ABC-1: one")
	pending := commitFile(t, dir, "b.txt", "b", "ABC-1: two")

	runGit(t, dir, "checkout", "main")
	runGit(t, dir, "cherry-pick", alreadyPicked)
	runGit(t, dir, "commit", "--amend", "-m", "ABC-1: one (picked)")

	g := NewGitRepo(dir, "https://git.example.com/g/p/")
	selection, err := g.CommitsForJira(context.Background(), "ABC-1", "main", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got := commitHashes(selection.Pending); strings.Join(got, ",") != pending {
		t.Fatalf("pending = %v, want only %s (patch-equivalent commit must be skipped)", got, pending)
	}
	if got := commitHashes(selection.Applied); strings.Join(got, ",") != alreadyPicked {
		t.Fatalf("applied = %v, want only %s", got, alreadyPicked)
	}
}

func TestCommitsForJiraSkipsReverted(t *testing.T) {
	dir := newWorkRepo(t)
	commitFile(t, dir, "base.txt", "base", "base")

	runGit(t, dir, "checkout", "-b", "dev")
	reverted := commitFile(t, dir, "a.txt", "a", "ABC-1: will be reverted")
	kept := commitFile(t, dir, "b.txt", "b", "ABC-1: keep")
	runGit(t, dir, "revert", "--no-edit", reverted)
	runGit(t, dir, "checkout", "main")

	g := NewGitRepo(dir, "https://git.example.com/g/p/")
	selection, err := g.CommitsForJira(context.Background(), "ABC-1", "main", "dev")
	if err != nil {
		t.Fatal(err)
	}
	got := commitHashes(selection.Pending)
	want := []string{kept}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pending = %v, want %v", got, want)
	}
}

func commitHashes(commits []Commit) []string {
	hashes := make([]string, len(commits))
	for i, commit := range commits {
		hashes[i] = commit.Hash
	}
	return hashes
}

func TestMergeRequestURL(t *testing.T) {
	g := NewGitRepo("", "https://git.example.com/g/p/")
	got, err := g.MergeRequestURL("ABC-1_desc_qa", "prod")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://git.example.com/g/p/merge_requests/new?merge_request%5Bsource_branch%5D=ABC-1_desc_qa&merge_request%5Btarget_branch%5D=prod"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}

	if _, err := NewGitRepo("", "git@git.example.com:g/p.git").MergeRequestURL("x", "y"); err == nil {
		t.Fatal("expected error for non-http url")
	}
}

func TestParseMergeURL(t *testing.T) {
	out := "remote: To create a merge request for x, visit:\nremote:   https://git.example.com/g/p/merge_requests/new?merge_request%5Bsource_branch%5D=x&merge_request%5Btarget_branch%5D=qa\n"
	got := parseMergeURL(out)
	want := "https://git.example.com/g/p/merge_requests/new?merge_request%5Bsource_branch%5D=x&merge_request%5Btarget_branch%5D=qa"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWithTargetBranch(t *testing.T) {
	base := "https://git.example.com/g/p/-/merge_requests/new?merge_request%5Bsource_branch%5D=ABC-1_desc_qa"

	got, err := withTargetBranch(base, "release-1.0")
	if err != nil {
		t.Fatal(err)
	}
	want := base + "&merge_request%5Btarget_branch%5D=release-1.0"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	withDefault := base + "&merge_request%5Btarget_branch%5D=master"
	got, err = withTargetBranch(withDefault, "release-1.1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "master") || !strings.Contains(got, "release-1.1") {
		t.Fatalf("target branch was not overridden: %q", got)
	}

	if _, err := withTargetBranch("", "qa"); err == nil {
		t.Fatal("expected error for empty merge url")
	}
}

func TestResolveMergeURL(t *testing.T) {
	g := NewGitRepo("", "https://git.example.com/g/p/")

	got := resolveMergeURL(g, "", "ABC-1_x_qa", "qa")
	if !strings.Contains(got, "merge_request%5Btarget_branch%5D=qa") {
		t.Fatalf("empty raw url should fall back to a target-aware url: %q", got)
	}

	raw := "https://git.example.com/g/p/-/merge_requests/new?merge_request%5Bsource_branch%5D=ABC-1_x_qa&merge_request%5Btarget_branch%5D=master"
	got = resolveMergeURL(g, raw, "ABC-1_x_qa", "release-1.2")
	if strings.Contains(got, "master") {
		t.Fatalf("resolved url still targets master: %q", got)
	}
	if !strings.Contains(got, "merge_request%5Btarget_branch%5D=release-1.2") {
		t.Fatalf("resolved url missing expected target: %q", got)
	}

	if got := resolveMergeURL(NewGitRepo("", "git@git.example.com:g/p.git"), "", "x", "y"); got != "" {
		t.Fatalf("expected empty url for unusable repo url, got %q", got)
	}
}

func TestProtectedBranches(t *testing.T) {
	for _, b := range []string{"master", "main", "qa", "staging", "prod", "release/1.0"} {
		if !isProtectedBranch(b) {
			t.Errorf("%q should be protected", b)
		}
	}
	if isProtectedBranch("ABC-1_feature_qa") {
		t.Fatal("integration branch should not be protected")
	}
}

func setupRemoteAndClone(t *testing.T) (work, bare string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "origin.git")
	runGit(t, root, "init", "--bare", "-b", "main", bare)

	seed := filepath.Join(root, "seed")
	runGit(t, root, "clone", bare, seed)
	runGit(t, seed, "config", "user.name", "test")
	runGit(t, seed, "config", "user.email", "test@example.com")
	commitFile(t, seed, "base.txt", "base\n", "base")
	runGit(t, seed, "push", "-u", "origin", "main")

	runGit(t, seed, "checkout", "-b", "qa")
	runGit(t, seed, "push", "-u", "origin", "qa")

	runGit(t, seed, "checkout", "main")
	runGit(t, seed, "checkout", "-b", "dev")
	commitFile(t, seed, "feature.txt", "feature\n", "ABC-1: feature")
	runGit(t, seed, "push", "-u", "origin", "dev")

	work = filepath.Join(root, "work")
	runGit(t, root, "clone", bare, work)
	runGit(t, work, "config", "user.name", "test")
	runGit(t, work, "config", "user.email", "test@example.com")
	runGit(t, work, "branch", "dev", "origin/dev")
	runGit(t, work, "checkout", "-b", "qa", "origin/qa")
	runGit(t, work, "checkout", "main")
	return work, bare
}

func TestPushWithLease(t *testing.T) {
	work, bare := setupRemoteAndClone(t)
	g := NewGitRepo(work, "https://git.example.com/g/p/")

	runGit(t, work, "checkout", "dev")
	if _, err := g.PushWithLease(context.Background(), "dev"); err != nil {
		t.Fatalf("first push: %v", err)
	}
	commitFile(t, work, "feature2.txt", "more\n", "ABC-1: more")
	if _, err := g.PushWithLease(context.Background(), "dev"); err != nil {
		t.Fatalf("leased push: %v", err)
	}

	local := runGit(t, work, "rev-parse", "HEAD")
	remote := runGit(t, bare, "rev-parse", "refs/heads/dev")
	if local != remote {
		t.Fatalf("remote %s != local %s", remote, local)
	}
}

func TestOrchestratorDryRun(t *testing.T) {
	work, bare := setupRemoteAndClone(t)
	cfg := &Config{
		Patch: &Patch{DevBranch: "dev", TargetBranches: []string{"qa"}, JiraID: "ABC-1", JiraDescription: "feature"},
		Repo:  map[string]*Repo{"w": {Name: "w", Path: work, URL: "https://git.example.com/g/p/"}},
	}
	var out bytes.Buffer
	orch := NewOrchestrator(cfg, []string{"w"}, PushOptions{
		Model:      "n",
		DryRun:     true,
		Yes:        true,
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		StatePath:  filepath.Join(t.TempDir(), "state.json"),
		ReportPath: filepath.Join(t.TempDir(), "report.txt"),
		Out:        &out,
	})
	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if strings.Contains(runGit(t, work, "branch"), "ABC-1_feature_qa") {
		t.Fatal("dry run must not create the integration branch")
	}
	if out := runGit(t, bare, "branch"); strings.Contains(out, "ABC-1_feature_qa") {
		t.Fatal("dry run must not push")
	}
}

func TestOrchestratorPushHappyPath(t *testing.T) {
	work, bare := setupRemoteAndClone(t)
	cfg := &Config{
		Patch: &Patch{DevBranch: "dev", TargetBranches: []string{"qa"}, JiraID: "ABC-1", JiraDescription: "feature"},
		Repo:  map[string]*Repo{"w": {Name: "w", Path: work, URL: "https://git.example.com/g/p/"}},
	}
	reportPath := filepath.Join(t.TempDir(), "report.txt")
	var out bytes.Buffer
	orch := NewOrchestrator(cfg, []string{"w"}, PushOptions{
		Model:      "n",
		Yes:        true,
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		StatePath:  filepath.Join(t.TempDir(), "state.json"),
		ReportPath: reportPath,
		Out:        &out,
	})
	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("push: %v\n%s", err, out.String())
	}

	remote := runGit(t, bare, "rev-parse", "refs/heads/ABC-1_feature_qa")
	if remote == "" {
		t.Fatal("integration branch was not pushed")
	}
	if show := runGit(t, bare, "show", "ABC-1_feature_qa:feature.txt"); show != "feature" {
		t.Fatalf("cherry-picked content = %q", show)
	}
	if cur := runGit(t, work, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Fatalf("worktree not restored, on %q", cur)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ABC-1") || !strings.Contains(string(data), "merge_request") {
		t.Fatalf("report missing data:\n%s", data)
	}
}

func TestOrchestratorNoOpWhenCommitsAlreadyApplied(t *testing.T) {
	work, bare := setupRemoteAndClone(t)
	feature := runGit(t, work, "rev-parse", "dev")

	runGit(t, work, "checkout", "qa")
	runGit(t, work, "cherry-pick", feature)
	runGit(t, work, "commit", "--amend", "-m", "ABC-1: feature (picked)")
	runGit(t, work, "push", "origin", "qa")
	runGit(t, work, "checkout", "main")

	cfg := &Config{
		Patch: &Patch{DevBranch: "dev", TargetBranches: []string{"qa"}, JiraID: "ABC-1", JiraDescription: "feature"},
		Repo:  map[string]*Repo{"w": {Name: "w", Path: work, URL: "https://git.example.com/g/p/"}},
	}
	reportPath := filepath.Join(t.TempDir(), "report.txt")
	var out bytes.Buffer
	orch := NewOrchestrator(cfg, []string{"w"}, PushOptions{
		Model:      "n",
		Yes:        true,
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		StatePath:  filepath.Join(t.TempDir(), "state.json"),
		ReportPath: reportPath,
		Out:        &out,
	})
	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("no-op run: %v\n%s", err, out.String())
	}

	if strings.Contains(runGit(t, work, "branch"), "ABC-1_feature_qa") {
		t.Fatal("no-op must not create the integration branch")
	}
	if strings.Contains(runGit(t, bare, "branch"), "ABC-1_feature_qa") {
		t.Fatal("no-op must not push")
	}
	if cur := runGit(t, work, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Fatalf("worktree not restored, on %q", cur)
	}
	if !strings.Contains(out.String(), "skip already-applied commit") || !strings.Contains(out.String(), "NO-OP") {
		t.Fatalf("expected no-op output, got:\n%s", out.String())
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status: no-op") || !strings.Contains(string(data), "already_applied") {
		t.Fatalf("report missing no-op data:\n%s", data)
	}
}

func TestOrchestratorConflictSkip(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	runGit(t, root, "init", "--bare", "-b", "main", bare)
	seed := filepath.Join(root, "seed")
	runGit(t, root, "clone", bare, seed)
	runGit(t, seed, "config", "user.name", "test")
	runGit(t, seed, "config", "user.email", "test@example.com")
	commitFile(t, seed, "conflict.txt", "base\n", "base")
	runGit(t, seed, "push", "-u", "origin", "main")

	runGit(t, seed, "checkout", "-b", "qa")
	commitFile(t, seed, "conflict.txt", "qa\n", "qa change")
	runGit(t, seed, "push", "-u", "origin", "qa")

	runGit(t, seed, "checkout", "main")
	runGit(t, seed, "checkout", "-b", "dev")
	commitFile(t, seed, "conflict.txt", "dev\n", "ABC-9: dev change")
	runGit(t, seed, "push", "-u", "origin", "dev")

	work := filepath.Join(root, "work")
	runGit(t, root, "clone", bare, work)
	runGit(t, work, "config", "user.name", "test")
	runGit(t, work, "config", "user.email", "test@example.com")
	runGit(t, work, "branch", "dev", "origin/dev")
	runGit(t, work, "checkout", "-b", "qa", "origin/qa")
	runGit(t, work, "checkout", "main")

	cfg := &Config{
		Patch: &Patch{DevBranch: "dev", TargetBranches: []string{"qa"}, JiraID: "ABC-9", JiraDescription: "conflict"},
		Repo:  map[string]*Repo{"w": {Name: "w", Path: work, URL: "https://git.example.com/g/p/"}},
	}
	var out bytes.Buffer
	orch := NewOrchestrator(cfg, []string{"w"}, PushOptions{
		Model:      "n",
		Yes:        false,
		ConfigPath: filepath.Join(root, "config.yaml"),
		StatePath:  filepath.Join(root, "state.json"),
		ReportPath: filepath.Join(root, "report.txt"),
		Out:        &out,
		In:         strings.NewReader("s\n"),
	})
	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("conflict skip: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "cherry-pick conflict") {
		t.Fatalf("expected conflict prompt, got:\n%s", out.String())
	}
}

func TestCherryPickCommitsResumesAfterManualContinue(t *testing.T) {
	dir := newWorkRepo(t)
	commitFile(t, dir, "base.txt", "base\n", "base")

	runGit(t, dir, "checkout", "-b", "dev")
	hash := commitFile(t, dir, "feature.txt", "feature\n", "ABC-1: feature")

	runGit(t, dir, "checkout", "main")
	runGit(t, dir, "checkout", "-b", "ABC-1_feature_qa")
	runGit(t, dir, "cherry-pick", hash)

	g := NewGitRepo(dir, "https://git.example.com/g/p/")
	js := &JobState{
		RepoName:     "w",
		RepoPath:     dir,
		DevBranch:    "dev",
		TargetBranch: "qa",
		NewBranch:    "ABC-1_feature_qa",
		Commits:      []Commit{{Hash: hash, Subject: "ABC-1: feature"}},
		Next:         0,
		Pending:      true,
	}
	o := &Orchestrator{
		state:     &State{Active: js, Model: "n"},
		statePath: filepath.Join(t.TempDir(), "state.json"),
		options:   PushOptions{Out: &bytes.Buffer{}},
		prompter:  NewConsolePrompter(nil, nil),
	}
	res := &Result{}
	if err := o.cherryPickCommits(context.Background(), g, js, res); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if js.Pending || js.Next != 1 {
		t.Fatalf("pending=%v next=%d", js.Pending, js.Next)
	}
	if len(res.Commits) != 1 {
		t.Fatalf("should record the manually continued commit, got %d", len(res.Commits))
	}
	if count := runGit(t, dir, "rev-list", "--count", "HEAD"); count != "2" {
		t.Fatalf("commit should not be applied twice, HEAD has %s commits", count)
	}
}
