package repo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	r := NewReport(path)
	r.Add(Result{
		RepoName:     "bot",
		RepoPath:     "/tmp/bot",
		DevBranch:    "dev",
		TargetBranch: "qa",
		NewBranch:    "ABC-1_x_qa",
		Commits:      []Commit{{Hash: "abc123", Subject: "ABC-1: fix"}},
		MergeURL:     "https://example.com/mr",
	})
	if err := r.Write(); err != nil {
		t.Fatal(err)
	}
	if err := r.Write(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if strings.Count(body, "=====") != 4 {
		t.Fatalf("expected two timestamped blocks, got:\n%s", body)
	}
	if !strings.Contains(body, "merge_request: https://example.com/mr") {
		t.Fatalf("missing merge url:\n%s", body)
	}
}

func TestReportPrintSummary(t *testing.T) {
	r := NewReport("unused")
	r.Add(Result{
		RepoName:     "bot",
		DevBranch:    "dev",
		TargetBranch: "release-1.0",
		NewBranch:    "ABC-1_x_release-1.0",
		Commits:      []Commit{{Hash: "e3c57b0c", Subject: "ABC-1: fix mig"}},
		MergeURL:     "https://git.example.com/g/p/-/merge_requests/new?merge_request%5Btarget_branch%5D=release-1.0",
	})
	r.Add(Result{
		RepoName:     "bot",
		DevBranch:    "dev",
		TargetBranch: "qa",
		NewBranch:    "ABC-1_x_qa",
		Err:          errNoCommits,
	})

	var out strings.Builder
	r.Print(&out)
	body := out.String()

	rule := strings.Repeat("=", outputRuleWidth)
	if !strings.HasPrefix(body, "\n"+rule+"\n") {
		t.Fatalf("summary should start with a full rule:\n%s", body)
	}
	if !strings.Contains(body, "RESULT SUMMARY") || !strings.Contains(body, "(total 2)") {
		t.Fatalf("missing summary header:\n%s", body)
	}
	if !strings.Contains(body, "[OK     ] 1/2") || !strings.Contains(body, "[FAILED ] 2/2") {
		t.Fatalf("missing per-result status lines:\n%s", body)
	}
	if !strings.Contains(body, "ok: 1  |  no-op: 0  |  failed: 1  |  dry-run: 0  |  total: 2") {
		t.Fatalf("missing totals line:\n%s", body)
	}
	if !strings.HasSuffix(body, rule+"\n") {
		t.Fatalf("summary should end with a full rule:\n%s", body)
	}
}

var errNoCommits = errors.New("no matching commits")

func TestRotatingWriterRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	w, err := NewRotatingWriter(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if _, err := w.Write([]byte("12345678\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("abcdefgh\n")); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated file: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "abcdefgh") {
		t.Fatalf("current log missing last write: %q", data)
	}
}
