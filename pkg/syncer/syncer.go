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

func (s *Syncer) createDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		s.dryPrint("Directory exists", dir)
		return nil
	}

	s.dryPrint("Create", dir)

	return os.MkdirAll(dir, 0755)
}

func (s *Syncer) CreateDirs() error {
	var errs []error
	for _, dir := range []string{unit.ContainerDir(s.User), unit.ServiceDir(s.User)} {
		errs = append(errs, s.createDir(dir))
	}
	return errors.Join(errs...)
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
	return s.transitionUnits("stop", units)
}

func (s *Syncer) StartUnits(units []unit.Unit) error {
	return s.transitionUnits("start", units)
}

func (s *Syncer) RestartUnits(units []unit.Unit) error {
	return s.transitionUnits("try-restart", units)
}

func (s *Syncer) EnableUnits(units []unit.Unit) error {
	filtered := utils.FilterSlice(units, func(u unit.Unit) bool { return u.CanBeEnabled() })
	if len(filtered) == 0 {
		return nil
	}
	return s.transitionUnits("enable", filtered)
}

func (s *Syncer) DisableUnits(units []unit.Unit) error {
	filtered := utils.FilterSlice(units, func(u unit.Unit) bool { return u.CanBeEnabled() })
	if len(filtered) == 0 {
		return nil
	}
	return s.transitionUnits("disable", filtered)
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

func (s *Syncer) transitionUnits(verb string, units []unit.Unit) error {
	if len(units) == 0 {
		return nil
	}

	names := utils.MapSlice(units, func(u unit.Unit) string { return u.SystemctlName() })
	return s.runSystemctl(verb, names...)
}

func (s *Syncer) dryPrint(action string, args ...any) {
	if s.Dry {
		fmt.Fprintf(os.Stderr, "%s: %v\n", action, args)
	}
	slog.Debug(fmt.Sprintf("syncer: %s", action), "args", args)
}
