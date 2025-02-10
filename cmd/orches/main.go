package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/orches-team/orches/pkg/git"
	"github.com/orches-team/orches/pkg/syncer"
	"github.com/spf13/cobra"
)

var baseDir string

func init() {
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		baseDir = "/var/lib/orches"
	} else if os.Getuid() != 0 {
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
			if _, err := cmdSync(getRootFlags(cmd)); err != nil {
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

			syncInterval, err := cmd.Flags().GetInt("interval")
			if err != nil {
				return err
			}

			for {
				res, err := cmdSync(getRootFlags(cmd))
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
				}

				if res != nil && res.RestartNeeded {
					fmt.Println("Restart needed, exiting.")
					return nil
				}

				select {
				case <-sig:
					fmt.Println("Received signal, exiting.")
					return nil
				case <-time.After(time.Duration(syncInterval) * time.Second):
				}
			}
		},
	}

	runCmd.Flags().Int("interval", 120, "Interval in seconds")

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

	var versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			info, ok := debug.ReadBuildInfo()
			if !ok {
				fmt.Println("No build info available")
				os.Exit(1)
			}

			buildinfo := struct {
				ref  string
				time string
			}{
				ref:  "unknown",
				time: "unknown",
			}

			for _, val := range info.Settings {
				switch val.Key {
				case "vcs.revision":
					buildinfo.ref = val.Value
				case "vcs.time":
					buildinfo.time = val.Value
				}
			}

			fmt.Printf("gitref: %s\n", buildinfo.ref)
			fmt.Printf("buildtime: %s\n", buildinfo.time)
		},
	}

	rootCmd.AddCommand(initCmd, syncCmd, pruneCmd, runCmd, switchCmd, versionCmd)
	if rootCmd.Execute() != nil {
		os.Exit(1)
	}
}

func lock(fn func() error) error {
	os.Mkdir(baseDir, 0755)

	slog.Debug("Adding interrupt signal handler in lock()")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	finished := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)

	defer func() {
		signal.Stop(sig)
		close(finished)
		wg.Wait()
	}()

	go func() {
		for {
			select {
			case <-sig:
				fmt.Println("Received signal while running, ignoring.")
			case <-finished:
				slog.Debug("Closing signal goroutine in lock()")
				wg.Done()
				return
			}
		}
	}()

	var f *os.File
	var err error
	for {
		f, err = os.OpenFile(path.Join(baseDir, "lock"), os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			break
		}
		slog.Debug("Failed to acquire lock, retrying", "error", err)
		time.Sleep(100 * time.Millisecond)
	}
	slog.Debug("Acquired lock")

	defer f.Close()
	defer func() {
		err := os.Remove(f.Name())
		if err != nil {
			slog.Error("Failed to remove lock file", "error", err)
		}
		slog.Debug("Removed lock")
	}()

	return fn()
}

func initRepo(remote string, flags rootFlags) error {
	return lock(func() error {
		repoPath := filepath.Join(baseDir, "repo")

		if _, err := os.Stat(repoPath); !errors.Is(err, os.ErrNotExist) {
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

		if _, err := syncer.SyncDirs(blank, repoPath, flags.dryRun); err != nil {
			return fmt.Errorf("failed to sync directories: %w", err)
		}

		if flags.dryRun {
			if err := os.RemoveAll(baseDir); err != nil {
				return fmt.Errorf("failed to remove directory: %w", err)
			}
		}
		return nil
	})
}

func cmdSync(flags rootFlags) (*syncer.SyncResult, error) {
	var res *syncer.SyncResult

	err := lock(func() error {
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

		res, err = syncer.SyncDirs(oldState.Path, newState.Path, flags.dryRun)

		if err != nil {
			return fmt.Errorf("failed to sync directories: %w", err)
		}

		fmt.Printf("Synced to %s\n", afterRef)

		return nil
	})
	return res, err
}

func cmdPrune(flags rootFlags) error {
	return lock(func() error {
		blank, err := os.MkdirTemp("", "orches-prune-")
		if err != nil {
			return fmt.Errorf("failed to create temporary directory: %w", err)
		}
		defer os.RemoveAll(blank)

		repoDir := filepath.Join(baseDir, "repo")
		if _, err := syncer.SyncDirs(repoDir, blank, flags.dryRun); err != nil {
			return fmt.Errorf("failed to sync directories: %w", err)
		}

		if flags.dryRun {
			fmt.Printf("Remove %s\n", repoDir)
			return nil
		}

		if err := os.RemoveAll(repoDir); err != nil {
			return fmt.Errorf("failed to remove directory: %w", err)
		}

		return nil
	})
}

func cmdSwitch(remote string, flags rootFlags) error {
	return lock(func() error {
		repoDir := path.Join(baseDir, "repo")

		newRepo, err := os.MkdirTemp(baseDir, "orches-switch-")
		if err != nil {
			return fmt.Errorf("failed to create temporary directory: %w", err)
		}

		if _, err := git.Clone(remote, newRepo); err != nil {
			return fmt.Errorf("failed to clone repo: %w", err)
		}
		defer os.RemoveAll(newRepo)

		if _, err := syncer.SyncDirs(repoDir, newRepo, flags.dryRun); err != nil {
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
	})
}
