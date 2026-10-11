package cadreis

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
)

// An archive packs a whole setup away, so a reinstall is a true first run
// and the setup can come back at the same paths:
// ~/.cadrei-archive/<YYYY-MM-DD-HHMM>/ with cadreis/<name>/, config/,
// other/, manifest.json and README.txt. Nothing in it is deleted.

// ArchiveRoot is where archives are kept.
func ArchiveRoot() string { return filepath.Join(paths.Home(), ".cadrei-archive") }

// Manifest is an archive's manifest.json.
type Manifest struct {
	Version       int            `json:"version"`
	ArchivedAt    time.Time      `json:"archived_at"`
	CadreiVersion string         `json:"cadrei_version"`
	Home          string         `json:"home"`
	Cadreis       []ArchivedCrew `json:"cadreis"`
	Machine       Machine        `json:"machine"`
	Steps         []string       `json:"steps"`
	RootLink      string         `json:"root_link,omitempty"`   // where ~/.cadrei linked to, when it was a link
	ConfigLink    string         `json:"config_link,omitempty"` // what ~/.cadrei/config linked to, as written, when it was a link
	MadeBy        string         `json:"made_by,omitempty"`     // the machine that made it: Archive.Seal, keyed by MachineID
	RestoredAt    *time.Time     `json:"restored_at"`
}

// ArchivedCrew is one crew in an archive.
type ArchivedCrew struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Default bool   `json:"default"`
	Backup  Backup `json:"backup"`
}

// Backup is a crew's backup as the archive checked it.
type Backup struct {
	Remote         string `json:"remote,omitempty"`
	IsPushed       bool   `json:"is_pushed"`
	HasUncommitted bool   `json:"has_uncommitted"`
}

// Machine is what the archive notes about this machine's setup.
type Machine struct {
	EveryChatHook string `json:"every_chat_hook"` // on or off
	SkillLinked   bool   `json:"skill_linked"`
	Orchestrator  string `json:"orchestrator"` // tmux or terminal: the default crew's
}

// Archive is one archive folder and its manifest.
type Archive struct {
	Dir      string
	Manifest Manifest
}

// StepDone is the manifest step that says an archive finished.
const StepDone = "done"

// Finished reports whether the archive's run finished.
func (a Archive) Finished() bool {
	for _, s := range a.Manifest.Steps {
		if s == StepDone {
			return true
		}
	}
	return false
}

// Archives lists the archives, newest first.
func Archives() []Archive {
	entries, _ := os.ReadDir(ArchiveRoot())
	var out []Archive
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if a, err := ReadArchive(filepath.Join(ArchiveRoot(), e.Name())); err == nil {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ArchivedAt.After(out[j].Manifest.ArchivedAt) })
	return out
}

// Unreadable lists the folders in ArchiveRoot that hold no manifest.json
// cadrei can read. Their crews are still in their cadreis folders.
func Unreadable() []string {
	entries, _ := os.ReadDir(ArchiveRoot())
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := ReadArchive(filepath.Join(ArchiveRoot(), e.Name())); err != nil {
			out = append(out, e.Name())
		}
	}
	return out
}

// ManifestVersion is the archive layout this cadrei makes and restores.
const ManifestVersion = 1

// ReadArchive reads an archive folder.
func ReadArchive(dir string) (Archive, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return Archive{}, err
	}
	a := Archive{Dir: dir}
	if err := json.Unmarshal(raw, &a.Manifest); err != nil {
		return Archive{}, fmt.Errorf("%s isn't an archive cadrei can read", Tilde(dir))
	}
	// An archive outlives the cadrei that made it: one a newer cadrei
	// made may hold what this one would restore only in part.
	if a.Manifest.Version > ManifestVersion {
		return Archive{}, fmt.Errorf("%s was made by a newer cadrei (archive version %d). Upgrade cadrei to restore it; nothing was changed", Tilde(dir), a.Manifest.Version)
	}
	// A manifest can come from anywhere (an archive copied from another
	// machine): every crew name must be one cadrei could have made, so a
	// restore never reaches outside ~/.cadrei or the archive, and named
	// once. Two names that differ only in case (made on a case-sensitive
	// disk) can both be read; where the disk can't hold both, the second
	// one's move stops at the first, and nothing is lost.
	seen := map[string]bool{}
	for _, c := range a.Manifest.Cadreis {
		if err := CheckName(c.Name); err != nil || seen[c.Name] {
			return Archive{}, fmt.Errorf("%s names a crew %q that cadrei can't restore", Tilde(dir), c.Name)
		}
		seen[c.Name] = true
	}
	// Its folders must be real folders: a link would lead a restore out of
	// the archive, to whatever it points at. The one link it may hold is
	// a linked config that uninstall moved as it was (7.8.1).
	for _, d := range ArchiveFolders {
		if st, err := os.Lstat(filepath.Join(dir, d)); err == nil && !st.IsDir() && !(d == "config" && a.LinkedConfig()) {
			return Archive{}, fmt.Errorf("cadrei won't restore from %s: its %s folder is a link or a file", Tilde(dir), d)
		}
	}
	return a, nil
}

// LinkedConfig reports whether the archive's config is the link
// ~/.cadrei/config was, moved in as it was: the manifest records the same
// target, and the archive is in ~/.cadrei-archive, made on this machine.
// It comes back as that link.
func (a Archive) LinkedConfig() bool {
	p := filepath.Join(a.Dir, "config")
	st, err := os.Lstat(p)
	if err != nil || st.Mode()&fs.ModeSymlink == 0 || a.Manifest.ConfigLink == "" {
		return false
	}
	target, err := os.Readlink(p)
	return err == nil && target == a.Manifest.ConfigLink && a.MadeHere()
}

// machineFile holds this machine's archive id.
func machineFile() string { return filepath.Join(ArchiveRoot(), ".machine") }

// MachineID is this machine's archive id, made on first use and kept in
// ~/.cadrei-archive/.machine (outside ~/.cadrei, which an archive moves
// away). Each archive made here records a seal made with it (Seal), so an
// archive copied into ~/.cadrei-archive from elsewhere isn't taken for one
// of this machine's.
func MachineID() (string, error) {
	if id := readMachineID(); id != "" {
		return id, nil
	}
	if err := os.MkdirAll(ArchiveRoot(), 0o700); err != nil {
		return "", err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	if err := fsx.WriteFile(machineFile(), []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// readMachineID is the id in machineFile, or "": a plain file only, never
// one it links to.
func readMachineID() string {
	if st, err := os.Lstat(machineFile()); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	raw, err := os.ReadFile(machineFile())
	if id := strings.TrimSpace(string(raw)); err == nil && len(id) == 32 {
		return id
	}
	return ""
}

// MadeHere reports whether this machine made the archive: it's in
// ~/.cadrei-archive and records this machine's seal for it. Only such an
// archive may bring back links, a linked config or the place ~/.cadrei
// linked to, and skip the warning about its crews' git settings.
func (a Archive) MadeHere() bool {
	id := readMachineID()
	return id != "" && hmac.Equal([]byte(a.Manifest.MadeBy), []byte(a.seal(id))) &&
		paths.Real(filepath.Dir(a.Dir)) == paths.Real(ArchiveRoot())
}

// Seal records that this machine made the archive: a mark of its folder's
// name and its time, keyed by this machine's id. The id itself never goes
// into an archive, which may leave the machine; the mark fits that one
// archive only, so it can't be copied into another.
func (a *Archive) Seal() error {
	id, err := MachineID()
	if err != nil {
		return err
	}
	a.Manifest.MadeBy = a.seal(id)
	return nil
}

func (a Archive) seal(id string) string {
	mac := hmac.New(sha256.New, []byte(id))
	mac.Write([]byte(filepath.Base(a.Dir) + "\n" + a.Manifest.ArchivedAt.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(mac.Sum(nil))
}

// ArchiveFolders are the folders an archive keeps its parts in.
var ArchiveFolders = []string{"cadreis", "config", "other"}

// Others lists what an archive keeps of ~/.cadrei besides its crews and
// config.
func (a Archive) Others() []string {
	entries, _ := os.ReadDir(filepath.Join(a.Dir, "other"))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// Save writes the manifest.
func (a *Archive) Save() error {
	raw, err := json.MarshalIndent(a.Manifest, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFile(filepath.Join(a.Dir, "manifest.json"), append(raw, '\n'), 0o600)
}

// Step records a step as done, in the manifest.
func (a *Archive) Step(step string) error {
	for _, s := range a.Manifest.Steps {
		if s == step {
			return nil
		}
	}
	a.Manifest.Steps = append(a.Manifest.Steps, step)
	return a.Save()
}

// HasCrew reports whether the archive holds the named crew.
func (a Archive) HasCrew(name string) bool {
	for _, c := range a.Manifest.Cadreis {
		if c.Name == name {
			return true
		}
	}
	return false
}

// Has reports whether a step is done.
func (a Archive) Has(step string) bool {
	for _, s := range a.Manifest.Steps {
		if s == step {
			return true
		}
	}
	return false
}

// NewArchive makes a new archive folder named for now, with its manifest.
func NewArchive(m Manifest) (*Archive, error) {
	name := m.ArchivedAt.Format("2006-01-02-1504")
	dir := filepath.Join(ArchiveRoot(), name)
	for i := 2; ; i++ {
		if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
			break
		}
		dir = filepath.Join(ArchiveRoot(), fmt.Sprintf("%s-%d", name, i))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	a := &Archive{Dir: dir, Manifest: m}
	if err := a.Seal(); err != nil {
		return nil, err
	}
	return a, a.Save()
}

// Move moves src to dst: a rename, or, when that can't work (another
// disk), a copy into a partial folder beside dst that is checked, renamed
// to dst, and then the original removed. A link is moved as a link. So
// dst is only ever whole: a partial left by a stop is redone, and a dst
// with the original still beside it means the stop came while removing
// the original, which is finished. It never removes the only whole copy,
// and never anything it didn't make: a partial folder is removed only
// when it carries cadrei's mark.
func Move(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		if _, err := os.Lstat(src); err != nil {
			return nil // moved already
		}
		if fsx.SameTree(src, dst) == nil {
			// A move stopped after its rename: the copy's name goes to disk
			// before the original goes, as below.
			if err := fsx.SyncDir(filepath.Dir(dst)); err != nil {
				return err
			}
			return removeOriginal(src)
		}
		return fmt.Errorf("%s is already there, with other files", Tilde(dst))
	}
	partial := PartialOf(dst)
	if err := clearPartial(partial); err != nil {
		return err
	}
	if err := rename(src, dst); err == nil {
		return nil
	}
	if err := os.Mkdir(partial, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(partial, partialMark), []byte("cadrei copies here while it moves "+filepath.Base(dst)+"\n"), 0o600); err != nil {
		os.RemoveAll(partial)
		return err
	}
	copied := filepath.Join(partial, filepath.Base(dst))
	if err := fsx.CopyTree(src, copied); err != nil {
		os.RemoveAll(partial)
		return err
	}
	if err := fsx.SameTree(src, copied); err != nil {
		os.RemoveAll(partial)
		return fmt.Errorf("%s", strings.Replace(err.Error(), src, Tilde(src), 1))
	}
	if err := os.Rename(copied, dst); err != nil {
		return err
	}
	// The copy is on disk (CopyTree flushed it) and named dst on disk too
	// before the original goes.
	if err := fsx.SyncDir(filepath.Dir(dst)); err != nil {
		return err
	}
	os.RemoveAll(partial)
	return removeOriginal(src)
}

// rename is os.Rename; a test makes it fail, as between disks.
var rename = os.Rename

// removeOriginal removes a moved folder's original, once its copy is on
// disk; tests check what was flushed by then.
var removeOriginal = os.RemoveAll

// PartialOf is the folder Move copies dst into first: beside it, hidden,
// and named as cadrei's own.
func PartialOf(dst string) string {
	return filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".cadrei-partial")
}

// partialMark is the file that marks a partial folder as cadrei's.
const partialMark = ".cadrei-partial"

// clearPartial removes a partial folder a stopped Move left, and refuses
// anything at its place that doesn't carry cadrei's mark.
func clearPartial(partial string) error {
	if _, err := os.Lstat(partial); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if st, err := os.Lstat(filepath.Join(partial, partialMark)); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("%s is in the way, and it isn't cadrei's", Tilde(partial))
	}
	return os.RemoveAll(partial)
}
