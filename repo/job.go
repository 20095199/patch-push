package repo

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// runJob executes the full push workflow for one repo and one target branch.
func (o *Orchestrator) runJob(ctx context.Context, repository *Repo, targetBranch string) (Result, error) {
	patch := o.config.Patch
	newBranch := buildIntegrationBranchName(patch, targetBranch)
	result := Result{
		RepoName:     repository.Name,
		RepoPath:     repository.Path,
		DevBranch:    patch.DevBranch,
		TargetBranch: targetBranch,
		NewBranch:    newBranch,
		DryRun:       o.options.DryRun,
	}

	gitRepo := NewGitRepo(repository.Path, repository.URL)
	if err := o.preflight(ctx, gitRepo, repository); err != nil {
		return result.fail(err)
	}

	originalBranch, err := gitRepo.CurrentBranch(ctx)
	if err != nil {
		return result.fail(err)
	}

	localTargetExists, err := gitRepo.HasBranch(ctx, targetBranch)
	if err != nil {
		return result.fail(err)
	}
	if !localTargetExists {
		remoteExists, _, err := gitRepo.HasRemoteBranch(ctx, targetBranch)
		if err != nil {
			return result.fail(err)
		}
		if !remoteExists {
			return result.fail(fmt.Errorf("target branch %q not found locally or on origin", targetBranch))
		}
	}

	targetRef, err := o.prepareTargetRef(ctx, gitRepo, targetBranch, localTargetExists)
	if err != nil {
		return result.fail(err)
	}

	selection, err := gitRepo.CommitsForJira(ctx, patch.JiraID, targetRef, patch.DevBranch)
	if err != nil {
		return result.fail(err)
	}

	result.Skipped = selection.Applied
	for _, commit := range selection.Applied {
		o.printf("skip already-applied commit %s %s\n", commit.Hash, commit.Subject)
	}

	if len(selection.Pending) == 0 {
		if len(selection.Applied) == 0 {
			return result.fail(fmt.Errorf("no commits matching %q found between %s and %s", patch.JiraID, targetRef, patch.DevBranch))
		}
		result.NoOp = true
		o.printf("no-op: all %d %s commits are already present in %s\n", len(selection.Applied), patch.JiraID, targetBranch)
		o.restoreBranch(ctx, gitRepo, originalBranch)
		return result, nil
	}

	commits := selection.Pending
	o.printCommitPlan(result, commits)

	if o.options.DryRun {
		if mergeURL, err := gitRepo.MergeRequestURL(newBranch, targetBranch); err == nil {
			result.MergeURL = mergeURL
		}
		result.Commits = commits
		return result, nil
	}

	if err := o.prepareBranch(ctx, gitRepo, patch.DevBranch, targetBranch, newBranch); err != nil {
		return result.fail(err)
	}

	job := &JobState{
		RepoName:     repository.Name,
		RepoPath:     repository.Path,
		DevBranch:    patch.DevBranch,
		TargetBranch: targetBranch,
		NewBranch:    newBranch,
		Commits:      commits,
	}
	o.state.Active = job
	if err := SaveState(o.statePath, o.state); err != nil {
		return result.fail(err)
	}

	if err := o.cherryPickCommits(ctx, gitRepo, job, &result); err != nil {
		return result.fail(err)
	}

	mergeURL, err := o.push(ctx, gitRepo, job)
	if err != nil {
		return result.fail(err)
	}
	result.MergeURL = mergeURL
	o.printf("pushed %s: %s -> %s\n  merge request: %s\n", repository.Name, newBranch, targetBranch, mergeURL)

	o.restoreBranch(ctx, gitRepo, originalBranch)
	return result, nil
}

// restoreBranch checks out branch unless it is already current, so early returns
// do not leave the working tree on the target branch.
func (o *Orchestrator) restoreBranch(ctx context.Context, gitRepo *GitRepo, branch string) {
	current, err := gitRepo.CurrentBranch(ctx)
	if err != nil {
		log.Printf("detect current branch failed: %v", err)
		return
	}
	if current == branch {
		return
	}
	if err := gitRepo.Checkout(ctx, branch); err != nil {
		log.Printf("restore branch %q failed: %v", branch, err)
	}
}

func (o *Orchestrator) resumeJob(ctx context.Context, repository *Repo, job *JobState) (Result, error) {
	result := Result{
		RepoName:     job.RepoName,
		RepoPath:     job.RepoPath,
		DevBranch:    job.DevBranch,
		TargetBranch: job.TargetBranch,
		NewBranch:    job.NewBranch,
	}

	gitRepo := NewGitRepo(repository.Path, repository.URL)
	if err := gitRepo.EnsureRepository(ctx); err != nil {
		return result.fail(err)
	}

	currentBranch, err := gitRepo.CurrentBranch(ctx)
	if err != nil {
		return result.fail(err)
	}
	if currentBranch != job.NewBranch {
		if err := gitRepo.Checkout(ctx, job.NewBranch); err != nil {
			return result.fail(err)
		}
	}

	o.printf("resuming %s -> %s from commit %d/%d\n", job.NewBranch, job.TargetBranch, job.Next, len(job.Commits))
	if err := o.cherryPickCommits(ctx, gitRepo, job, &result); err != nil {
		return result.fail(err)
	}

	mergeURL, err := o.push(ctx, gitRepo, job)
	if err != nil {
		return result.fail(err)
	}
	result.MergeURL = mergeURL
	o.printf("pushed %s: %s -> %s\n  merge request: %s\n", job.RepoName, job.NewBranch, job.TargetBranch, mergeURL)
	return result, nil
}

func (o *Orchestrator) preflight(ctx context.Context, gitRepo *GitRepo, repository *Repo) error {
	if err := gitRepo.EnsureRepository(ctx); err != nil {
		return err
	}
	if err := gitRepo.DevBranchExists(ctx, o.config.Patch.DevBranch); err != nil {
		return err
	}
	if err := gitRepo.CheckRemoteAccess(ctx); err != nil {
		return fmt.Errorf("cannot reach origin for %s: %w", repository.Path, err)
	}
	clean, status, err := gitRepo.WorktreeClean(ctx)
	if err != nil {
		return err
	}
	if !clean {
		return fmt.Errorf("working tree for %s is not clean; commit or stash changes first:\n%s", repository.Path, status)
	}
	return nil
}

// prepareTargetRef checks out/fetches the target branch as needed and returns
// the ref to diff against. Dry runs avoid mutating the working tree.
func (o *Orchestrator) prepareTargetRef(ctx context.Context, gitRepo *GitRepo, targetBranch string, localExists bool) (string, error) {
	switch {
	case o.options.DryRun:
		if localExists {
			return targetBranch, nil
		}
		if err := gitRepo.Fetch(ctx); err != nil {
			return "", err
		}
		return "origin/" + targetBranch, nil
	case localExists:
		if err := gitRepo.Checkout(ctx, targetBranch); err != nil {
			return "", err
		}
		if err := gitRepo.PullFastForwardOnly(ctx); err != nil {
			return "", err
		}
		return targetBranch, nil
	default:
		if err := gitRepo.Fetch(ctx); err != nil {
			return "", err
		}
		if err := gitRepo.CreateBranchFromRemote(ctx, targetBranch); err != nil {
			return "", err
		}
		return targetBranch, nil
	}
}

func (o *Orchestrator) prepareBranch(ctx context.Context, gitRepo *GitRepo, devBranch, targetBranch, newBranch string) error {
	if newBranch == devBranch || newBranch == targetBranch {
		return fmt.Errorf("generated branch %q conflicts with the dev or target branch", newBranch)
	}
	exists, err := gitRepo.HasBranch(ctx, newBranch)
	if err != nil {
		return err
	}
	if o.options.Model == "n" {
		if exists {
			if err := gitRepo.DeleteLocalBranch(ctx, newBranch); err != nil {
				return err
			}
		}
		return gitRepo.CreateBranch(ctx, newBranch)
	}

	if exists {
		return gitRepo.Checkout(ctx, newBranch)
	}
	remoteExists, _, err := gitRepo.HasRemoteBranch(ctx, newBranch)
	if err != nil {
		return err
	}
	if remoteExists {
		if err := gitRepo.Fetch(ctx); err != nil {
			return err
		}
		return gitRepo.CreateBranchFromRemote(ctx, newBranch)
	}
	return gitRepo.CreateBranch(ctx, newBranch)
}

func (o *Orchestrator) push(ctx context.Context, gitRepo *GitRepo, job *JobState) (string, error) {
	rawURL, err := gitRepo.PushWithLease(ctx, job.NewBranch)
	if err != nil {
		return "", err
	}
	mergeURL := resolveMergeURL(gitRepo, rawURL, job.NewBranch, job.TargetBranch)
	log.Printf("pushed repo=%s branch=%s target=%s url=%s", job.RepoName, job.NewBranch, job.TargetBranch, mergeURL)
	return mergeURL, nil
}

// resolveMergeURL prefers the URL printed by git, but always rewrites its target
// branch; without that GitLab falls back to the repository's default branch.
func resolveMergeURL(gitRepo *GitRepo, rawURL, srcBranch, targetBranch string) string {
	if rawURL != "" {
		normalized, err := withTargetBranch(rawURL, targetBranch)
		if err == nil {
			return normalized
		}
		log.Printf("normalise merge request url %q failed: %v", rawURL, err)
	}
	fallback, err := gitRepo.MergeRequestURL(srcBranch, targetBranch)
	if err != nil {
		log.Printf("build merge request url for %s failed: %v", srcBranch, err)
		return ""
	}
	return fallback
}

func (o *Orchestrator) printCommitPlan(result Result, commits []Commit) {
	o.printf("\n%s\n", strings.Repeat("-", outputRuleWidth))
	o.printf("repo: %s\n  dev: %s\n  target: %s\n  new branch: %s\n  commits (%d, oldest first):\n",
		result.RepoName, result.DevBranch, result.TargetBranch, result.NewBranch, len(commits))
	for index, commit := range commits {
		o.printf("  %2d. %s %s\n", index+1, commit.Hash, commit.Subject)
	}
}

func buildIntegrationBranchName(patch *Patch, targetBranch string) string {
	return fmt.Sprintf("%s_%s_%s", patch.JiraID, patch.JiraDescription, targetBranch)
}
