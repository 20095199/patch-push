package repo

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// ConflictAction is the operator's decision for a cherry-pick conflict.
type ConflictAction int

const (
	// ConflictResolved means the user resolved the conflict and the pick can continue.
	ConflictResolved ConflictAction = iota
	// ConflictSkip means the conflicting commit should be skipped.
	ConflictSkip
	// ConflictStop means stop the run and let the user resume later.
	ConflictStop
)

// Prompter abstracts interactive questions so orchestration can be driven by a
// terminal or by an alternative front-end.
type Prompter interface {
	ResolveConflict(commit Commit) (ConflictAction, error)
}

// ConsolePrompter reads answers from a reader and writes prompts to a writer.
type ConsolePrompter struct {
	out    io.Writer
	reader *bufio.Reader
}

// NewConsolePrompter builds a ConsolePrompter, defaulting nil streams to stdin/stdout.
func NewConsolePrompter(in io.Reader, out io.Writer) *ConsolePrompter {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	return &ConsolePrompter{out: out, reader: bufio.NewReader(in)}
}

// ResolveConflict explains the conflict options and returns the chosen action.
func (p *ConsolePrompter) ResolveConflict(commit Commit) (ConflictAction, error) {
	p.printf("\ncherry-pick conflict on %s %s\n", commit.Hash, commit.Subject)
	p.printf("  y: conflict resolved (continues cherry-pick)\n")
	p.printf("  s: skip this commit\n")
	p.printf("  n: stop and resume later with --continue\n")
	choice, err := p.readChoice("choice [y/s/n]: ", "ysn")
	if err != nil {
		return ConflictStop, err
	}
	switch choice {
	case 'y':
		return ConflictResolved, nil
	case 's':
		return ConflictSkip, nil
	default:
		return ConflictStop, nil
	}
}

func (p *ConsolePrompter) readChoice(prompt, allowed string) (rune, error) {
	for {
		fmt.Fprint(p.out, prompt)
		line, err := p.reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if err != nil && line == "" {
			return 0, fmt.Errorf("stdin is closed, use --yes to run non-interactively")
		}
		if line == "" {
			continue
		}
		choice := []rune(line)[0]
		if strings.ContainsRune(allowed, choice) {
			return choice, nil
		}
	}
}

func (p *ConsolePrompter) printf(format string, args ...any) {
	fmt.Fprintf(p.out, format, args...)
}
