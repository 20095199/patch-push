package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st := &State{
		ConfigPath: "config.yaml.1",
		RepoKeys:   []string{"bot", "common"},
		Model:      "n",
		Completed:  []string{JobKey("bot", "qa")},
		Active: &JobState{
			RepoName:     "common",
			RepoPath:     "/tmp/common",
			DevBranch:    "dev",
			TargetBranch: "qa",
			NewBranch:    "ABC-1_x_qa",
			Commits:      []Commit{{Hash: "abc", Subject: "ABC-1: x"}},
			Next:         1,
		},
	}
	if err := SaveState(path, st); err != nil {
		t.Fatal(err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("state is nil")
	}
	if got.Model != "n" || got.Active == nil || got.Active.Next != 1 {
		t.Fatalf("unexpected state: %+v", got)
	}
	if got.Active.Commits[0].Hash != "abc" {
		t.Fatalf("commits not persisted: %+v", got.Active.Commits)
	}

	if err := ClearState(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state should be removed, err=%v", err)
	}
	got, err = LoadState(path)
	if err != nil || got != nil {
		t.Fatalf("load after clear = %+v, %v", got, err)
	}
}
