package cadreis

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/registry"
)

// A cadrei's places are where its projects are on this machine, in its
// local folder (places.json), a JSON object of project name to folder
// (~/... under the home folder, absolute otherwise). The registry travels
// with the cadrei and holds no local paths; each machine keeps its own
// places, which git ignores.

// PlacesFile is the cadrei's places file.
func PlacesFile(c Cadrei) string { return filepath.Join(LocalDir(c.Path), "places.json") }

// oldPlacesFile is where 0.2.0 kept a cadrei's places.
func oldPlacesFile(c Cadrei) string { return filepath.Join(ConfigDir(), "places", c.Name+".json") }

// Places returns the cadrei's places as written (~ not expanded).
func Places(c Cadrei) map[string]string {
	m, _, err := placesOf(PlacesFile(c))
	if err != nil {
		return map[string]string{}
	}
	return m
}

// ReadPlaces is Places for a caller that must know every place, such as
// one that deletes what holds them: its error says why the file can't be
// read, where Places counts that as no places.
func ReadPlaces(c Cadrei) (map[string]string, error) {
	m, _, err := placesOf(PlacesFile(c))
	return m, err
}

// OldPlaces is ReadPlaces for the 0.2.0 places still in ~/.cadrei/config,
// by file: the notes of crews cadrei hasn't moved them into yet.
func OldPlaces() (map[string]map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(ConfigDir(), "places", "*.json"))
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	for _, f := range files {
		m, _, err := placesOf(f)
		if err != nil {
			return nil, err
		}
		out[f] = m
	}
	return out, nil
}

// PlaceDir is the folder a place names, or "" for one cadrei never writes
// (a relative path).
func PlaceDir(value string) string {
	p := expandHome(value)
	if !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

// LockPlaces holds a crew's lock on its places, which every change to
// them takes, until Release.
func LockPlaces(c Cadrei) (*fsx.Lock, error) { return lockLocal(c) }

// Place is the folder of a project on this machine, or "" when it has
// none. A relative entry, which cadrei never writes, is ignored.
func Place(c Cadrei, project string) string { return PlaceDir(Places(c)[project]) }

// SetPlace records where a project is on this machine; dir "" forgets it.
// A file that does not parse is refused rather than replaced, which would
// lose every other project's place.
func SetPlace(c Cadrei, project, dir string) error {
	l, err := lockLocal(c)
	if err != nil {
		return err
	}
	defer l.Release()
	places, m, err := placesOf(PlacesFile(c))
	if err != nil {
		return err
	}
	if dir == "" {
		delete(places, project)
	} else {
		places[project] = placeValue(dir)
	}
	return setPlaces(PlacesFile(c), places, m)
}

// ProjectDir is where a cadrei's project is on this machine, or "" when it
// has no place here.
func ProjectDir(c Cadrei, e *registry.Entry) string { return Place(c, e.Name) }

// Where says how a project's folder is on this machine: "present",
// "not here" (no place recorded), "missing" (the folder is gone), or
// "drive" (it is on a drive that is not connected).
func Where(dir string) string {
	if dir == "" {
		return "not here"
	}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return "present"
	}
	if onMissingDrive(dir) {
		return "drive"
	}
	return "missing"
}

// onMissingDrive reports whether dir is on a removable or network drive
// whose mount point is not there: /Volumes/<drive> on macOS, /media,
// /run/media or /mnt on Linux.
func onMissingDrive(dir string) bool {
	parts := strings.Split(filepath.Clean(dir), string(filepath.Separator))
	// parts[0] is "" for an absolute path.
	var mount string
	switch {
	case len(parts) > 2 && (parts[1] == "Volumes" || parts[1] == "mnt"):
		mount = filepath.Join("/", parts[1], parts[2])
	case len(parts) > 3 && parts[1] == "media":
		mount = filepath.Join("/", parts[1], parts[2], parts[3])
	case len(parts) > 4 && parts[1] == "run" && parts[2] == "media":
		mount = filepath.Join("/", parts[1], parts[2], parts[3], parts[4])
	default:
		return false
	}
	_, err := os.Stat(mount)
	return err != nil
}
