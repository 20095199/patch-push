package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const DefaultTimeout = 5 * time.Minute

// ErrCommandTimeout indicates a command exceeded its execution deadline.
var ErrCommandTimeout = errors.New("command timed out")

// Runner abstracts command execution so git interactions can be tested.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (stdout, stderr string, err error)
}

// ExecRunner executes real commands with a timeout and a non-interactive git environment.
type ExecRunner struct {
	Timeout time.Duration
	Env     []string
}

// NewExecRunner builds an ExecRunner with the given per-command timeout.
func NewExecRunner(timeout time.Duration) *ExecRunner {
	return &ExecRunner{Timeout: timeout}
}

// Run executes name with args in dir, enforcing Timeout and disabling prompts.
func (r *ExecRunner) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.WaitDelay = 2 * time.Second

	baseEnv := r.Env
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	cmd.Env = append(baseEnv,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_MERGE_AUTOEDIT=no",
		"GIT_EDITOR=true",
		"GCM_INTERACTIVE=never",
		"LC_ALL=C",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("%w: %s %s (after %s)",
			ErrCommandTimeout, name, strings.Join(args, " "), r.Timeout)
	}
	if err != nil {
		return stdout.String(), stderr.String(), &CommandError{
			Command: name,
			Args:    args,
			Stderr:  strings.TrimSpace(stderr.String()),
			Err:     err,
		}
	}
	return stdout.String(), stderr.String(), nil
}

// CommandError reports the command, arguments and stderr of a failed execution.
type CommandError struct {
	Command string
	Args    []string
	Stderr  string
	Err     error
}

func (e *CommandError) Error() string {
	msg := fmt.Sprintf("%s %s: %v", e.Command, strings.Join(e.Args, " "), e.Err)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

// Unwrap exposes the underlying error for errors.Is/As.
func (e *CommandError) Unwrap() error { return e.Err }
