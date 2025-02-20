package syncer

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/orches-team/orches/pkg/unit"
	"github.com/orches-team/orches/pkg/utils"
)

type SyncResult struct {
	RestartNeeded bool
}

func SyncDirs(old, new string, dryRun bool) (*SyncResult, error) {
	oldUnits, err := listUnits(old)
	if err != nil {
		return nil, fmt.Errorf("failed to list old files: %w", err)
	}

	newUnits, err := listUnits(new)
	if err != nil {
		return nil, fmt.Errorf("failed to list new files: %w", err)
	}

	added, removed, modified := diffUnits(oldUnits, newUnits)

	res, err := processChanges(new, added, removed, modified, dryRun)
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

func processChanges(newDir string, added, removed, modified []unit.Unit, dryRun bool) (*SyncResult, error) {
	if len(added) == 0 && len(removed) == 0 && len(modified) == 0 {
		fmt.Fprintf(os.Stderr, "No changes to process.")
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
