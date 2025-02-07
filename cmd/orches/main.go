package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"github.com/orches-team/orches/pkg/git"
	"github.com/orches-team/orches/pkg/syncer"
	"github.com/orches-team/orches/pkg/unit"
	"github.com/orches-team/orches/pkg/utils"
	"github.com/spf13/cobra"
)

var baseDir string

func init() {
	if isUser() {
		dir, err := os.UserHomeDir()
		if err != nil {
			panic(fmt.Sprintf("failed to get user home directory: %v", err))
		}
		// TODO: RESPECT XDG
		baseDir = path.Join(dir, ".config", "orches")
	} else {
		baseDir = "/var/lib/orches"
	}
}

type rootFlags struct {
	dryRun bool
}

func getRootFlags(cmd *cobra.Command) rootFlags {
	dryRun, _ := cmd.Flags().GetBool("dry")
	return rootFlags{dryRun: dryRun}
}

func isUser() bool {
	return os.Getuid() != 0
}

func main() {
	var rootCmd = &cobra.Command{
		Use: "orches",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			level := slog.LevelWarn
			verboseLevel, _ := cmd.Flags().GetCount("verbose")
			if verboseLevel == 1 {
				level = slog.LevelInfo
			} else if verboseLevel > 1 {
				level = slog.LevelDebug
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: level,
			}))
			slog.SetDefault(logger)

			slog.Info("Verbose", "level", verboseLevel)
		},
	}
	rootCmd.PersistentFlags().Bool("dry", false, "Dry run")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbose output")

	var initCmd = &cobra.Command{
		Use:   "init [remote]",
		Short: "Initialize by cloning a repo and setting up state.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := initRepo(args[0], getRootFlags(cmd)); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			return nil
		},
	}

	var syncCmd = &cobra.Command{
		Use:   "sync",
		Short: "Sync deployments",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdSync(getRootFlags(cmd)); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			return nil
		},
	}

	var pruneCmd = &cobra.Command{
		Use:   "prune",
		Short: "Prune deployments",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdPrune(getRootFlags(cmd)); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			return nil
		},
	}

	var runCmd = &cobra.Command{
		Use:   "run",
		Short: "Periodically sync deployments",
		RunE: func(cmd *cobra.Command, args []string) error {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

			for {
				if err := cmdSync(getRootFlags(cmd)); err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
				}
				select {
				case <-sig:
					fmt.Println("Received signal, exiting.")
					return nil
				case <-time.After(1 * time.Minute):
				}
			}
		},
	}

	var switchCmd = &cobra.Command{
		Use:   "switch",
		Short: "Switch to a different deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdSwitch(args[0], getRootFlags(cmd)); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			return nil
		},
	}

	rootCmd.AddCommand(initCmd, syncCmd, pruneCmd, runCmd, switchCmd)
	rootCmd.Execute()
}

func initRepo(remote string, flags rootFlags) error {
	repoPath := filepath.Join(baseDir, "repo")

	if _, err := os.Stat(baseDir); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("repository already exists at %s", repoPath)
	}

	if _, err := git.Clone(remote, repoPath); err != nil {
		return fmt.Errorf("failed to clone repo: %w", err)
	}

	blank, err := os.MkdirTemp("", "orches-initial-sync-")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(blank)

	if err := syncDirs(blank, repoPath, flags.dryRun); err != nil {
		return fmt.Errorf("failed to sync directories: %w", err)
	}

	if flags.dryRun {
		if err := os.RemoveAll(baseDir); err != nil {
			return fmt.Errorf("failed to remove directory: %w", err)
		}
	}
	return nil
}

func processChanges(newDir string, added, removed, modified []unit.Unit, dryRun bool) error {
	if len(added) == 0 && len(removed) == 0 && len(modified) == 0 {
		fmt.Println("No changes to process.")
		return nil
	}

	if len(added) > 0 {
		fmt.Printf("Added: %v\n", utils.MapSlice(added, func(u unit.Unit) string { return u.Name() }))
	}
	if len(removed) > 0 {
		fmt.Printf("Removed: %v\n", utils.MapSlice(removed, func(u unit.Unit) string { return u.Name() }))
	}
	if len(modified) > 0 {
		fmt.Printf("Modified: %v\n", utils.MapSlice(modified, func(u unit.Unit) string { return u.Name() }))
	}

	s := syncer.Syncer{
		Dry:  dryRun,
		User: isUser(),
	}

	if err := s.CreateDirs(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	if err := s.StopUnits(removed); err != nil {
		return fmt.Errorf("failed to stop unit: %w", err)
	}

	if err := s.Remove(removed); err != nil {
		return fmt.Errorf("failed to remove unit: %w", err)
	}

	if err := s.Add(newDir, append(added, modified...)); err != nil {
		return fmt.Errorf("failed to add unit: %w", err)
	}

	if err := s.ReloadDaemon(); err != nil {
		return fmt.Errorf("failed to reload daemon: %w", err)
	}

	if err := s.RestartUnits(modified); err != nil {
		return fmt.Errorf("failed to restart unit: %w", err)
	}

	if err := s.StartUnits(append(added, modified...)); err != nil {
		return fmt.Errorf("failed to start unit: %w", err)
	}

	return nil
}

func syncDirs(old, new string, dryRun bool) error {
	oldUnits, err := listUnits(old)
	if err != nil {
		return fmt.Errorf("failed to list old files: %w", err)
	}

	newUnits, err := listUnits(new)
	if err != nil {
		return fmt.Errorf("failed to list new files: %w", err)
	}

	added, removed, modified := diffUnits(oldUnits, newUnits)

	if err := processChanges(new, added, removed, modified, dryRun); err != nil {
		return fmt.Errorf("failed to process changes: %w", err)
	}

	return nil
}

func cmdSync(flags rootFlags) error {
	// TODO: implement locking
	repoDir := filepath.Join(baseDir, "repo")

	repo := git.Repo{Path: repoDir}
	oldState, err := repo.NewWorktree("HEAD")
	if err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}
	defer oldState.Cleanup()

	beforeRef, err := repo.Ref("HEAD")
	if err != nil {
		return fmt.Errorf("failed to get ref: %w", err)
	}

	remoteURL, err := repo.RemoteURL("origin")
	if err != nil {
		return fmt.Errorf("failed to get remote URL: %w", err)
	}

	fmt.Printf("Pulling from %s\n", remoteURL)

	if err := repo.Pull(); err != nil {
		return fmt.Errorf("failed to pull repo: %w", err)
	}

	afterRef, err := repo.Ref("HEAD")
	if err != nil {
		return fmt.Errorf("failed to get ref: %w", err)
	}

	if beforeRef == afterRef {
		fmt.Println("No new commits to sync.")
		return nil
	}

	newState, err := repo.NewWorktree("HEAD")
	if err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}
	defer newState.Cleanup()

	fmt.Printf("Syncing %s..%s\n", beforeRef, afterRef)

	if flags.dryRun {
		if err := repo.Reset(beforeRef); err != nil {
			return fmt.Errorf("dry run error: failed to reset repo: %w", err)
		}
	}

	if err := syncDirs(oldState.Path, newState.Path, flags.dryRun); err != nil {
		return fmt.Errorf("failed to sync directories: %w", err)
	}

	fmt.Printf("Synced to %s\n", afterRef)

	return nil
}

func cmdPrune(flags rootFlags) error {
	blank, err := os.MkdirTemp("", "orches-prune-")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(blank)

	repoDir := filepath.Join(baseDir, "repo")
	if err := syncDirs(repoDir, blank, flags.dryRun); err != nil {
		return fmt.Errorf("failed to sync directories: %w", err)
	}

	if flags.dryRun {
		fmt.Printf("Remove %s\n", repoDir)
		return nil
	}

	if err := os.RemoveAll(baseDir); err != nil {
		return fmt.Errorf("failed to remove directory: %w", err)
	}

	return nil
}

func cmdSwitch(remote string, flags rootFlags) error {
	repoDir := path.Join(baseDir, "repo")

	newRepo, err := os.MkdirTemp(baseDir, "orches-switch-")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}

	if _, err := git.Clone(remote, newRepo); err != nil {
		return fmt.Errorf("failed to clone repo: %w", err)
	}
	defer os.RemoveAll(newRepo)

	if err := syncDirs(repoDir, newRepo, flags.dryRun); err != nil {
		return fmt.Errorf("failed to sync directories: %w", err)
	}

	if flags.dryRun {
		return nil
	}

	if err := os.RemoveAll(repoDir); err != nil {
		return fmt.Errorf("failed to remove directory: %w", err)
	}

	if err := os.Rename(newRepo, repoDir); err != nil {
		return fmt.Errorf("failed to rename directory: %w", err)
	}

	return nil
}

func listUnits(dir string) (map[string]unit.Unit, error) {
	files := make(map[string]unit.Unit)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		u, err := unit.New(dir, entry.Name())
		var e *unit.ErrUnknownUnitType
		if errors.As(err, &e) {
			slog.Info("Skipping unknown unit type", "unit", entry.Name())
			continue
		} else if err != nil {
			return nil, err
		}

		files[entry.Name()] = u
	}
	return files, err
}

func diffUnits(old, new map[string]unit.Unit) (added, removed, changed []unit.Unit) {
	for file, u := range old {
		if _, exists := new[file]; !exists {
			removed = append(removed, u)
		}
	}
	for file, u := range new {
		if _, exists := old[file]; !exists {
			added = append(added, u)
		}
	}
	for file, u := range new {
		if oldU, exists := old[file]; exists && !u.EqualContent(oldU) {
			changed = append(changed, u)
		}
	}

	return
}
