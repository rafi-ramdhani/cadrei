// Package memory keeps the orchestrator's own memory in its crew: it
// moves the notes in from the folder the runtime keeps them in on this
// machine, merges them with the crew's, and moves them back out when the
// user wants them kept on this machine only.
package memory

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
)

// Folder is the crew's memory folder, at its root.
const Folder = ".memory"

// Index is the memory folder's index, one line per note.
const Index = "MEMORY.md"

// Dir is the crew's memory folder.
func Dir(crew string) string { return filepath.Join(crew, Folder) }

// Pointer is the index cadrei leaves in the runtime's own memory folder
// for the crew, so a session without cadrei's settings learns where the
// memory is.
func Pointer(crew string) string {
	return "- This crew's orchestrator memory is in " + crew + "/" + Folder + " (cadrei keeps it there). Read " + crew + "/" + Folder + "/" + Index + " for the index.\n"
}

var pointerRule = regexp.MustCompile(`^- This crew's orchestrator memory is in (.+)/\.memory \(cadrei keeps it there\)\. Read (.+)/\.memory/MEMORY\.md for the index\.\n$`)

// pointerLine is a pointer's line, for any crew, among an index's lines.
var pointerLine = regexp.MustCompile(`^- This crew's orchestrator memory is in .+/\.memory \(cadrei keeps it there\)\. Read .+/\.memory/MEMORY\.md for the index\.$`)

// withoutPointer is an index without cadrei's pointer lines, which Claude
// Code can write notes' lines under: nil when nothing else is left.
func withoutPointer(data []byte) []byte {
	if IsPointer(data) {
		return nil
	}
	var keep []string
	found := false
	for _, l := range strings.SplitAfter(string(data), "\n") {
		if pointerLine.MatchString(strings.TrimRight(l, "\n")) {
			found = true
			continue
		}
		keep = append(keep, l)
	}
	if !found {
		return data
	}
	if strings.TrimSpace(strings.Join(keep, "")) == "" {
		return nil
	}
	return []byte(strings.Join(keep, ""))
}

// IsPointer reports whether data is exactly a pointer cadrei wrote.
func IsPointer(data []byte) bool {
	m := pointerRule.FindSubmatch(data)
	return m != nil && bytes.Equal(m[1], m[2])
}

// A Result is what a move did.
type Result struct {
	Moved  int      // notes copied over, the index not counted
	Copies []string // notes that clashed, kept beside the other under these names
}

// entries lists a memory folder's files by name, the index apart. A
// pointer index counts as none. Anything but a file stops it: cadrei
// moves only what it can vouch for.
func entries(dir string) (files []string, index []byte, err error) {
	list, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, en := range list {
		if !en.Type().IsRegular() {
			return nil, nil, fmt.Errorf("%s isn't a file", filepath.Join(dir, en.Name()))
		}
		if en.Name() == Index {
			data, err := os.ReadFile(filepath.Join(dir, Index))
			if err != nil {
				return nil, nil, err
			}
			index = withoutPointer(data)
			continue
		}
		files = append(files, en.Name())
	}
	sort.Strings(files)
	return files, index, nil
}

// Holds reports whether dir holds notes: any file but a pointer index.
func Holds(dir string) (bool, error) {
	files, index, err := entries(dir)
	return len(files) > 0 || index != nil, err
}

// Count is the number of notes in the crew, the index not counted.
func Count(crew string) int {
	files, _, _ := entries(Dir(crew))
	return len(files)
}

// merge copies the notes in from into to, which it makes when it must. A
// note to lacks is copied; one with the same name and the same text is
// skipped; one with the same name and other text is copied beside it as
// <name>.from-<machine>.md. The index gets from's lines it lacks (never a
// pointer's), with links to renamed notes renamed. Every copy is read
// back before merge returns, and from is left as it was. taken is each
// file of from it dealt with, as it read it, for clear.
func merge(from, to, machine string) (res Result, taken map[string][]byte, err error) {
	taken = map[string][]byte{}
	files, index, err := entries(from)
	if err != nil {
		return res, nil, err
	}
	if len(files) == 0 && index == nil {
		return res, nil, nil
	}
	if raw, err := os.ReadFile(filepath.Join(from, Index)); err == nil {
		taken[Index] = raw
	}
	have, haveIndex, err := entries(to)
	if err != nil {
		return res, nil, err
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return res, nil, err
	}
	present := map[string]bool{}
	for _, f := range have {
		present[f] = true
	}
	renamed := map[string]string{}
	written := map[string][]byte{}
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(from, name))
		if err != nil {
			return res, nil, err
		}
		taken[name] = data
		target := name
		if present[name] {
			same, err := sameText(filepath.Join(to, name), data)
			if err != nil {
				return res, nil, err
			}
			if same {
				continue
			}
			if target, same, err = freeName(to, name, machine, data); err != nil {
				return res, nil, err
			}
			renamed[name] = target
			res.Copies = append(res.Copies, target)
			if same {
				continue
			}
		}
		if err := fsx.WriteFile(filepath.Join(to, target), data, 0o644); err != nil {
			return res, nil, err
		}
		written[target] = data
		res.Moved++
	}
	if next, changed := mergeIndex(haveIndex, index, renamed, machine); changed {
		if err := fsx.WriteFile(filepath.Join(to, Index), next, 0o644); err != nil {
			return res, nil, err
		}
		written[Index] = next
	}
	for name, data := range written {
		if same, err := sameText(filepath.Join(to, name), data); err != nil || !same {
			return res, nil, fmt.Errorf("%s didn't read back as it was written", filepath.Join(to, name))
		}
	}
	return res, taken, nil
}

// freeName is the name a clashing note is copied under: <name>.from-
// <machine>.md, numbered when that is taken by other text. same reports
// that the name holds this text already.
func freeName(dir, name, machine string, data []byte) (string, bool, error) {
	base, ext := name, ""
	if strings.HasSuffix(name, ".md") {
		base, ext = strings.TrimSuffix(name, ".md"), ".md"
	}
	for i := 1; ; i++ {
		cand := base + ".from-" + machine + ext
		if i > 1 {
			cand = fmt.Sprintf("%s.from-%s-%d%s", base, machine, i, ext)
		}
		same, err := sameText(filepath.Join(dir, cand), data)
		if errors.Is(err, fs.ErrNotExist) {
			return cand, false, nil
		}
		if err != nil || same {
			return cand, same, err
		}
	}
}

// sameText reports whether the file at path holds data.
func sameText(path string, data []byte) (bool, error) {
	have, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return bytes.Equal(have, data), nil
}

// mergeIndex adds from's index lines that have lacks, after have's own:
// links to renamed notes are renamed first, and a renamed note with no
// line gets one. It reports whether that changed anything.
func mergeIndex(have, from []byte, renamed map[string]string, machine string) ([]byte, bool) {
	lines := map[string]bool{}
	for _, l := range strings.Split(string(have), "\n") {
		lines[strings.TrimSpace(l)] = true
	}
	var add []string
	linked := map[string]bool{}
	for _, l := range strings.Split(string(from), "\n") {
		for old, now := range renamed {
			if strings.Contains(l, "("+old+")") {
				l = strings.ReplaceAll(l, "("+old+")", "("+now+")")
				linked[now] = true
			}
		}
		if t := strings.TrimSpace(l); t != "" && !lines[t] {
			lines[t] = true
			add = append(add, l)
		}
	}
	var olds []string
	for old := range renamed {
		olds = append(olds, old)
	}
	sort.Strings(olds)
	for _, old := range olds {
		if now := renamed[old]; !linked[now] && !strings.Contains(string(have), "("+now+")") {
			add = append(add, "- ["+strings.TrimSuffix(old, ".md")+", as "+machine+" had it]("+now+")")
		}
	}
	if len(add) == 0 {
		return have, false
	}
	text := string(have)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return []byte(text + strings.Join(add, "\n") + "\n"), true
}

// clear removes the files merge took from dir, each only while it still
// holds what merge copied (a note saved or changed meanwhile stays, for
// the next move), and dir when that empties it.
func clear(dir string, taken map[string][]byte) error {
	for name, data := range taken {
		if same, err := sameText(filepath.Join(dir, name), data); err != nil || !same {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	os.Remove(dir) // only when empty
	return nil
}

// MoveIn moves the notes in each of from (the runtime's own folders for
// the crew) into the crew's memory folder, merging them with what's
// there, and leaves a pointer in the first of from. A folder it can't
// read or write stops it with an error, and the notes it hadn't moved yet
// stay where they were.
func MoveIn(crew string, from []string, machine string) (Result, error) {
	var res Result
	seen := map[string]bool{}
	// Every folder is merged before any is cleared: a stop on one leaves
	// them all as they were.
	var dirs []string
	var takes []map[string][]byte
	for _, d := range from {
		if seen[paths.Real(d)] {
			continue
		}
		seen[paths.Real(d)] = true
		r, taken, err := merge(d, Dir(crew), machine)
		res.Moved += r.Moved
		res.Copies = append(res.Copies, r.Copies...)
		if err != nil {
			return res, err
		}
		dirs, takes = append(dirs, d), append(takes, taken)
	}
	for i, d := range dirs {
		if err := clear(d, takes[i]); err != nil {
			return res, err
		}
	}
	if len(from) > 0 {
		return res, Point(crew, from[0])
	}
	return res, nil
}

// Point writes the pointer to the crew's memory into dir, the runtime's
// own folder for it, when the crew has a memory folder and dir holds no
// notes. It never writes over a note.
func Point(crew, dir string) error {
	if _, err := os.Stat(Dir(crew)); err != nil {
		return nil
	}
	if holds, err := Holds(dir); err != nil || holds {
		return err
	}
	want := []byte(Pointer(crew))
	if same, err := sameText(filepath.Join(dir, Index), want); err == nil && same {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return fsx.WriteFile(filepath.Join(dir, Index), want, 0o644)
}

// Copy copies the crew's notes into to, merged as MoveOut merges, and
// leaves the crew's as they are.
func Copy(crew, to, machine string) (Result, error) {
	res, _, err := merge(Dir(crew), to, machine)
	return res, err
}

// MoveOut moves the crew's notes into to, the runtime's own folder for
// the crew, merging them with what's there (a pointer there goes), and
// removes the crew's memory folder.
func MoveOut(crew, to, machine string) (Result, error) {
	if data, err := os.ReadFile(filepath.Join(to, Index)); err == nil && IsPointer(data) {
		if holds, err := Holds(to); err == nil && !holds {
			os.Remove(filepath.Join(to, Index))
		}
	}
	res, taken, err := merge(Dir(crew), to, machine)
	if err != nil {
		return res, err
	}
	return res, clear(Dir(crew), taken)
}

// RemovePointer removes the pointer in dir, when it is exactly cadrei's,
// and dir when that empties it. It reports whether it removed one.
func RemovePointer(dir string) bool {
	file := filepath.Join(dir, Index)
	data, err := os.ReadFile(file)
	if err != nil || !IsPointer(data) || os.Remove(file) != nil {
		return false
	}
	os.Remove(dir)
	return true
}

var notName = regexp.MustCompile(`[^A-Za-z0-9-]+`)

// Machine is this machine's short name, for the names of notes that
// clashed: its host name up to the first dot, in letters, digits and
// dashes.
func Machine() string {
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	host = strings.Trim(notName.ReplaceAllString(host, "-"), "-")
	if host == "" {
		return "this-machine"
	}
	return strings.ToLower(host)
}
