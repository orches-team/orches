package unit

import (
	"fmt"
	"os"
	"path"
)

var homeDir string

func init() {
	dir, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("failed to get user home directory: %v", err))
	}

	homeDir = dir
}

func ContainerDir(user bool) string {
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return "/etc/containers/systemd"
	}
	if user {
		return path.Join(homeDir, ".config", "containers", "systemd")
	}
	return "/etc/containers/systemd"
}

type UnitType int

const (
	UnitTypeContainer UnitType = iota
)

type unit struct {
	name    string
	content string
}

type Unit interface {
	Name() string
	SystemctlName() string
	Path(user bool) string
	EqualContent(Unit) bool
}

type ErrUnknownUnitType struct {
	name string
}

func (e *ErrUnknownUnitType) Error() string {
	return fmt.Sprintf("unknown unit type: %v", e.name)
}

func New(baseDir, name string) (Unit, error) {
	data, err := os.ReadFile(path.Join(baseDir, name))
	if err != nil {
		return nil, err
	}

	u := &unit{
		name:    name,
		content: string(data),
	}
	if u.innerTyp(name) == nil {
		return nil, &ErrUnknownUnitType{name: name}
	}
	return u, nil
}

func (u *unit) Name() string {
	return u.name
}

func (u *unit) innerTyp(name string) *UnitType {
	var typ UnitType

	switch true {
	case path.Ext(name) == ".container":
		typ = UnitTypeContainer
	default:
		return nil
	}

	return &typ
}

func (u *unit) Typ() UnitType {
	return *u.innerTyp(u.name)
}

func (u *unit) SystemctlName() string {
	if u.Name() == "orches.container" {
		return ""
	}

	switch u.Typ() {
	case UnitTypeContainer:
		return u.name[:len(u.name)-len(".container")] + ".service"
	default:
		panic("unknown unit type: " + u.name)
	}
}

func (u *unit) Path(user bool) string {
	switch u.Typ() {
	case UnitTypeContainer:
		return path.Join(ContainerDir(user), u.name)
	default:
		panic("unknown unit type: " + u.name)
	}
}

func (u *unit) EqualContent(other Unit) bool {
	return u.content == other.(*unit).content
}
