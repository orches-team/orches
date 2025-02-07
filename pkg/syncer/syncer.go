package syncer

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"

	"github.com/orches-team/orches/pkg/unit"
	"github.com/orches-team/orches/pkg/utils"
)

type Syncer struct {
	Dry  bool
	User bool
}

func (s *Syncer) CreateDirs() error {
	if _, err := os.Stat(unit.ContainerDir(s.User)); err == nil {
		s.dryPrint("Directory exists", unit.ContainerDir(s.User))
		return nil
	}

	s.dryPrint("Create", unit.ContainerDir(s.User))

	return os.MkdirAll(unit.ContainerDir(s.User), 0755)
}

func (s *Syncer) Remove(units []unit.Unit) error {
	errs := []error{}

	for _, u := range units {
		s.dryPrint("remove", u.Path(s.User))
		if !s.Dry {
			errs = append(errs, os.Remove(u.Path(s.User)))
		}

	}

	return errors.Join(errs...)
}

func (s *Syncer) StopUnits(units []unit.Unit) error {
	if len(units) == 0 {
		return nil
	}
	names := utils.MapSlice(units, func(u unit.Unit) string { return u.SystemctlName() })
	return s.runSystemctl("stop", names...)
}

func (s *Syncer) StartUnits(units []unit.Unit) error {
	if len(units) == 0 {
		return nil
	}
	names := utils.MapSlice(units, func(u unit.Unit) string { return u.SystemctlName() })
	return s.runSystemctl("start", names...)
}

func (s *Syncer) RestartUnits(units []unit.Unit) error {
	if len(units) == 0 {
		return nil
	}
	names := utils.MapSlice(units, func(u unit.Unit) string { return u.SystemctlName() })
	return s.runSystemctl("try-restart", names...)
}

func (s *Syncer) Add(srcDir string, units []unit.Unit) error {
	errs := []error{}

	for _, u := range units {
		s.dryPrint("copy", path.Join(srcDir, u.Name()), u.Path(s.User))
		if !s.Dry {
			errs = append(errs, utils.CopyFile(path.Join(srcDir, u.Name()), u.Path(s.User)))
		}
	}

	return errors.Join(errs...)
}

func (s *Syncer) ReloadDaemon() error {
	return s.runSystemctl("daemon-reload")
}

func (s *Syncer) systemctlCmd(verb string, args ...string) []string {
	cmd := []string{"systemctl"}

	if s.User {
		cmd = append(cmd, "--user")
	}

	cmd = append(cmd, verb)
	cmd = append(cmd, args...)
	return cmd
}

func (s *Syncer) runSystemctl(verb string, args ...string) error {
	cmd := s.systemctlCmd(verb, args...)
	s.dryPrint("Run", cmd)

	out, err := utils.ExecOutput(cmd...)

	if len(out) > 0 {
		slog.Debug("systemctl output", "output", string(out))
	}

	return err
}

func (s *Syncer) dryPrint(action string, args ...any) {
	if s.Dry {
		fmt.Printf("%s: %v\n", action, args)
	}
	slog.Debug(fmt.Sprintf("syncer: %s", action), "args", args)
}
