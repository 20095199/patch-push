package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GitRepo provides high-level operations on a local git repository.
type GitRepo struct {
	Path   string
	URL    string
	Runner Runner
}

// NewGitRepo builds a GitRepo that executes real git commands with the default timeout.
func NewGitRepo(path, repoURL string) *GitRepo {
	return &GitRepo{Path: path, URL: repoURL, Runner: NewExecRunner(DefaultTimeout)}
}

func (r *GitRepo) run(ctx context.Context, args ...string) (string, string, error) {
	return r.Runner.Run(ctx, r.Path, "git", args...)
}

// EnsureRepository verifies that Path is inside a git work tree.
func (r *GitRepo) EnsureRepository(ctx context.Context) error {
	if r.Path == "" {
		return errors.New("repo path is empty")
	}
	out, _, err := r.run(ctx, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("not a git repository: %s: %w", r.Path, err)
	}
	if strings.TrimSpace(out) != "true" {
		return fmt.Errorf("%s is not inside a git work tree", r.Path)
	}
	return nil
}

// CurrentBranch returns the currently checked-out branch, rejecting detached HEAD.
func (r *GitRepo) CurrentBranch(ctx context.Context) (string, error) {
	out, _, err := r.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" || branch == "HEAD" {
		return "", errors.New("repository is in detached HEAD state")
	}
	return branch, nil
}

// Branches lists local branch names.
func (r *GitRepo) Branches(ctx context.Context) ([]string, error) {
	out, _, err := r.run(ctx, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		if branch := strings.TrimSpace(line); branch != "" {
			branches = append(branches, branch)
		}
	}
	return branches, nil
}

// HasBranch reports whether branch exists locally.
func (r *GitRepo) HasBranch(ctx context.Context, branch string) (bool, error) {
	if branch == "" {
		return false, errors.New("branch is empty")
	}
	branches, err := r.Branches(ctx)
	if err != nil {
		return false, err
	}
	for _, localBranch := range branches {
		if localBranch == branch {
			return true, nil
		}
	}
	return false, nil
}

// HasRemoteBranch reports whether branch exists on origin and returns its SHA.
func (r *GitRepo) HasRemoteBranch(ctx context.Context, branch string) (bool, string, error) {
	if branch == "" {
		return false, "", errors.New("branch is empty")
	}
	out, _, err := r.run(ctx, "ls-remote", "--heads", "origin", "refs/heads/"+branch)
	if err != nil {
		return false, "", err
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) == 0 {
		return false, "", nil
	}
	return true, fields[0], nil
}

// CheckRemoteAccess verifies that origin is reachable within a bounded timeout.
func (r *GitRepo) CheckRemoteAccess(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _, err := r.run(ctx, "ls-remote")
	return err
}

// Checkout switches to an existing branch.
func (r *GitRepo) Checkout(ctx context.Context, branch string) error {
	if branch == "" {
		return errors.New("branch is empty")
	}
	_, _, err := r.run(ctx, "checkout", branch)
	return err
}

// CheckoutDetached detaches HEAD at the current commit.
func (r *GitRepo) CheckoutDetached(ctx context.Context) error {
	_, _, err := r.run(ctx, "checkout", "--detach")
	return err
}

// CreateBranch creates and checks out a new local branch from HEAD.
func (r *GitRepo) CreateBranch(ctx context.Context, branch string) error {
	if branch == "" {
		return errors.New("branch is empty")
	}
	_, _, err := r.run(ctx, "checkout", "-b", branch)
	return err
}

// CreateBranchFromRemote creates and checks out branch from origin/<branch>.
func (r *GitRepo) CreateBranchFromRemote(ctx context.Context, branch string) error {
	if branch == "" {
		return errors.New("branch is empty")
	}
	_, _, err := r.run(ctx, "checkout", "-b", branch, "origin/"+branch)
	return err
}

// WorktreeClean reports whether tracked files have uncommitted changes.
func (r *GitRepo) WorktreeClean(ctx context.Context) (bool, string, error) {
	out, _, err := r.run(ctx, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, "", err
	}
	status := strings.TrimSpace(out)
	return status == "", status, nil
}

// Fetch updates remote-tracking refs and prunes deleted branches.
func (r *GitRepo) Fetch(ctx context.Context) error {
	_, _, err := r.run(ctx, "fetch", "--prune")
	return err
}

// PullFastForwardOnly pulls the current branch, refusing to create a merge commit.
func (r *GitRepo) PullFastForwardOnly(ctx context.Context) error {
	_, _, err := r.run(ctx, "pull", "--ff-only")
	return err
}

// DeleteLocalBranch deletes a local branch, refusing protected branches.
func (r *GitRepo) DeleteLocalBranch(ctx context.Context, branch string) error {
	if isProtectedBranch(branch) {
		return fmt.Errorf("refusing to delete protected branch %q", branch)
	}
	return r.DeleteLocalBranchForce(ctx, branch)
}

// DeleteLocalBranchForce deletes a local branch without checking whether it is merged.
func (r *GitRepo) DeleteLocalBranchForce(ctx context.Context, branch string) error {
	if branch == "" {
		return errors.New("branch is empty")
	}
	_, _, err := r.run(ctx, "branch", "-D", branch)
	return err
}

func isProtectedBranch(branch string) bool {
	switch branch {
	case "master", "main", "qa", "staging", "prod", "release":
		return true
	}
	return strings.HasPrefix(branch, "release/")
}

// DevBranchExists returns an error when branch does not exist locally.
func (r *GitRepo) DevBranchExists(ctx context.Context, branch string) error {
	exists, err := r.HasBranch(ctx, branch)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("dev branch %q does not exist locally", branch)
	}
	return nil
}
