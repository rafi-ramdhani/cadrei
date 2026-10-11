package cadreis

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/gitx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
)

// Create makes a new cadrei in ~/.cadrei/<name> from the template (fs holds
// template/ at its root, as the embedded assets do). The cadrei is a git
// repository; its first commit is made when git has an identity, and note
// says so otherwise.
func Create(name string, tmpl fs.FS) (c Cadrei, note string, err error) {
	if err := CheckName(name); err != nil {
		return c, "", err
	}
	dest := filepath.Join(Root(), name)
	if _, err := os.Lstat(dest); err == nil {
		return c, "", fmt.Errorf("%s already exists", Tilde(paths.Real(dest)))
	}
	if other, ok := Clash(name, paths.Real(dest)); ok {
		return c, "", fmt.Errorf("a crew called %s is already at %s. Pick another name", other.Name, Tilde(other.Path))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return c, "", err
	}
	if err := writeTemplate(tmpl, dest, name); err != nil {
		return c, "", err
	}
	c = Cadrei{Name: name, Path: paths.Real(dest)}
	if out, err := git(c.Path, "init", "-q", "-b", "main"); err != nil {
		return c, "", fmt.Errorf("git init in %s: %s", c.Path, out)
	}
	note, err = Commit(c.Path, "Start "+name+" from the cadrei template", ".")
	return c, note, err
}

// writeTemplate copies template/ into dest, putting the cadrei's name into
// its Markdown files.
func writeTemplate(tmpl fs.FS, dest, name string) error {
	return fs.WalkDir(tmpl, "template", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("template", p)
		to := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		data, err := fs.ReadFile(tmpl, p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(p, ".md") {
			data = bytes.ReplaceAll(data, []byte("{{name}}"), []byte(name))
		}
		return os.WriteFile(to, data, 0o644)
	})
}

func git(dir string, args ...string) (string, error) {
	out, err := gitx.Reading(dir, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Commit commits files (paths inside the cadrei) with msg. Without a git
// identity it leaves the change uncommitted and returns a note saying so;
// a cadrei that is not a git repository of its own is left alone.
func Commit(cadrei, msg string, files ...string) (string, error) {
	if _, err := os.Lstat(filepath.Join(cadrei, ".git")); err != nil {
		return "", nil
	}
	if _, err := git(cadrei, "rev-parse", "--git-dir"); err != nil {
		return "", nil
	}
	if _, err := git(cadrei, "config", "user.email"); err != nil {
		return "Git has no name or email set, so this change was left uncommitted in " + Tilde(paths.Real(cadrei)) +
			". Set them (git config --global user.name and user.email), then commit it.", nil
	}
	if out, err := git(cadrei, append([]string{"add", "--"}, files...)...); err != nil {
		return "", fmt.Errorf("git add: %s", out)
	}
	// Nothing to commit is not an error, as in the bash version.
	git(cadrei, append([]string{"commit", "-qm", msg, "--"}, files...)...)
	return "", nil
}
