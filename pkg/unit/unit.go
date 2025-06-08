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

func ServiceDir(user bool) string {
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return "/etc/systemd/system"
	}
	if user {
		return path.Join(homeDir, ".config", "systemd", "user")
	}
	return "/etc/systemd/system"
}

type UnitType int

const (
	UnitTypeContainer UnitType = iota
	UnitTypeNetwork
	UnitTypeVolume
	UnitTypePod
	UnitTypeService
	UnitTypeSocket
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
	CanBeEnabled() bool
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
	case path.Ext(name) == ".network":
		typ = UnitTypeNetwork
	case path.Ext(name) == ".volume":
		typ = UnitTypeVolume
	case path.Ext(name) == ".pod":
		typ = UnitTypePod
	case path.Ext(name) == ".service":
		typ = UnitTypeService
	case path.Ext(name) == ".socket":
		typ = UnitTypeSocket
	default:
		return nil
	}

	return &typ
}

func (u *unit) Typ() UnitType {
	return *u.innerTyp(u.name)
}

func (u *unit) SystemctlName() string {
	switch u.Typ() {
	case UnitTypeContainer:
		return u.name[:len(u.name)-len(".container")] + ".service"
	case UnitTypeNetwork:
		return u.name[:len(u.name)-len(".network")] + "-network.service"
	case UnitTypeVolume:
		return u.name[:len(u.name)-len(".volume")] + "-volume.service"
	case UnitTypePod:
		return u.name[:len(u.name)-len(".pod")] + "-pod.service"
	case UnitTypeService:
		return u.name
	case UnitTypeSocket:
		return u.name
	default:
		panic("unknown unit type: " + u.name)
	}
}

func (u *unit) Path(user bool) string {
	switch u.Typ() {
	case UnitTypeContainer:
		fallthrough
	case UnitTypeNetwork:
		fallthrough
	case UnitTypeVolume:
		fallthrough
	case UnitTypePod:
		return path.Join(ContainerDir(user), u.name)
	case UnitTypeService:
		fallthrough
	case UnitTypeSocket:
		return path.Join(ServiceDir(user), u.name)
	default:
		panic("unknown unit type: " + u.name)
	}
}

func (u *unit) EqualContent(other Unit) bool {
	return u.content == other.(*unit).content
}

func (u *unit) CanBeEnabled() bool {
	return u.Typ() == UnitTypeService || u.Typ() == UnitTypeSocket
}
