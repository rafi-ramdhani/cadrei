// Package cadreis knows where cadreis live: each in its own folder under
// ~/.cadrei. It lists them, keeps the default, resolves the cadrei a command
// acts on, and creates new ones.
package cadreis

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
)

// Root is ~/.cadrei.
func Root() string { return filepath.Join(paths.Home(), ".cadrei") }

// ConfigDir is ~/.cadrei/config: machine settings, in no cadrei's git.
func ConfigDir() string { return filepath.Join(Root(), "config") }

// Config is a file in ConfigDir.
func Config(name string) string { return filepath.Join(ConfigDir(), name) }

// Cadrei is one cadrei: a folder ~/.cadrei/<name> with members/.
type Cadrei struct {
	Name string // the folder's name
	Path string // physical
}

// Present reports whether the cadrei's folder is there.
func (c Cadrei) Present() bool {
	st, err := os.Stat(filepath.Join(c.Path, "members"))
	return err == nil && st.IsDir()
}

var nameRule = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// reserved names cannot be cadreis: they are ~/.cadrei's own folders.
// Anything new cadrei keeps in ~/.cadrei, ~/.cadrei-archive, a crew's top
// folder or members/<team>/ starts with a dot, which no crew, team or role
// name can, so it never needs another reserved name.
var reserved = map[string]bool{"config": true, "framework": true}

// CheckName refuses a name that cannot be a cadrei's: it is part of every
// session name, so it may use letters, digits, - and _, and config and
// framework are ~/.cadrei's own folders.
func CheckName(name string) error {
	if !nameRule.MatchString(name) {
		return fmt.Errorf("a crew's name can use letters, digits, - and _, so %s won't work", name)
	}
	if reserved[strings.ToLower(name)] {
		return fmt.Errorf("%s is kept for cadrei's own files. Pick another name", name)
	}
	return nil
}

// List returns every cadrei: the folders under ~/.cadrei that hold
// members/, sorted by name. Cadreis live only there.
func List() ([]Cadrei, error) {
	var out []Cadrei
	entries, err := os.ReadDir(Root())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		n := e.Name()
		// A symlink here would make any folder a cadrei without cadreis add,
		// and a name that cannot be a cadrei's would break session names.
		if e.Type()&fs.ModeSymlink != 0 || CheckName(n) != nil {
			continue
		}
		c := Cadrei{Name: n, Path: paths.Real(filepath.Join(Root(), n))}
		if c.Present() {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Find returns the cadrei called name.
func Find(name string) (Cadrei, bool) {
	list, _ := List()
	for _, c := range list {
		if c.Name == name {
			return c, true
		}
	}
	return Cadrei{}, false
}

// At returns the listed cadrei whose folder is path (physical).
func At(path string) (Cadrei, bool) {
	list, _ := List()
	for _, c := range list {
		if c.Path == path {
			return c, true
		}
	}
	return Cadrei{}, false
}

// Clash returns a present cadrei, other than at path, whose name equals
// name in any letter case: names are addresses, so two cannot share one.
func Clash(name, path string) (Cadrei, bool) {
	list, _ := List()
	for _, c := range list {
		if strings.EqualFold(c.Name, name) && c.Path != path && c.Present() {
			return c, true
		}
	}
	return Cadrei{}, false
}

// Default returns the default cadrei's name, or "". When none was chosen
// (the config is gone), the only cadrei is the default.
func Default() string {
	raw, err := os.ReadFile(Config("default"))
	if err != nil {
		if list, _ := List(); len(list) == 1 {
			return list[0].Name
		}
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// DefaultSet reports whether a default cadrei was chosen on this machine:
// without it (a reinstall, or a config deleted), the first run plugs the
// cadreis in ~/.cadrei back in.
func DefaultSet() bool {
	_, err := os.Stat(Config("default"))
	return err == nil
}

// SetDefault makes the cadrei called name the default.
func SetDefault(name string) error {
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return err
	}
	return fsx.WriteFile(Config("default"), []byte(name+"\n"), 0o600)
}

// lockConfig serializes changes to the files in config/.
func lockConfig() (*fsx.Lock, error) {
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return nil, err
	}
	l, err := fsx.Acquire(Config(".lock"), 10*time.Second)
	if errors.Is(err, fsx.ErrBusy) {
		return nil, fmt.Errorf("another cadrei command is changing %s. Try again", Tilde(ConfigDir()))
	}
	return l, err
}

// Inside reports whether path is inside ~/.cadrei (physical paths).
func Inside(path string) bool { return paths.Within(path, paths.Real(Root())) }
