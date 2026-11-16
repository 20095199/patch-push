package repo

import (
	"context"
	"errors"
	"strings"
)

// Commit is a git commit reduced to the fields this tool needs.
type Commit struct {
	Hash    string
	Subject string
}

// CommitSelection separates Jira commits by whether their change already exists
// on the target branch (patch-equivalent, even when the commit hash differs).
type CommitSelection struct {
	Pending []Commit
	Applied []Commit
}

// CommitsForJira selects commits between targetRef and devBranch that belong to
// jiraID, oldest first. Reverted commits and commits whose change is already
// present in targetRef are excluded from Pending and reported in Applied.
//
// A commit belongs to the Jira when its subject starts with the Jira key
// followed by a colon (ASCII or full-width), or is wrapped as [JIRA-KEY].
func (r *GitRepo) CommitsForJira(ctx context.Context, jiraID, targetRef, devBranch string) (CommitSelection, error) {
	if jiraID == "" {
		return CommitSelection{}, errors.New("jira id is empty")
	}
	if targetRef == "" || devBranch == "" {
		return CommitSelection{}, errors.New("target ref and dev branch are required")
	}

	out, _, err := r.run(ctx, "cherry", "-v", targetRef, devBranch)
	if err != nil {
		return CommitSelection{}, err
	}
	marked := parseCherryOutput(out)
	allCommits := make([]Commit, 0, len(marked))
	for _, item := range marked {
		allCommits = append(allCommits, item.Commit)
	}
	reverts := revertSubjects(allCommits)

	var selection CommitSelection
	for _, item := range marked {
		if isReverted(item.Commit.Subject, reverts) || !matchesJira(item.Commit.Subject, jiraID) {
			continue
		}
		if item.Applied {
			selection.Applied = append(selection.Applied, item.Commit)
			continue
		}
		selection.Pending = append(selection.Pending, item.Commit)
	}
	return selection, nil
}

type markedCommit struct {
	Commit  Commit
	Applied bool
}

// parseCherryOutput parses `git cherry -v` lines: "+ <hash> <subject>" marks a
// pending commit and "- <hash> <subject>" a patch-equivalent one.
func parseCherryOutput(out string) []markedCommit {
	var commits []markedCommit
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(strings.TrimRight(line, "\r"), " ", 3)
		if len(fields) < 2 {
			continue
		}
		mark, hash := fields[0], fields[1]
		if mark != "+" && mark != "-" {
			continue
		}
		subject := ""
		if len(fields) == 3 {
			subject = fields[2]
		}
		commits = append(commits, markedCommit{
			Commit:  Commit{Hash: hash, Subject: subject},
			Applied: mark == "-",
		})
	}
	return commits
}

func matchesJira(subject, jiraID string) bool {
	if jiraID == "" {
		return false
	}
	trimmed := strings.TrimSpace(subject)
	if strings.HasPrefix(trimmed, "[") {
		if end := strings.Index(trimmed, "]"); end > 0 {
			return strings.EqualFold(trimmed[1:end], jiraID)
		}
		return false
	}
	if len(trimmed) < len(jiraID) || !strings.EqualFold(trimmed[:len(jiraID)], jiraID) {
		return false
	}
	rest := strings.TrimLeft(trimmed[len(jiraID):], " \t")
	return strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "：")
}

// IsMergeCommit reports whether hash has more than one parent.
func (r *GitRepo) IsMergeCommit(ctx context.Context, hash string) (bool, error) {
	out, _, err := r.run(ctx, "rev-list", "--parents", "-n", "1", hash)
	if err != nil {
		return false, err
	}
	return len(strings.Fields(strings.TrimSpace(out))) > 2, nil
}

// CherryPick applies hash onto the current branch. It returns skipped=true when
// the change is already present (an empty cherry-pick), which git reports as an
// error even though the desired state is already reached.
func (r *GitRepo) CherryPick(ctx context.Context, hash string) (skipped bool, err error) {
	stdout, stderr, err := r.run(ctx, "cherry-pick", hash)
	if err == nil {
		return false, nil
	}
	if isEmptyCherryPick(stdout + "\n" + stderr) {
		if _, _, skipErr := r.run(ctx, "cherry-pick", "--skip"); skipErr != nil {
			return false, skipErr
		}
		return true, nil
	}
	return false, err
}

func isEmptyCherryPick(output string) bool {
	markers := []string{
		"nothing to commit",
		"The previous cherry-pick is now empty",
		"no changes added to commit",
		"--allow-empty",
	}
	for _, marker := range markers {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

// IsCherryPickInProgress reports whether a cherry-pick is stopped mid-sequence.
func (r *GitRepo) IsCherryPickInProgress(ctx context.Context) bool {
	_, _, err := r.run(ctx, "rev-parse", "--verify", "--quiet", "CHERRY_PICK_HEAD")
	return err == nil
}

// ContinueCherryPick continues a stopped cherry-pick after conflicts were resolved.
func (r *GitRepo) ContinueCherryPick(ctx context.Context) error {
	_, _, err := r.run(ctx, "cherry-pick", "--continue")
	return err
}

// AbortCherryPick aborts an in-progress cherry-pick.
func (r *GitRepo) AbortCherryPick(ctx context.Context) error {
	_, _, err := r.run(ctx, "cherry-pick", "--abort")
	return err
}

// SkipCherryPick skips the commit that is currently being cherry-picked.
func (r *GitRepo) SkipCherryPick(ctx context.Context) error {
	_, _, err := r.run(ctx, "cherry-pick", "--skip")
	return err
}

func revertSubjects(commits []Commit) []string {
	var subjects []string
	for _, commit := range commits {
		if strings.HasPrefix(commit.Subject, "Revert") {
			subjects = append(subjects, commit.Subject)
		}
	}
	return subjects
}

// isReverted matches git's default revert message (`Revert "<original subject>"`)
// against a candidate subject using containment.
func isReverted(subject string, reverts []string) bool {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return false
	}
	for _, revert := range reverts {
		if revert != subject && strings.Contains(revert, subject) {
			return true
		}
	}
	return false
}
