package syncer

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"

	// "github.com/orches-team/orches/pkg/git" // No longer needed here
	"github.com/orches-team/orches/pkg/unit"
	"github.com/orches-team/orches/pkg/utils"
)

// PostSyncAction defines a function to be called after units are on disk and daemon reloaded,
// but before services are (re)started. It's responsible for finalizing any underlying
// state (like a git repository reset or directory removal) and should handle dryRun appropriately.
type PostSyncAction func(dryRun bool) error

type SyncResult struct {
	RestartNeeded bool
}

func SyncDirs(
	oldWorktreePath string,
	newWorktreePath string,
	dryRun bool,
	postSyncAction PostSyncAction,
) (*SyncResult, error) {
	oldUnits, err := listUnits(oldWorktreePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list old files: %w", err)
	}

	newUnits, err := listUnits(newWorktreePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list new files: %w", err)
	}

	added, removed, modified := diffUnits(oldUnits, newUnits)

	res, err := processChanges(newWorktreePath, added, removed, modified, dryRun, postSyncAction)
	if err != nil {
		return nil, fmt.Errorf("failed to process changes: %w", err)
	}

	return res, nil
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

func processChanges(
	newDir string, // This is newWorktreePath
	added, removed, modified []unit.Unit,
	dryRun bool,
	postSyncAction PostSyncAction,
) (*SyncResult, error) {
	if len(added) == 0 && len(removed) == 0 && len(modified) == 0 {
		fmt.Fprintf(os.Stderr, "No changes to process.")
		// Execute postSyncAction even if no unit changes, as the underlying repo might have changed.
		if postSyncAction != nil {
			if err := postSyncAction(dryRun); err != nil {
				return nil, fmt.Errorf("post sync action failed even with no unit changes: %w", err)
			}
		}
		return &SyncResult{}, nil
	}

	if len(added) > 0 {
		fmt.Fprintf(os.Stderr, "Added: %v\n", utils.MapSlice(added, func(u unit.Unit) string { return u.Name() }))
	}
	if len(removed) > 0 {
		fmt.Fprintf(os.Stderr, "Removed: %v\n", utils.MapSlice(removed, func(u unit.Unit) string { return u.Name() }))
	}
	if len(modified) > 0 {
		fmt.Fprintf(os.Stderr, "Modified: %v\n", utils.MapSlice(modified, func(u unit.Unit) string { return u.Name() }))
	}

	s := Syncer{
		Dry:  dryRun,
		User: os.Getuid() != 0,
	}

	isOrches := func(u unit.Unit) bool { return u.Name() == "orches.container" }

	restartNeeded := false

	toRestart := modified
	toStop := removed
	if slices.ContainsFunc(modified, isOrches) {
		toRestart = slices.DeleteFunc(append([]unit.Unit{}, modified...), isOrches)
		fmt.Println("orches.container was changed")
		restartNeeded = true
	} else if slices.ContainsFunc(removed, isOrches) {
		toStop = slices.DeleteFunc(append([]unit.Unit{}, removed...), isOrches)
		fmt.Println("orches.container was removed")
		restartNeeded = true
	}

	if err := s.CreateDirs(); err != nil {
		return nil, fmt.Errorf("failed to create directories: %w", err)
	}

	if err := s.DisableUnits(removed); err != nil {
		return nil, fmt.Errorf("failed to disable unit: %w", err)
	}

	if err := s.StopUnits(toStop); err != nil {
		return nil, fmt.Errorf("failed to stop unit: %w", err)
	}

	if err := s.Remove(removed); err != nil {
		return nil, fmt.Errorf("failed to remove unit: %w", err)
	}

	if err := s.Add(newDir, append(added, modified...)); err != nil {
		return nil, fmt.Errorf("failed to add unit: %w", err)
	}

	if err := s.ReloadDaemon(); err != nil {
		return nil, fmt.Errorf("failed to reload daemon: %w", err)
	}

	// Perform the post-sync action (e.g., git reset, directory removal)
	if postSyncAction != nil {
		slog.Info("Executing post-sync action")
		if err := postSyncAction(s.Dry); err != nil { // Pass syncer's dryRun state
			return nil, fmt.Errorf("post-sync action failed: %w", err)
		}
		slog.Info("Post-sync action completed successfully")
	} else {
		slog.Info("No post-sync action provided")
	}

	if err := s.RestartUnits(toRestart); err != nil {
		return nil, fmt.Errorf("failed to restart unit: %w", err)
	}

	if err := s.StartUnits(append(added, toRestart...)); err != nil {
		return nil, fmt.Errorf("failed to start unit: %w", err)
	}

	if err := s.EnableUnits(added); err != nil {
		return nil, fmt.Errorf("failed to enable unit: %w", err)
	}

	return &SyncResult{RestartNeeded: restartNeeded}, nil
}
