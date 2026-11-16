// Package cli provides a small subcommand framework: each command owns its
// flag definitions and execution, while SubCommand handles flag parsing and
// name/help wiring.
package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"
)

// Command is a subcommand that defines its own flags and runs an action.
type Command interface {
	// SetupFlags registers the command's flags on fs.
	SetupFlags(fs *flag.FlagSet) error
	// Run executes the command after its flags have been parsed.
	Run(ctx context.Context) error
	// Name returns the subcommand name used on the command line.
	Name() string
	// Help returns a one-line description shown in the usage text.
	Help() string
}

// StringList is a flag.Value that accumulates comma-separated values.
// Repeating the flag appends to the existing list.
type StringList []string

func (s *StringList) String() string {
	return fmt.Sprint([]string(*s))
}

// Set parses a comma-separated value such as "a,b,c", trimming whitespace and
// dropping empty entries.
func (s *StringList) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			*s = append(*s, item)
		}
	}
	return nil
}

// SubCommand binds a Command to a FlagSet and knows how to parse its arguments.
type SubCommand struct {
	flags   *flag.FlagSet
	command Command
}

// New creates a SubCommand for command using a ContinueOnError flag set.
func New(command Command) *SubCommand {
	return &SubCommand{
		flags:   flag.NewFlagSet(command.Name(), flag.ContinueOnError),
		command: command,
	}
}

// SetupFlags asks the underlying command to register its flags.
func (s *SubCommand) SetupFlags() error {
	return s.command.SetupFlags(s.flags)
}

// Run parses args (already stripped of the program and subcommand names) and
// then executes the command.
func (s *SubCommand) Run(ctx context.Context, args []string) error {
	if err := s.flags.Parse(args); err != nil {
		return err
	}
	return s.command.Run(ctx)
}

// Name returns the subcommand name.
func (s *SubCommand) Name() string { return s.command.Name() }

// Help returns the subcommand description.
func (s *SubCommand) Help() string { return s.command.Help() }

// PrintDefaults prints the flag defaults to stderr, matching flag.FlagSet.
func (s *SubCommand) PrintDefaults() { s.flags.PrintDefaults() }
