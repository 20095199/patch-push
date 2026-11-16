package repo

import (
	"context"
	"fmt"
	"log"
)

// cherryPickCommits applies the job's commits in order, resuming a previously
// stopped cherry-pick when the state marks it pending.
func (o *Orchestrator) cherryPickCommits(ctx context.Context, gitRepo *GitRepo, job *JobState, result *Result) error {
	if err := o.resumePendingCommit(ctx, gitRepo, job, result); err != nil {
		return err
	}
	for index := job.Next; index < len(job.Commits); index++ {
		if err := o.cherryPickCommitAt(ctx, gitRepo, job, result, index); err != nil {
			return err
		}
	}
	return nil
}

// resumePendingCommit records a commit the user finished cherry-picking
// manually before re-running with --continue, so it is not applied twice.
func (o *Orchestrator) resumePendingCommit(ctx context.Context, gitRepo *GitRepo, job *JobState, result *Result) error {
	if !job.Pending {
		return nil
	}
	if gitRepo.IsCherryPickInProgress(ctx) {
		if job.Next < len(job.Commits) {
			o.printf("resuming pending cherry-pick for %s\n", job.Commits[job.Next].Hash)
		}
		return nil
	}
	if job.Next < len(job.Commits) {
		result.Commits = append(result.Commits, job.Commits[job.Next])
		job.Next++
	}
	job.Pending = false
	return SaveState(o.statePath, o.state)
}

func (o *Orchestrator) cherryPickCommitAt(ctx context.Context, gitRepo *GitRepo, job *JobState, result *Result, index int) error {
	commit := job.Commits[index]

	isMerge, err := gitRepo.IsMergeCommit(ctx, commit.Hash)
	if err != nil {
		return err
	}
	if isMerge {
		o.printf("skip merge commit %s %s\n", commit.Hash, commit.Subject)
		job.Next = index + 1
		if err := SaveState(o.statePath, o.state); err != nil {
			log.Printf("save state failed: %v", err)
		}
		return nil
	}

	job.Next = index
	if err := SaveState(o.statePath, o.state); err != nil {
		return err
	}

	skipped, err := o.performCherryPick(ctx, gitRepo, job, commit)
	if err != nil {
		return err
	}
	job.Pending = false
	if !skipped {
		result.Commits = append(result.Commits, commit)
	}
	job.Next = index + 1
	return SaveState(o.statePath, o.state)
}

// performCherryPick either handles an already-stopped cherry-pick or starts a
// new one, falling back to conflict handling when git reports a conflict.
func (o *Orchestrator) performCherryPick(ctx context.Context, gitRepo *GitRepo, job *JobState, commit Commit) (bool, error) {
	if gitRepo.IsCherryPickInProgress(ctx) {
		job.Pending = true
		if err := SaveState(o.statePath, o.state); err != nil {
			return false, err
		}
		return o.handleConflict(ctx, gitRepo, commit)
	}

	skipped, err := gitRepo.CherryPick(ctx, commit.Hash)
	if err == nil {
		return skipped, nil
	}
	if !gitRepo.IsCherryPickInProgress(ctx) {
		return false, fmt.Errorf("cherry-pick %s failed: %w", commit.Hash, err)
	}
	job.Pending = true
	if err := SaveState(o.statePath, o.state); err != nil {
		return false, err
	}
	return o.handleConflict(ctx, gitRepo, commit)
}

// handleConflict asks the operator how to proceed and returns skipped=true
// when the conflicting commit should not be recorded as applied.
func (o *Orchestrator) handleConflict(ctx context.Context, gitRepo *GitRepo, commit Commit) (bool, error) {
	if o.options.Yes {
		return false, fmt.Errorf("%w: cherry-pick conflict on %s (%s)", ErrInterrupted, commit.Hash, commit.Subject)
	}

	action, err := o.prompter.ResolveConflict(commit)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrInterrupted, err)
	}

	switch action {
	case ConflictStop:
		return false, ErrInterrupted
	case ConflictSkip:
		if err := gitRepo.SkipCherryPick(ctx); err != nil {
			return false, err
		}
		return true, nil
	case ConflictResolved:
		if gitRepo.IsCherryPickInProgress(ctx) {
			if err := gitRepo.ContinueCherryPick(ctx); err != nil {
				return false, fmt.Errorf("%w: cherry-pick --continue failed: %v", ErrInterrupted, err)
			}
		}
		return false, nil
	}
	return false, nil
}
