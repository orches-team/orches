package git

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/orches-team/orches/pkg/utils"
)

type Repo struct {
	Path string
}

func Clone(remote, path string) (*Repo, error) {
	if err := utils.ExecNoOutput("git", "clone", remote, path); err != nil {
		return nil, fmt.Errorf("failed to clone repo: %w", err)
	}

	return &Repo{Path: path}, nil
}

func (r *Repo) Fetch(remote string) error {
	return utils.ExecNoOutput("git", "-C", r.Path, "fetch", remote)
}

func (r *Repo) Ref(ref string) (string, error) {
	out, err := utils.ExecOutput("git", "-C", r.Path, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("failed to get ref: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

func (r *Repo) Reset(ref string) error {
	return utils.ExecNoOutput("git", "-C", r.Path, "reset", "--hard", ref)
}

func (r *Repo) RemoteURL(remote string) (string, error) {
	out, err := utils.ExecOutput("git", "-C", r.Path, "remote", "get-url", remote)
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

type worktree struct {
	Path string

	repo *Repo
}

func (r *Repo) NewWorktree(ref string) (*worktree, error) {
	worktreeDir, err := os.MkdirTemp("", "orches-worktree-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}

	if err := utils.ExecNoOutput("git", "-C", r.Path, "worktree", "add", worktreeDir, ref); err != nil {
		return nil, fmt.Errorf("failed to create worktree: %w", err)
	}

	return &worktree{repo: r, Path: worktreeDir}, nil
}

func (wt *worktree) Cleanup() error {
	var errs []error
	errs = append(errs, utils.ExecNoOutput("git", "-C", wt.repo.Path, "worktree", "remove", wt.Path))
	errs = append(errs, os.RemoveAll(wt.Path))

	return errors.Join(errs...)
}
