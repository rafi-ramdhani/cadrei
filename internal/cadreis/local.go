package cadreis

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
)

// A cadrei keeps this machine's own notes about it in its local folder,
// which git ignores: places.json (where each project is on this machine),
// state.json (the orchestrator's conversation id) and an optional
// cadrei.conf that overrides the cadrei's on this machine. So everything
// about a cadrei lives in it, and ~/.cadrei/config holds only what cadrei
// can rebuild or ask again.

// LocalDir is a cadrei's local folder. The program sets it to a folder
// beside the runtime's build folder, which members can't edit.
var LocalDir = func(cadrei string) string { return filepath.Join(cadrei, ".local") }

// localVersion is the version of places.json and state.json.
const localVersion = 1

// readLocal reads a local JSON file whole, keeping keys it doesn't know.
// A file that isn't a JSON object is an error, so it is never replaced.
func readLocal(path string) (map[string]json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return m, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, fmt.Errorf("%s is not a JSON object; fix or remove it (nothing was changed)", Tilde(path))
	}
	return m, nil
}

// versionOf is a local file's version: 0 when it has none.
func versionOf(m map[string]json.RawMessage) int {
	var v int
	json.Unmarshal(m["version"], &v)
	return v
}

// writeLocal writes a local JSON file with its version. It never writes
// over a file a newer cadrei wrote, nor lowers a version: an older cadrei
// would stamp its own over what it doesn't know (after a downgrade, say).
func writeLocal(path string, m map[string]json.RawMessage) error {
	v := versionOf(m)
	if on, err := readLocal(path); err == nil && versionOf(on) > v {
		v = versionOf(on)
	}
	if v > localVersion {
		return fmt.Errorf("%s was written by a newer cadrei. Upgrade cadrei; nothing was changed", Tilde(path))
	}
	m["version"], _ = json.Marshal(localVersion)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFile(path, append(data, '\n'), 0o600)
}

// lockLocal serializes changes to a cadrei's local files.
func lockLocal(c Cadrei) (*fsx.Lock, error) {
	dir := LocalDir(c.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	l, err := fsx.Acquire(filepath.Join(dir, ".lock"), 10*time.Second)
	if errors.Is(err, fsx.ErrBusy) {
		return nil, fmt.Errorf("another cadrei command is changing %s. Try again", Tilde(dir))
	}
	return l, err
}

// OrchestratorID is the last conversation id cadrei issued to a cadrei's
// orchestrator: it resumes only that one.
func OrchestratorID(c Cadrei) string {
	m, err := readLocal(filepath.Join(LocalDir(c.Path), "state.json"))
	if err != nil {
		return ""
	}
	var id string
	json.Unmarshal(m["orchestrator_id"], &id)
	return id
}

// SetOrchestratorID records the orchestrator's conversation id.
func SetOrchestratorID(c Cadrei, id string) error {
	l, err := lockLocal(c)
	if err != nil {
		return err
	}
	defer l.Release()
	path := filepath.Join(LocalDir(c.Path), "state.json")
	m, err := readLocal(path)
	if err != nil {
		return err
	}
	m["orchestrator_id"], _ = json.Marshal(id)
	return writeLocal(path, m)
}

// MemoryNotice is when cadrei first told the user that the crew keeps the
// orchestrator's memory, or "": nothing of it is committed before.
func MemoryNotice(c Cadrei) string {
	m, err := readLocal(filepath.Join(LocalDir(c.Path), "state.json"))
	if err != nil {
		return ""
	}
	var when string
	json.Unmarshal(m["memory_notice"], &when)
	return when
}

// SetMemoryNotice records when the user was told; "" forgets it, so a
// later move into the crew tells them again.
func SetMemoryNotice(c Cadrei, when string) error {
	l, err := lockLocal(c)
	if err != nil {
		return err
	}
	defer l.Release()
	path := filepath.Join(LocalDir(c.Path), "state.json")
	m, err := readLocal(path)
	if err != nil {
		return err
	}
	if when == "" {
		if _, ok := m["memory_notice"]; !ok {
			return nil
		}
		delete(m, "memory_notice")
	} else {
		m["memory_notice"], _ = json.Marshal(when)
	}
	return writeLocal(path, m)
}

// RecordServer notes the tmux server (its socket's path) a session of the
// crew was started on, so the commands that move or delete crews look
// for its teams there whatever server the environment names then.
func RecordServer(c Cadrei, socket string) error {
	l, err := lockLocal(c)
	if err != nil {
		return err
	}
	defer l.Release()
	path := filepath.Join(LocalDir(c.Path), "state.json")
	m, err := readLocal(path)
	if err != nil {
		return err
	}
	var servers []string
	json.Unmarshal(m["tmux_servers"], &servers)
	if slices.Contains(servers, socket) {
		return nil
	}
	m["tmux_servers"], _ = json.Marshal(append(servers, socket))
	return writeLocal(path, m)
}

// Servers are the tmux servers the crew's sessions were started on.
func Servers(c Cadrei) []string {
	m, err := readLocal(filepath.Join(LocalDir(c.Path), "state.json"))
	if err != nil {
		return nil
	}
	var servers []string
	json.Unmarshal(m["tmux_servers"], &servers)
	return servers
}

// placesOf reads a cadrei's places: project name to folder, as written.
func placesOf(path string) (map[string]string, map[string]json.RawMessage, error) {
	m, err := readLocal(path)
	if err != nil {
		return nil, nil, err
	}
	places := map[string]string{}
	if raw, ok := m["places"]; ok {
		if json.Unmarshal(raw, &places) != nil {
			return nil, nil, fmt.Errorf("%s has places that aren't project folders; fix or remove it (nothing was changed)", Tilde(path))
		}
	}
	return places, m, nil
}

// setPlaces writes a cadrei's places, keeping the file's other keys.
func setPlaces(path string, places map[string]string, m map[string]json.RawMessage) error {
	m["places"], _ = json.Marshal(places)
	return writeLocal(path, m)
}

// placeValue writes a folder as places keep it: ~/... under the home
// folder, absolute otherwise.
func placeValue(dir string) string { return Tilde(paths.Real(dir)) }

// MoveIn moves what 0.2.0 kept about a cadrei in ~/.cadrei/config into
// its local folder (its places, its orchestrator's id), and makes git
// ignore that folder. It reports whether it moved anything. Running it
// again is safe: a step that's done is skipped, and when both the old and
// the new file exist, the new one wins; lost names the projects whose old
// place lost, kept in places.0.2.0.json. A folder that isn't a crew is
// never written to.
func MoveIn(c Cadrei) (moved bool, lost []string, err error) {
	// A crew a newer cadrei changed is never written to (Format).
	if !c.Present() || NewerFormat(c.Path) != "" {
		return false, nil, nil
	}
	l, err := lockLocal(c)
	if err != nil {
		return false, nil, err
	}
	old := oldPlacesFile(c)
	if raw, err := os.ReadFile(old); err == nil {
		was := map[string]string{}
		if json.Unmarshal(raw, &was) != nil {
			l.Release()
			return false, nil, fmt.Errorf("%s is not a JSON object of project folders; fix or remove it", Tilde(old))
		}
		places, m, err := placesOf(PlacesFile(c))
		if err != nil {
			l.Release()
			return false, nil, err
		}
		if places == nil {
			places, m = map[string]string{}, map[string]json.RawMessage{}
		}
		// Merged, not replaced: after a downgrade and an upgrade again,
		// the old file can hold places the new one lacks. Where both name
		// a project differently, the new one wins and the old file is kept.
		// 0.2.0's places that lose go into places.0.2.0.json, merged with
		// any an earlier clash put there, and are named.
		kept := filepath.Join(LocalDir(c.Path), "places.0.2.0.json")
		earlier := map[string]string{}
		if raw, err := os.ReadFile(kept); err == nil {
			json.Unmarshal(raw, &earlier)
		}
		for k, v := range was {
			switch have, ok := places[k]; {
			case !ok:
				places[k] = v
			case have != v:
				earlier[k] = v
				lost = append(lost, k)
			}
		}
		if len(lost) > 0 {
			sort.Strings(lost)
			raw, _ := json.MarshalIndent(earlier, "", "  ")
			if err := fsx.WriteFile(kept, append(raw, '\n'), 0o600); err != nil {
				l.Release()
				return false, nil, err
			}
		}
		if err := setPlaces(PlacesFile(c), places, m); err != nil {
			l.Release()
			return false, nil, err
		}
		if p, _, err := placesOf(PlacesFile(c)); err == nil && p != nil {
			os.Remove(old)
			moved = true
		}
	}
	l.Release()
	if id := GetState(oldOrchestratorKey(c.Name)); id != "" {
		now := OrchestratorID(c)
		// Two ids (a downgrade, then an upgrade): the chat used last wins.
		if now == "" || (now != id && ChatTime != nil && ChatTime(id, c.Path).After(ChatTime(now, c.Path))) {
			if err := SetOrchestratorID(c, id); err != nil {
				return moved, lost, err
			}
		}
		if OrchestratorID(c) != "" {
			SetState(oldOrchestratorKey(c.Name), "")
			moved = true
		}
	}
	if _, err := IgnoreLocal(c); err != nil {
		return moved, lost, err
	}
	return moved, lost, nil
}

// HasOldNotes reports whether 0.2.0 kept notes about a crew in
// ~/.cadrei/config that MoveIn would move.
func HasOldNotes(c Cadrei) bool {
	_, err := os.Stat(oldPlacesFile(c))
	return err == nil || GetState(oldOrchestratorKey(c.Name)) != ""
}

// ChatTime is when the orchestrator's chat id was last written to, in the
// cadrei at dir, or the zero time; the runtime sets it.
var ChatTime func(id, dir string) time.Time

// Leftovers lists the crews whose 0.2.0 notes are still in ~/.cadrei/config
// (places or the orchestrator's chat id), which MoveIn couldn't move.
func Leftovers() []string {
	seen := map[string]bool{}
	var out []string
	files, _ := filepath.Glob(filepath.Join(ConfigDir(), "places", "*.json"))
	for _, f := range files {
		n := strings.TrimSuffix(filepath.Base(f), ".json")
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, k := range StateKeys() {
		if n, ok := strings.CutPrefix(k, "orchestrator_id:"); ok && GetState(k) != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// IgnoreLocal adds the local folder to the cadrei's .gitignore when it
// isn't there, and commits that. It reports whether it added it.
func IgnoreLocal(c Cadrei) (bool, error) {
	rel, err := filepath.Rel(c.Path, LocalDir(c.Path))
	if err != nil {
		return false, err
	}
	line := "/" + filepath.ToSlash(rel) + "/"
	file := filepath.Join(c.Path, ".gitignore")
	raw, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == line {
			return false, nil
		}
	}
	text := string(raw)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "# This machine's own notes: where the projects are, the orchestrator's chat.\n" + line + "\n"
	if err := fsx.WriteFile(file, []byte(text), 0o644); err != nil {
		return false, err
	}
	Commit(c.Path, "Keep this machine's notes out of git", ".gitignore")
	return true, nil
}
