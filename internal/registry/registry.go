// Package registry reads and edits a cadrei's projects.yaml: a flat list of
// entries, each a "name:" line followed by indented "key: value" lines.
// It is not general YAML. Edits change only the lines they must, so the
// user's comments and blank lines survive (section L.5). A value runs to
// the end of its line, inline # and quotes included. It stays this subset:
// a real YAML parser would read existing files differently, so one could
// only come with a crew format bump (cadreis.Format).
package registry

import (
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
)

// Entry is one project.
type Entry struct {
	Name   string
	Fields map[string]string
	header int   // index of the "name:" line
	fields []int // indexes of its "key: value" lines
}

// Get returns a field, or "".
func (e *Entry) Get(key string) string { return e.Fields[key] }

// File is a parsed projects.yaml.
type File struct {
	lines   []string
	entries []*Entry
}

// Parse reads a registry the way the bash version did: blank lines and
// lines starting with # (after indentation) are skipped; a line that does
// not start with whitespace and ends with ":" starts an entry; any other
// line with a ":" is a key and value of the current entry, split at the
// first ":" and trimmed.
func Parse(data string) *File {
	// Python read a lone CR as a line end too.
	data = strings.ReplaceAll(strings.ReplaceAll(data, "\r\n", "\n"), "\r", "\n")
	f := &File{lines: strings.Split(data, "\n")}
	if n := len(f.lines); n > 0 && f.lines[n-1] == "" {
		f.lines = f.lines[:n-1]
	}
	f.index()
	return f
}

func (f *File) index() {
	f.entries = nil
	var cur *Entry
	for i, line := range f.lines {
		s := strings.TrimRightFunc(line, unicode.IsSpace)
		if strings.TrimSpace(s) == "" || strings.HasPrefix(strings.TrimSpace(s), "#") {
			continue
		}
		if first, _ := utf8.DecodeRuneInString(s); !unicode.IsSpace(first) && strings.HasSuffix(s, ":") {
			name := strings.TrimSuffix(strings.TrimSpace(s), ":")
			// A repeated name starts the entry again in its first place,
			// as the bash version's dict did.
			cur = &Entry{Name: name, Fields: map[string]string{}, header: i}
			placed := false
			for k, e := range f.entries {
				if e.Name == name {
					f.entries[k], placed = cur, true
					break
				}
			}
			if !placed {
				f.entries = append(f.entries, cur)
			}
			continue
		}
		if cur != nil && strings.Contains(s, ":") {
			k, v, _ := strings.Cut(strings.TrimSpace(s), ":")
			cur.Fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
			cur.fields = append(cur.fields, i)
		}
	}
}

// Load reads the registry at path; a missing file is an empty registry.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Parse(""), nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(string(raw)), nil
}

// nameRule is a project's name. A name becomes a folder in the projects
// folder and part of session names, and a registry can come from another
// machine or a shared backup, so a name can never climb (..) or nest (/).
var nameRule = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidName reports whether name can be a project's name: letters,
// digits, ., - and _, starting with a letter or digit, and no "..".
func ValidName(name string) bool {
	return nameRule.MatchString(name) && !strings.Contains(name, "..")
}

// Entries returns the projects, in file order. Entries whose name is not
// a valid project name are left out (Skipped lists them), so no caller
// ever builds a path from one.
func (f *File) Entries() []*Entry {
	var out []*Entry
	for _, e := range f.entries {
		if ValidName(e.Name) {
			out = append(out, e)
		}
	}
	return out
}

// Skipped lists the names of entries left out because they are not valid
// project names, in file order.
func (f *File) Skipped() []string {
	var out []string
	for _, e := range f.entries {
		if !ValidName(e.Name) {
			out = append(out, e.Name)
		}
	}
	return out
}

// Get returns the project called name, or nil (also for a name that is not
// a valid project name).
func (f *File) Get(name string) *Entry {
	if !ValidName(name) {
		return nil
	}
	for _, e := range f.entries {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Field is a key and value, for Add.
type Field struct{ Key, Value string }

// Add appends an entry, after a blank line.
func (f *File) Add(name string, fields ...Field) {
	f.lines = append(f.lines, "", name+":")
	for _, fl := range fields {
		f.lines = append(f.lines, strings.TrimRight("  "+fl.Key+": "+fl.Value, " "))
	}
	f.index()
}

// Set changes a field of an entry in place, or adds it after the entry's
// last field. It reports whether the entry exists.
func (f *File) Set(name, key, value string) bool {
	e := f.Get(name)
	if e == nil {
		return false
	}
	line := strings.TrimRight("  "+key+": "+value, " ")
	for _, i := range e.fields {
		k, _, _ := strings.Cut(strings.TrimSpace(f.lines[i]), ":")
		if strings.TrimSpace(k) == key {
			f.lines[i] = line
			f.index()
			return true
		}
	}
	at := e.header + 1
	if len(e.fields) > 0 {
		at = e.fields[len(e.fields)-1] + 1
	}
	f.lines = append(f.lines[:at], append([]string{line}, f.lines[at:]...)...)
	f.index()
	return true
}

// Remove deletes an entry: its name line and its field lines, and the
// blank line before it when one is there. Comments are kept. It reports
// whether the entry existed.
func (f *File) Remove(name string) bool {
	e := f.Get(name)
	if e == nil {
		return false
	}
	drop := map[int]bool{e.header: true}
	for _, i := range e.fields {
		drop[i] = true
	}
	if e.header > 0 && strings.TrimSpace(f.lines[e.header-1]) == "" {
		drop[e.header-1] = true
	}
	kept := f.lines[:0:0]
	for i, l := range f.lines {
		if !drop[i] {
			kept = append(kept, l)
		}
	}
	f.lines = kept
	f.index()
	return true
}

// Bytes is the file's text, ending with a newline.
func (f *File) Bytes() []byte {
	if len(f.lines) == 0 {
		return nil
	}
	return []byte(strings.Join(f.lines, "\n") + "\n")
}

// Save writes the registry atomically.
func (f *File) Save(path string) error { return fsx.WriteFile(path, f.Bytes(), 0o644) }
