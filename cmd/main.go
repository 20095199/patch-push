package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/patch-push/cli"
	"github.com/example/patch-push/repo"
)

// version is overridden at build time via -ldflags "-X main.version=<value>".
var version = "dev"

// PushCommand implements the "push" subcommand.
type PushCommand struct {
	repoNames   cli.StringList
	configPath  string
	branchModel string
	dryRun      bool
	yes         bool
	resume      bool
	abort       bool

	flags *flag.FlagSet
	name  string
	help  string
}

// NewPushCommand creates a push subcommand with the given name and help text.
func NewPushCommand(name, help string) *PushCommand {
	return &PushCommand{name: name, help: help}
}

func (c *PushCommand) Name() string { return c.name }
func (c *PushCommand) Help() string { return c.help }

func (c *PushCommand) SetupFlags(fs *flag.FlagSet) error {
	c.flags = fs
	fs.Var(&c.repoNames, "r", "要 push 的 repo 名称（config 中的 key），多个用 ',' 分隔")
	fs.StringVar(&c.configPath, "c", "config.yaml", "配置文件路径")
	fs.StringVar(&c.branchModel, "m", "n", "集成分支模式：n 重建（默认），a 复用已有分支")
	fs.BoolVar(&c.dryRun, "dry-run", false, "只打印计划和提交，不做任何修改或推送")
	fs.BoolVar(&c.yes, "yes", false, "非交互模式：遇到冲突时不询问，直接保存断点并退出（可用 -continue 继续）")
	fs.BoolVar(&c.resume, "continue", false, "从中断处继续（解决冲突后使用）")
	fs.BoolVar(&c.abort, "abort", false, "放弃上次中断的 cherry-pick")
	return nil
}

func (c *PushCommand) Run(ctx context.Context) error {
	config, err := repo.LoadConfig(c.configPath)
	if err != nil {
		return err
	}

	settings := repo.ResolvePushSettings(repo.PushSettings{
		Repos:    c.repoNames,
		Model:    c.branchModel,
		DryRun:   c.dryRun,
		Yes:      c.yes,
		Continue: c.resume,
		Abort:    c.abort,
	}, config, explicitFlags(c.flags))

	if err := validatePushSettings(settings); err != nil {
		return err
	}

	orchestrator := repo.NewOrchestrator(config, settings.Repos, repo.PushOptions{
		Model:      settings.Model,
		DryRun:     settings.DryRun,
		Yes:        settings.Yes,
		Continue:   settings.Continue,
		Abort:      settings.Abort,
		ConfigPath: c.configPath,
	})
	return orchestrator.Run(ctx)
}

// validatePushSettings enforces cross-field constraints that cannot be
// expressed by the flag package alone.
func validatePushSettings(settings repo.PushSettings) error {
	if settings.Model != "n" && settings.Model != "a" {
		return fmt.Errorf("invalid model %q: must be n (new) or a (append)", settings.Model)
	}
	if settings.Continue && settings.Abort {
		return errors.New("-continue and -abort are mutually exclusive")
	}
	if len(settings.Repos) == 0 && !settings.Continue && !settings.Abort {
		return errors.New("param repo is none, use -r or config.repos")
	}
	return nil
}

// PullCommand implements the "pull" subcommand.
type PullCommand struct {
	repoNames   cli.StringList
	configPath  string
	deleteLocal bool

	flags *flag.FlagSet
	name  string
	help  string
}

// NewPullCommand creates a pull subcommand with the given name and help text.
func NewPullCommand(name, help string) *PullCommand {
	return &PullCommand{name: name, help: help}
}

func (c *PullCommand) Name() string { return c.name }
func (c *PullCommand) Help() string { return c.help }

func (c *PullCommand) SetupFlags(fs *flag.FlagSet) error {
	c.flags = fs
	fs.Var(&c.repoNames, "r", "要 pull 的 repo 名称（config 中的 key），多个用 ',' 分隔")
	fs.StringVar(&c.configPath, "c", "config.yaml", "配置文件路径")
	fs.BoolVar(&c.deleteLocal, "d", false, "先删除本地目标分支，再基于 origin 重建")
	return nil
}

func (c *PullCommand) Run(ctx context.Context) error {
	config, err := repo.LoadConfig(c.configPath)
	if err != nil {
		return err
	}

	settings := repo.ResolvePullSettings(repo.PullSettings{
		Repos:  c.repoNames,
		Delete: c.deleteLocal,
	}, config, explicitFlags(c.flags))

	if len(settings.Repos) == 0 {
		return errors.New("param repo is none, use -r or config.repos")
	}
	repositories, err := config.SelectRepos(settings.Repos)
	if err != nil {
		return err
	}

	puller := repo.NewPuller(config, repositories, repo.PullOptions{Delete: settings.Delete})
	return puller.Run(ctx)
}

// explicitFlags reports which flags were actually set on the command line, so
// config defaults are only applied to flags the user did not pass explicitly.
func explicitFlags(fs *flag.FlagSet) map[string]bool {
	explicit := make(map[string]bool)
	if fs == nil {
		return explicit
	}
	fs.Visit(func(f *flag.Flag) {
		explicit[f.Name] = true
	})
	return explicit
}

// commandRegistry keeps subcommands in declaration order for usage output and
// offers name-based lookup for dispatch.
type commandRegistry struct {
	ordered []*cli.SubCommand
	byName  map[string]*cli.SubCommand
}

func newCommandRegistry(commands ...*cli.SubCommand) *commandRegistry {
	registry := &commandRegistry{
		ordered: commands,
		byName:  make(map[string]*cli.SubCommand, len(commands)),
	}
	for _, command := range commands {
		registry.byName[command.Name()] = command
	}
	return registry
}

func (r *commandRegistry) lookup(name string) (*cli.SubCommand, bool) {
	command, ok := r.byName[name]
	return command, ok
}

func usage(registry *commandRegistry) {
	fmt.Println("Usage: This is a tool for submit patch code")
	fmt.Println("1. Multiple repos can be submitted at once")
	fmt.Println("2. Can be submitted to multiple patches/branches at once")
	fmt.Println("3. Automatically generate merge request url")
	fmt.Println("You only need to modify the configuration file: config.yaml")
	fmt.Println()
	for _, command := range registry.ordered {
		fmt.Printf("%s %s\n", command.Name(), command.Help())
		command.PrintDefaults()
		fmt.Println()
	}
}

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	closer, err := repo.SetupLogging("patchpush.log")
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup logging:", err)
		return 1
	}
	defer func() { _ = closer.Close() }()

	registry := newCommandRegistry(
		cli.New(NewPushCommand("push", ": cherry-pick and push")),
		cli.New(NewPullCommand("pull", ": pull target branch")),
	)
	for _, command := range registry.ordered {
		if err := command.SetupFlags(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	if len(os.Args) < 2 {
		usage(registry)
		return 0
	}
	switch os.Args[1] {
	case "-version", "--version", "version":
		fmt.Println("patchpush", version)
		return 0
	case "-h", "--help", "help":
		usage(registry)
		return 0
	}

	command, ok := registry.lookup(os.Args[1])
	if !ok {
		usage(registry)
		return 2
	}
	if err := command.Run(ctx, os.Args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}
