package repo

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Result captures the outcome of one repo/target-branch job.
type Result struct {
	RepoName     string
	RepoPath     string
	DevBranch    string
	TargetBranch string
	NewBranch    string
	Commits      []Commit
	Skipped      []Commit
	MergeURL     string
	DryRun       bool
	NoOp         bool
	Err          error
}

// fail records err on the result and returns it together with err, keeping the
// per-step error plumbing in runJob compact.
func (r *Result) fail(err error) (Result, error) {
	r.Err = err
	return *r, err
}

// Report collects per-branch results and appends them to a file.
type Report struct {
	mu      sync.Mutex
	path    string
	results []Result
}

// NewReport creates a Report that appends to path.
func NewReport(path string) *Report {
	return &Report{path: path}
}

// Add appends a result to the report.
func (r *Report) Add(result Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, result)
}

// Results returns a copy of the collected results.
func (r *Report) Results() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Result, len(r.results))
	copy(out, r.results)
	return out
}

const outputRuleWidth = 78

// Print writes a clearly delimited result summary to w.
func (r *Report) Print(w io.Writer) {
	results := r.Results()
	if len(results) == 0 {
		return
	}

	rule := strings.Repeat("=", outputRuleWidth)
	separator := strings.Repeat("-", outputRuleWidth)

	fmt.Fprintf(w, "\n%s\n", rule)
	fmt.Fprintf(w, "  RESULT SUMMARY  %s  (total %d)\n", time.Now().Format("2006-01-02 15:04:05"), len(results))
	fmt.Fprintf(w, "%s\n", rule)

	okCount, failedCount, dryRunCount, noOpCount := 0, 0, 0, 0
	for index, result := range results {
		if index > 0 {
			fmt.Fprintf(w, "%s\n", separator)
		}

		label := statusLabel(result)
		switch label {
		case "FAILED":
			failedCount++
		case "NO-OP":
			noOpCount++
		case "DRY-RUN":
			dryRunCount++
		default:
			okCount++
		}

		fmt.Fprintf(w, "[%-7s] %d/%d  %s  %s -> %s\n",
			label, index+1, len(results), result.RepoName, result.NewBranch, result.TargetBranch)
		if len(result.Commits) > 0 {
			fmt.Fprintf(w, "          commits: %d\n", len(result.Commits))
			for commitIndex, commit := range result.Commits {
				fmt.Fprintf(w, "            %2d. %s %s\n", commitIndex+1, commit.Hash, commit.Subject)
			}
		}
		if len(result.Skipped) > 0 {
			fmt.Fprintf(w, "          already applied: %d\n", len(result.Skipped))
			for commitIndex, commit := range result.Skipped {
				fmt.Fprintf(w, "            %2d. %s %s\n", commitIndex+1, commit.Hash, commit.Subject)
			}
		}
		if result.Err != nil {
			fmt.Fprintf(w, "          error: %v\n", result.Err)
		}
		if result.MergeURL != "" {
			fmt.Fprintf(w, "          merge request: %s\n", result.MergeURL)
		}
	}

	fmt.Fprintf(w, "%s\n", separator)
	fmt.Fprintf(w, "  ok: %d  |  no-op: %d  |  failed: %d  |  dry-run: %d  |  total: %d\n",
		okCount, noOpCount, failedCount, dryRunCount, len(results))
	fmt.Fprintf(w, "%s\n", rule)
}

func statusLabel(result Result) string {
	switch {
	case result.Err != nil:
		return "FAILED"
	case result.NoOp:
		return "NO-OP"
	case result.DryRun:
		return "DRY-RUN"
	default:
		return "OK"
	}
}

// Write appends one timestamped block containing all collected results to the report file.
func (r *Report) Write() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.results) == 0 {
		return nil
	}
	file, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open report %q: %w", r.path, err)
	}
	defer func() { _ = file.Close() }()

	var builder strings.Builder
	fmt.Fprintf(&builder, "\n===== %s =====\n", time.Now().Format("2006-01-02 15:04:05"))
	for _, result := range r.results {
		fmt.Fprintf(&builder, "repo: %s (%s)\n", result.RepoName, result.RepoPath)
		fmt.Fprintf(&builder, "  dev: %s -> %s (target: %s)\n", result.DevBranch, result.NewBranch, result.TargetBranch)
		if result.DryRun {
			builder.WriteString("  mode: dry-run\n")
		}
		if result.Err != nil {
			fmt.Fprintf(&builder, "  error: %v\n", result.Err)
		}
		if len(result.Commits) > 0 {
			builder.WriteString("  commits:\n")
			for _, commit := range result.Commits {
				fmt.Fprintf(&builder, "    %s %s\n", commit.Hash, commit.Subject)
			}
		}
		if len(result.Skipped) > 0 {
			builder.WriteString("  already_applied:\n")
			for _, commit := range result.Skipped {
				fmt.Fprintf(&builder, "    %s %s\n", commit.Hash, commit.Subject)
			}
		}
		if result.NoOp {
			builder.WriteString("  status: no-op (all commits already applied)\n")
		}
		if result.MergeURL != "" {
			fmt.Fprintf(&builder, "  merge_request: %s\n", result.MergeURL)
		}
		builder.WriteString("\n")
	}
	if _, err := file.WriteString(builder.String()); err != nil {
		return fmt.Errorf("write report %q: %w", r.path, err)
	}
	return nil
}
