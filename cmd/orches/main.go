package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/orches-team/orches/pkg/git"
	"github.com/orches-team/orches/pkg/syncer"
	"github.com/spf13/cobra"
)

const version = "0.1.1-dev"

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

type daemonCommand struct {
	Name string `json:"name"`
	Arg  string `json:"arg"`
}

func handleConnection(sock net.Listener, cmdChan chan<- daemonCommand, resultChan <-chan string) error {
	conn, err := sock.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()

	var cmd daemonCommand
	if err := json.NewDecoder(conn).Decode(&cmd); err != nil {
		return err
	}

	slog.Debug("Received command", "name", cmd.Name, "arg", cmd.Arg)

	cmdChan <- cmd
	status := <-resultChan
	_, err = io.Copy(conn, strings.NewReader(status))
	if err != nil {
		return fmt.Errorf("failed to send the status: %w", err)
	}

	return nil
}

func waitForCommands(sock net.Listener) (<-chan daemonCommand, chan<- string) {
	cmdChan := make(chan daemonCommand)
	resultChan := make(chan string)

	go func() {
		for {
			err := handleConnection(sock, cmdChan, resultChan)
			if errors.Is(err, net.ErrClosed) {
				fmt.Fprintf(os.Stderr, "Socket closed, stopping the listener.\n")
				break
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to handle connection: %v\n", err)
			}
		}
	}()

	return cmdChan, resultChan
}

func getRootFlags(cmd *cobra.Command) rootFlags {
	dryRun, _ := cmd.Flags().GetBool("dry")
	return rootFlags{dryRun: dryRun}
}

func socketPath() string {
	return path.Join(baseDir, "socket")
}

func socketExists() bool {
	_, err := os.Stat(socketPath())
	return err == nil
}

func sendMessageToDaemon(cmd daemonCommand) (string, error) {
	if !socketExists() {
		return "", nil
	}

	fmt.Fprintf(os.Stderr, "Sending %s command to the daemon\n", cmd.Name)

	conn, err := net.Dial("unix", socketPath())
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(cmd); err != nil {
		return "", err
	}

	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}

	// sanity check
	if len(result) == 0 {
		return "", fmt.Errorf("empty response from the daemon")
	}

	return string(result), nil
}

func main() {
	var rootCmd = &cobra.Command{
		Use:     "orches",
		Short:   "A simple git-ops tool for Podman and systemd",
		Long:    "orches is a git-ops tool for orchestrating Podman containers and systemd units on a single machine.",
		Version: version,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			level := slog.LevelInfo
			verbose, _ := cmd.Flags().GetBool("verbose")
			if verbose {
				level = slog.LevelDebug
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: level,
			}))
			slog.SetDefault(logger)
			if verbose {
				slog.Debug("Verbose output enabled")
			}

			slog.Debug("Base directory", "path", baseDir)
			slog.Debug("uid", "uid", os.Getuid())
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	rootCmd.PersistentFlags().Bool("dry", false, "Dry run")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Verbose output")

	var initCmd = &cobra.Command{
		Use:   "init [remote]",
		Short: "Initialize by cloning a repo and setting up state",
		Long:  "Initialize orches by cloning a Git repository and setting up the initial deployment state. The remote argument can be any valid Git repository URL or local path.",
		Example: "  orches init https://github.com/user/repo.git\n" +
			"  orches init /path/to/local/repo",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if socketExists() {
				return errors.New("daemon is already running, cannot init")
			}
			return initRepo(args[0], getRootFlags(cmd))
		},
	}

	var syncCmd = &cobra.Command{
		Use:   "sync",
		Short: "Sync deployments",
		Long:  "Synchronize the local system state with the target repository's state. This will fetch the latest changes and apply them.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dc := daemonCommand{Name: "sync"}
			remoteRes, err := sendMessageToDaemon(dc)
			if err != nil {
				return fmt.Errorf("failed to send message to daemon: %w", err)
			}
			if remoteRes != "" {
				fmt.Fprintf(os.Stderr, "Daemon responded: %s\n", remoteRes)
				return nil
			}

			_, err = cmdSync(getRootFlags(cmd))
			return err
		},
	}

	var pruneCmd = &cobra.Command{
		Use:   "prune",
		Short: "Prune deployments",
		Long:  "Remove all deployed resources and clean up the local repository state. This will stop all managed services and containers.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dc := daemonCommand{Name: "prune"}
			remoteRes, err := sendMessageToDaemon(dc)
			if err != nil {
				return fmt.Errorf("failed to send message to daemon: %w", err)
			}
			if remoteRes != "" {
				fmt.Fprintf(os.Stderr, "Daemon responded:\n%s\n", remoteRes)
				return nil
			}
			return cmdPrune(getRootFlags(cmd))
		},
	}

	var switchCmd = &cobra.Command{
		Use:   "switch [remote]",
		Short: "Switch to a different deployment",
		Long:  "Switch the deployment source to a different Git repository. This will first prune the existing deployment and then initialize from the new source.",
		Example: "  orches switch https://github.com/user/new-repo.git\n" +
			"  orches switch /path/to/new/local/repo",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := args[0]

			if git.IsLocalEndpoint(p) {
				var err error
				// absolute path is important for the daemon
				p, err = filepath.Abs(p)
				if err != nil {
					return fmt.Errorf("failed to get absolute path: %w", err)
				}
			}

			dc := daemonCommand{Name: "switch", Arg: p}
			remoteRes, err := sendMessageToDaemon(dc)
			if err != nil {
				return fmt.Errorf("failed to send message to daemon: %w", err)
			}
			if remoteRes != "" {
				fmt.Fprintf(os.Stderr, "Daemon responded:\n%s\n", remoteRes)
				return nil
			}

			return cmdSwitch(p, getRootFlags(cmd))
		},
	}

	var statusCmd = &cobra.Command{
		Use:   "status",
		Short: "Show the repository status",
		Long:  "Display information about the current deployment, including the remote repository URL and the currently deployed Git reference.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dc := daemonCommand{Name: "status"}
			remoteRes, err := sendMessageToDaemon(dc)
			if err != nil {
				return fmt.Errorf("failed to send message to daemon: %w", err)
			}
			if remoteRes != "" {
				fmt.Fprintf(os.Stderr, "Daemon responded:\n%s\n", remoteRes)
				return nil
			}

			if _, err := os.Stat(path.Join(baseDir, "repo")); errors.Is(err, os.ErrNotExist) {
				return errors.New("no repository found, initalize orches first")
			}
			result, err := cmdStatus()
			if err != nil {
				return err
			}

			fmt.Printf("%s\n", result)

			return nil
		},
	}

	var runCmd = &cobra.Command{
		Use:   "run",
		Short: "Periodically sync deployments",
		Long:  "Start the orches daemon that periodically synchronizes the local system with the remote repository.",
		Example: "  orches run\n" +
			"  orches run --interval 300",
		RunE: func(cmd *cobra.Command, args []string) error {
			syncInterval, err := cmd.Flags().GetInt("interval")
			if err != nil {
				return err
			}

			if _, err := os.Stat(path.Join(baseDir, "repo")); errors.Is(err, os.ErrNotExist) {
				return errors.New("no repository found, initalize orches first")
			}

			sock, err := net.Listen("unix", socketPath())
			if err != nil {
				return fmt.Errorf("failed to start the daemon socket: %w", err)
			}
			defer sock.Close()

			cmdChan, statusChan := waitForCommands(sock)

			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(sig)

			for {
				res, err := cmdSync(getRootFlags(cmd))
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error while running periodic sync: %v\n", err)
				}

				if res != nil && res.RestartNeeded {
					fmt.Fprintln(os.Stderr, "Restart needed after a periodical sync, exiting.")
					return nil
				}

				nextTick := time.After(time.Duration(syncInterval) * time.Second)

			innerLoop:
				for {
					select {
					case <-sig:
						fmt.Fprintln(os.Stderr, "Received interrupt signal, exiting.")
						return nil
					case c := <-cmdChan:
						switch c.Name {
						case "sync":
							res, err := cmdSync(getRootFlags(cmd))
							if err != nil {
								statusChan <- fmt.Sprintf("%v", err)
								fmt.Fprintf(os.Stderr, "Remote sync command failed: %v\n", err)
							} else {
								statusChan <- "Synced"
								fmt.Fprintln(os.Stderr, "Remote sync command successfully processed.")
							}
							if res != nil && res.RestartNeeded {
								fmt.Fprintln(os.Stderr, "Restart needed after a remote sync, exiting.")
								return nil
							}
						case "prune":
							err := cmdPrune(getRootFlags(cmd))
							if err != nil {
								statusChan <- fmt.Sprintf("%v", err)
								fmt.Fprintf(os.Stderr, "Remote prune command failed: %v\n", err)
							} else {
								statusChan <- "Pruned"
								fmt.Fprintln(os.Stderr, "Remote prune command successfully processed, exiting.")
								return nil
							}
						case "switch":
							err := cmdSwitch(c.Arg, getRootFlags(cmd))
							if err != nil {
								statusChan <- fmt.Sprintf("%v", err)
								fmt.Fprintf(os.Stderr, "Remote switch (%s) command failed: %v\n", c.Arg, err)
							} else {
								statusChan <- fmt.Sprintf("Switched to %s", c.Arg)
								fmt.Fprintf(os.Stderr, "Remote switch (%s) command successfully processed, exiting.\n", c.Arg)
								return nil
							}
						case "status":
							res, err := cmdStatus()
							if err != nil {
								statusChan <- fmt.Sprintf("%v", err)
								fmt.Fprintf(os.Stderr, "Remote status command failed: %v\n", err)
							} else {
								statusChan <- res
								fmt.Fprintln(os.Stderr, "Remote status command successfully processed.")
							}
						default:
							statusChan <- "Unknown command"
							fmt.Fprintf(os.Stderr, "Received unknown remote command: %s\n", c.Name)
						}
					case <-nextTick:
						break innerLoop
					}
				}
			}
		},
	}

	runCmd.Flags().Int("interval", 120, "Interval in seconds between synchronization attempts")

	var versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Long:  "Display version information about orches, including the version number, Git reference, and build timestamp.",
		RunE: func(cmd *cobra.Command, args []string) error {
			info, ok := debug.ReadBuildInfo()
			if !ok {
				return errors.New("no build info available")
			}

			buildinfo := struct {
				version string
				ref     string
				time    string
			}{
				version: version,
				ref:     "unknown",
				time:    "unknown",
			}

			for _, val := range info.Settings {
				switch val.Key {
				case "vcs.revision":
					buildinfo.ref = val.Value
				case "vcs.time":
					buildinfo.time = val.Value
				}
			}

			fmt.Printf("version: %s\n", buildinfo.version)
			fmt.Printf("gitref: %s\n", buildinfo.ref)
			fmt.Printf("buildtime: %s\n", buildinfo.time)

			return nil
		},
	}

	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w\nSee '%s --help'", err, cmd.CommandPath())
	})

	rootCmd.AddCommand(initCmd, syncCmd, pruneCmd, runCmd, switchCmd, statusCmd, versionCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func lock(fn func() error) error {
	os.Mkdir(baseDir, 0755)

	slog.Debug("Adding interrupt signal handler in lock()")
	interruptSig := make(chan os.Signal, 1)
	signal.Notify(interruptSig, os.Interrupt, syscall.SIGTERM)

	defer func() {
		signal.Stop(interruptSig)
		slog.Debug("Removed interrupt signal handler in lock()")
	}()

	var f *os.File
	var err error
	for {
		f, err = os.OpenFile(path.Join(baseDir, "lock"), os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			break
		}
		slog.Debug("Failed to acquire lock, retrying", "error", err)
		select {
		case <-time.After(100 * time.Millisecond):
		case <-interruptSig:
			return errors.New("interrupted while waiting for a lock")
		}
	}

	defer f.Close()
	defer func() {
		err := os.Remove(f.Name())
		if err != nil {
			slog.Error("Failed to remove lock file", "error", err)
		}
		slog.Debug("Removed lock")
	}()

	slog.Debug("Acquired lock")

	return fn()
}

func initRepo(remote string, flags rootFlags) error {
	return lock(func() error {
		return doInit(remote, flags.dryRun)
	})
}

func doInit(remote string, dryRun bool) error {
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

	if _, err := syncer.SyncDirs(blank, repoPath, dryRun, nil); err != nil {
		return fmt.Errorf("failed to sync directories: %w", err)
	}

	if dryRun {
		if err := os.RemoveAll(baseDir); err != nil {
			return fmt.Errorf("failed to remove directory: %w", err)
		}
		return nil
	}

	fmt.Fprintf(os.Stderr, "Initialized repo from %s\n", remote)
	return nil
}

func cmdSync(flags rootFlags) (*syncer.SyncResult, error) {
	var res *syncer.SyncResult

	err := lock(func() error {
		repoDir := filepath.Join(baseDir, "repo")
		repo := git.Repo{Path: repoDir}

		currentLocalRef, err := repo.Ref("HEAD")
		if err != nil {
			return fmt.Errorf("failed to get current HEAD ref: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Fetching from origin\n")
		if err := repo.Fetch("origin"); err != nil {
			return fmt.Errorf("failed to fetch from origin: %w", err)
		}

		remoteUpstreamRef, err := repo.Ref("@{u}")
		if err != nil {
			return fmt.Errorf("failed to get upstream ref (@{u}): %w. Ensure your current branch is tracking an upstream branch", err)
		}

		syncPostSyncAction := func(isDryRun bool) error {
			if !isDryRun {
				slog.Info("PostSyncAction(cmdSync): Resetting repository", "ref", remoteUpstreamRef)
				if err := repo.Reset(remoteUpstreamRef); err != nil {
					return fmt.Errorf("failed to reset repository to %s: %w", remoteUpstreamRef, err)
				}
				fmt.Fprintf(os.Stderr, "Repository reset to %s\n", remoteUpstreamRef)
			} else {
				fmt.Fprintf(os.Stderr, "PostSyncAction(cmdSync): Dry run, repository would have been reset to %s\n", remoteUpstreamRef)
			}
			return nil
		}

		if currentLocalRef == remoteUpstreamRef {
			fmt.Fprintln(os.Stderr, "No new commits to sync.")
			return nil
		}

		fmt.Fprintf(os.Stderr, "Current HEAD is %s, targeting %s\n", currentLocalRef, remoteUpstreamRef)

		oldState, err := repo.NewWorktree(currentLocalRef)
		if err != nil {
			return fmt.Errorf("failed to create worktree for current state: %w", err)
		}
		defer oldState.Cleanup()

		newState, err := repo.NewWorktree(remoteUpstreamRef)
		if err != nil {
			return fmt.Errorf("failed to create worktree for new state: %w", err)
		}
		defer newState.Cleanup()

		fmt.Fprintf(os.Stderr, "Syncing changes between %s and %s\n", currentLocalRef, remoteUpstreamRef)

		res, err = syncer.SyncDirs(oldState.Path, newState.Path, flags.dryRun, syncPostSyncAction)
		if err != nil {
			slog.Error("Sync process failed", "error", err, "current_ref", currentLocalRef)
			return fmt.Errorf("failed to sync directories: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Synced to %s\n", remoteUpstreamRef)
		return nil
	})
	return res, err
}

func cmdPrune(flags rootFlags) error {
	return lock(func() error {
		return doPrune(flags.dryRun)
	})
}

func doPrune(dryRun bool) error {
	repoDir := filepath.Join(baseDir, "repo")
	if _, err := os.Stat(repoDir); errors.Is(err, os.ErrNotExist) {
		return errors.New("no repository to prune, orches not initialized")
	}

	blank, err := os.MkdirTemp("", "orches-prune-")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(blank)

	prunePostSyncAction := func(isDryRun bool) error {
		if !isDryRun {
			slog.Info("PostSyncAction(doPrune): Removing repository directory", "path", repoDir)
			if err := os.RemoveAll(repoDir); err != nil {
				return fmt.Errorf("failed to remove repository directory %s: %w", repoDir, err)
			}
			fmt.Fprintf(os.Stderr, "Repository pruned from %s\n", repoDir)
		} else {
			fmt.Fprintf(os.Stderr, "PostSyncAction(doPrune): Dry run, would remove repository directory %s\n", repoDir)
		}
		return nil
	}

	if _, err := syncer.SyncDirs(repoDir, blank, dryRun, prunePostSyncAction); err != nil {
		return fmt.Errorf("failed to sync directories for prune: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Repository pruned\n")
	return nil
}

func cmdSwitch(remote string, flags rootFlags) error {
	return lock(func() error {
		// First prune the existing deployment
		if err := doPrune(flags.dryRun); err != nil {
			return fmt.Errorf("failed to prune existing deployment: %w", err)
		}

		// Then initialize with the new remote
		if err := doInit(remote, flags.dryRun); err != nil {
			return fmt.Errorf("failed to initialize new deployment: %w", err)
		}

		return nil
	})
}

func cmdStatus() (string, error) {
	repoDir := path.Join(baseDir, "repo")
	if _, err := os.Stat(repoDir); errors.Is(err, os.ErrNotExist) {
		return "", errors.New("no repository found, initalize orches first")
	}

	repo := git.Repo{Path: repoDir}

	remoteURL, err := repo.RemoteURL("origin")
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}

	head, err := repo.Ref("HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD: %w", err)
	}

	buf := fmt.Sprintf("remote: %s\nref: %s", remoteURL, head)
	return buf, nil
}
