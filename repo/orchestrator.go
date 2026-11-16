package repo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
)

// ErrInterrupted signals that a cherry-pick conflict stopped the run and the
// user can resume later with --continue.
var ErrInterrupted = errors.New("interrupted; resolve conflicts and re-run with --continue")

// PushOptions configures a push run.
type PushOptions struct {
	Model      string
	DryRun     bool
	Yes        bool
	Continue   bool
	Abort      bool
	ConfigPath string
	StatePath  string
	ReportPath string
	Out        io.Writer
	In         io.Reader
}

// Orchestrator coordinates a push run across repositories and target branches.
type Orchestrator struct {
	config    *Config
	repoNames []string
	options   PushOptions
	report    *Report
	state     *State
	statePath string
	prompter  Prompter
}

// NewOrchestrator builds an Orchestrator, applying defaults for optional paths
// and wiring a console prompter from the supplied input/output streams.
func NewOrchestrator(config *Config, repoNames []string, options PushOptions) *Orchestrator {
	if options.Out == nil {
		options.Out = os.Stdout
	}
	if options.In == nil {
		options.In = os.Stdin
	}
	if options.ReportPath == "" {
		options.ReportPath = "push_report.txt"
	}
	if options.StatePath == "" {
		options.StatePath = options.ConfigPath + ".state"
	}
	if options.Model == "" {
		options.Model = "n"
	}
	return &Orchestrator{
		config:    config,
		repoNames: repoNames,
		options:   options,
		report:    NewReport(options.ReportPath),
		statePath: options.StatePath,
		prompter:  NewConsolePrompter(options.In, options.Out),
	}
}

// Run dispatches to abort, resume or a fresh start based on the options.
func (o *Orchestrator) Run(ctx context.Context) error {
	switch {
	case o.options.Abort:
		return o.abort(ctx)
	case o.options.Continue:
		return o.resume(ctx)
	default:
		return o.start(ctx)
	}
}

func (o *Orchestrator) start(ctx context.Context) error {
	repositories, err := o.config.SelectRepos(o.repoNames)
	if err != nil {
		return err
	}
	o.state = &State{ConfigPath: o.options.ConfigPath, RepoKeys: o.repoNames, Model: o.options.Model}
	if err := SaveState(o.statePath, o.state); err != nil {
		return err
	}
	return o.execute(ctx, repositories)
}

func (o *Orchestrator) resume(ctx context.Context) error {
	state, err := LoadState(o.statePath)
	if err != nil {
		return err
	}
	if state == nil {
		return errors.New("no interrupted run found")
	}
	o.state = state
	if state.Model != "" {
		o.options.Model = state.Model
	}
	repositories, err := o.config.SelectRepos(state.RepoKeys)
	if err != nil {
		return err
	}
	o.printf("resuming interrupted run from %s\n", state.ConfigPath)
	return o.execute(ctx, repositories)
}

func (o *Orchestrator) abort(ctx context.Context) error {
	state, err := LoadState(o.statePath)
	if err != nil {
		return err
	}
	if state == nil || state.Active == nil {
		if err := ClearState(o.statePath); err != nil {
			return err
		}
		o.printf("nothing to abort\n")
		return nil
	}

	job := state.Active
	gitRepo := NewGitRepo(job.RepoPath, "")
	if repository := o.config.Repo[job.RepoName]; repository != nil {
		gitRepo = NewGitRepo(repository.Path, repository.URL)
	}
	if gitRepo.IsCherryPickInProgress(ctx) {
		if err := gitRepo.AbortCherryPick(ctx); err != nil {
			return err
		}
	}
	o.printf("aborted interrupted job %s -> %s\n", job.NewBranch, job.TargetBranch)
	return ClearState(o.statePath)
}

func (o *Orchestrator) execute(ctx context.Context, repositories []*Repo) error {
	var errs []error

	if o.state.Active != nil {
		job := o.state.Active
		result, err := o.resumeJob(ctx, o.repoForJob(job), job)
		o.report.Add(result)
		if err != nil {
			if errors.Is(err, ErrInterrupted) {
				return o.stop(err)
			}
			errs = append(errs, err)
			o.state.Active = nil
			if saveErr := SaveState(o.statePath, o.state); saveErr != nil {
				log.Printf("save state failed: %v", saveErr)
			}
		} else {
			o.markCompleted(job.RepoName, job.TargetBranch)
		}
	}

	for _, repository := range repositories {
		for _, targetBranch := range o.config.Patch.TargetBranches {
			if o.isCompleted(repository.Name, targetBranch) {
				continue
			}
			result, err := o.runJob(ctx, repository, targetBranch)
			o.report.Add(result)
			if err != nil {
				if errors.Is(err, ErrInterrupted) {
					return o.stop(err)
				}
				errs = append(errs, fmt.Errorf("%s -> %s: %w", repository.Name, targetBranch, err))
				log.Printf("job failed: repo=%s target=%s: %v", repository.Name, targetBranch, err)
				continue
			}
			o.markCompleted(repository.Name, targetBranch)
		}
	}

	o.report.Print(o.options.Out)
	if err := o.report.Write(); err != nil {
		errs = append(errs, err)
	}
	if err := ClearState(o.statePath); err != nil {
		log.Printf("clear state failed: %v", err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (o *Orchestrator) stop(err error) error {
	o.report.Print(o.options.Out)
	if writeErr := o.report.Write(); writeErr != nil {
		log.Printf("write report failed: %v", writeErr)
	}
	o.printf("\nrun interrupted. resolve the conflict, then re-run with the same arguments plus --continue\n")
	return err
}

func (o *Orchestrator) isCompleted(repoName, targetBranch string) bool {
	key := JobKey(repoName, targetBranch)
	for _, completed := range o.state.Completed {
		if completed == key {
			return true
		}
	}
	return false
}

func (o *Orchestrator) markCompleted(repoName, targetBranch string) {
	if !o.isCompleted(repoName, targetBranch) {
		o.state.Completed = append(o.state.Completed, JobKey(repoName, targetBranch))
	}
	o.state.Active = nil
	if err := SaveState(o.statePath, o.state); err != nil {
		log.Printf("save state failed: %v", err)
	}
}

// repoForJob finds the configured repo for a persisted job, falling back to a
// path-only repo so resume still works if the config changed.
func (o *Orchestrator) repoForJob(job *JobState) *Repo {
	if repository, ok := o.config.Repo[job.RepoName]; ok {
		return repository
	}
	return &Repo{Name: job.RepoName, Path: job.RepoPath}
}

func (o *Orchestrator) printf(format string, args ...any) {
	fmt.Fprintf(o.options.Out, format, args...)
}
