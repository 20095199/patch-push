package repo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var mergeURLPattern = regexp.MustCompile(`https?://\S*?/merge_requests/new\?[^\s]+`)

// PushWithLease pushes localBranch to origin. When the remote branch already
// exists it uses --force-with-lease bound to the observed remote SHA, so a
// concurrent update by someone else is never overwritten.
func (r *GitRepo) PushWithLease(ctx context.Context, localBranch string) (string, error) {
	if localBranch == "" {
		return "", errors.New("branch is empty")
	}
	exists, remoteSHA, err := r.HasRemoteBranch(ctx, localBranch)
	if err != nil {
		return "", err
	}

	args := []string{"push", "--set-upstream", "origin", localBranch}
	if exists && remoteSHA != "" {
		lease := fmt.Sprintf("--force-with-lease=refs/heads/%s:%s", localBranch, remoteSHA)
		args = []string{"push", lease, "--set-upstream", "origin", localBranch}
	}

	stdout, stderr, err := r.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return parseMergeURL(stdout + "\n" + stderr), nil
}

func parseMergeURL(output string) string {
	return strings.TrimRight(mergeURLPattern.FindString(output), ".,;)")
}

// withTargetBranch pins merge_request[target_branch], because the URL printed by
// git push omits it and GitLab then defaults to the project's default branch.
func withTargetBranch(mergeURL, targetBranch string) (string, error) {
	if mergeURL == "" || targetBranch == "" {
		return "", errors.New("merge url and target branch are required")
	}
	parsed, err := url.Parse(mergeURL)
	if err != nil {
		return "", fmt.Errorf("parse merge url %q: %w", mergeURL, err)
	}
	query := parsed.Query()
	query.Set("merge_request[target_branch]", targetBranch)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// MergeRequestURL builds a GitLab new-MR URL for the given source and target branches.
func (r *GitRepo) MergeRequestURL(srcBranch, targetBranch string) (string, error) {
	if srcBranch == "" || targetBranch == "" {
		return "", errors.New("source and target branch are required")
	}
	base := strings.TrimRight(strings.TrimSpace(r.URL), "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return "", fmt.Errorf("repo url %q is not an http(s) url", r.URL)
	}
	return base + "/merge_requests/new?merge_request%5Bsource_branch%5D=" +
		url.QueryEscape(srcBranch) + "&merge_request%5Btarget_branch%5D=" + url.QueryEscape(targetBranch), nil
}
