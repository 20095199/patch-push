package repo

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExecRunnerReturnsOutputAndError(t *testing.T) {
	r := NewExecRunner(5 * time.Second)
	stdout, stderr, err := r.Run(context.Background(), "", "sh", "-c", "printf out; printf err >&2; exit 3")
	if err == nil {
		t.Fatal("expected error")
	}
	if stdout != "out" || stderr != "err" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("expected CommandError, got %T", err)
	}
	if cmdErr.Stderr != "err" {
		t.Fatalf("CommandError.Stderr = %q", cmdErr.Stderr)
	}
}

func TestExecRunnerTimeout(t *testing.T) {
	r := NewExecRunner(50 * time.Millisecond)
	_, _, err := r.Run(context.Background(), "", "sh", "-c", "exec sleep 2")
	if !errors.Is(err, ErrCommandTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestExecRunnerSuccess(t *testing.T) {
	r := NewExecRunner(time.Second)
	stdout, stderr, err := r.Run(context.Background(), "", "sh", "-c", "printf hello")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "hello" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}
