package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

const stateVersion = 1

// JobState is the resumable progress of one repo/target-branch cherry-pick job.
type JobState struct {
	RepoName     string   `json:"repo_name"`
	RepoPath     string   `json:"repo_path"`
	DevBranch    string   `json:"dev_branch"`
	TargetBranch string   `json:"tgt_branch"`
	NewBranch    string   `json:"new_branch"`
	Commits      []Commit `json:"commits"`
	Next         int      `json:"next"`
	Pending      bool     `json:"pending,omitempty"`
}

// State is the persisted progress of an entire push run.
type State struct {
	Version    int       `json:"version"`
	ConfigPath string    `json:"config_path"`
	RepoKeys   []string  `json:"repo_keys"`
	Model      string    `json:"model"`
	Completed  []string  `json:"completed"`
	Active     *JobState `json:"active,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// JobKey returns the stable key used to record a completed repo/branch pair.
func JobKey(repoName, targetBranch string) string {
	return repoName + "|" + targetBranch
}

// LoadState reads state from path. A missing file returns (nil, nil) so callers
// can distinguish "no saved run" from a real error.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state %q: %w", path, err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state %q: %w", path, err)
	}
	if state.Version != stateVersion {
		return nil, fmt.Errorf("unsupported state version %d", state.Version)
	}
	return &state, nil
}

// SaveState atomically writes state to path.
func SaveState(path string, state *State) error {
	if state == nil {
		return nil
	}
	state.Version = stateVersion
	state.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write state %q: %w", path, err)
	}
	return os.Rename(tmp, path)
}

// ClearState removes the state file, treating a missing file as success.
func ClearState(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
