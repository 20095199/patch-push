package repo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// PullOptions configures a pull run.
type PullOptions struct {
	Delete bool
	Out    io.Writer
}

// Puller refreshes target branches for a set of repositories.
type Puller struct {
	config            *Config
	repos             []*Repo
	deleteLocalBranch bool
	output            io.Writer
}

// NewPuller builds a Puller, defaulting the output stream to stdout.
func NewPuller(config *Config, repos []*Repo, options PullOptions) *Puller {
	if options.Out == nil {
		options.Out = os.Stdout
	}
	return &Puller{
		config:            config,
		repos:             repos,
		deleteLocalBranch: options.Delete,
		output:            options.Out,
	}
}

// Run pulls every configured target branch for every repository, aggregating errors.
func (p *Puller) Run(ctx context.Context) error {
	var errs []error
	for _, repository := range p.repos {
		gitRepo := NewGitRepo(repository.Path, repository.URL)
		if err := gitRepo.EnsureRepository(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", repository.Name, err))
			continue
		}
		if err := gitRepo.CheckRemoteAccess(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: cannot reach origin: %w", repository.Name, err))
			continue
		}
		for _, targetBranch := range p.config.Patch.TargetBranches {
			if err := p.pullBranch(ctx, gitRepo, targetBranch); err != nil {
				errs = append(errs, fmt.Errorf("%s -> %s: %w", repository.Name, targetBranch, err))
				continue
			}
			fmt.Fprintf(p.output, "pulled %s: %s\n", repository.Name, targetBranch)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (p *Puller) pullBranch(ctx context.Context, gitRepo *GitRepo, targetBranch string) error {
	if targetBranch == p.config.Patch.DevBranch {
		return fmt.Errorf("refusing to pull over the dev branch %q", targetBranch)
	}

	if p.deleteLocalBranch {
		clean, status, err := gitRepo.WorktreeClean(ctx)
		if err != nil {
			return err
		}
		if !clean {
			return fmt.Errorf("working tree is not clean; commit or stash changes first:\n%s", status)
		}
	}

	exists, err := gitRepo.HasBranch(ctx, targetBranch)
	if err != nil {
		return err
	}

	if p.deleteLocalBranch && exists {
		if err := gitRepo.CheckoutDetached(ctx); err != nil {
			return err
		}
		if err := gitRepo.DeleteLocalBranchForce(ctx, targetBranch); err != nil {
			return err
		}
		exists = false
	}

	if !exists {
		remoteExists, _, err := gitRepo.HasRemoteBranch(ctx, targetBranch)
		if err != nil {
			return err
		}
		if !remoteExists {
			return fmt.Errorf("target branch %q not found on origin", targetBranch)
		}
		if err := gitRepo.Fetch(ctx); err != nil {
			return err
		}
		return gitRepo.CreateBranchFromRemote(ctx, targetBranch)
	}

	if err := gitRepo.Checkout(ctx, targetBranch); err != nil {
		return err
	}
	return gitRepo.PullFastForwardOnly(ctx)
}
