package project

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/registry"
)

// CheckName refuses a project name that could leave its folder.
func CheckName(name string) error {
	if !registry.ValidName(name) {
		return errors.New("a project's name can use letters, digits, ., - and _ (but not ..)")
	}
	return nil
}

// Clone clones repo (owner/repo through gh when signed in, else over
// https; a URL or local path as it is) into dir. A repo comes from a
// registry that may be shared, so one that starts with - (an option to
// git) is refused, and every word after the options is passed after --.
func Clone(repo, dir string) error {
	if strings.HasPrefix(repo, "-") || strings.HasPrefix(dir, "-") {
		return fmt.Errorf("couldn't clone %s: a repo can't start with -", Redact(repo))
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch {
	case strings.Count(repo, "/") >= 2 || strings.HasPrefix(repo, "/") || strings.HasPrefix(repo, ".") || strings.Contains(repo, ":"):
		cmd = exec.Command("git", "clone", "-q", "--", repo, dir)
	case ghSignedIn():
		cmd = exec.Command("gh", "repo", "clone", repo, dir, "--", "-q")
	default:
		cmd = exec.Command("git", "clone", "-q", "--", "https://github.com/"+repo+".git", dir)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		return fmt.Errorf("couldn't clone %s: %s", Redact(repo), strings.TrimSuffix(strings.TrimPrefix(line, "fatal: "), "."))
	}
	return nil
}

func ghSignedIn() bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	return exec.Command("gh", "auth", "status").Run() == nil
}

// SameRepo reports whether two spellings name one repository: the same
// host and path (owner/repo on GitHub), compared without case, a .git
// suffix or a port. owner/repo alone is on github.com, as Clone reads it.
// A local path or a file:// URL matches only itself.
func SameRepo(a, b string) bool {
	ia, ib := repoID(a), repoID(b)
	return ia != "" && ia == ib
}

// repoID is what SameRepo compares: "<host>/<path>" in lower case, or
// "local:<path>" for a repository on disk, or "" when there is none. A
// network spelling with \, # or ? is refused (""): git reads its host
// differently from a simple split (https://evil.example#@github.com/x/y
// is on evil.example), so it never matches anything.
func repoID(repo string) string {
	r := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(repo), "/"), ".git")
	switch {
	case r == "":
		return ""
	case strings.HasPrefix(strings.ToLower(r), "file://"):
		return "local:" + filepath.Clean(r[len("file://"):])
	case strings.HasPrefix(r, "/") || strings.HasPrefix(r, ".") || strings.HasPrefix(r, "~"):
		return "local:" + filepath.Clean(r)
	case strings.ContainsAny(r, `\#?`):
		return ""
	}
	var host, rest string
	switch {
	case strings.Contains(r, "://"):
		u, err := url.Parse(r)
		if err != nil || u.Opaque != "" || !plainUser(u.User) {
			return ""
		}
		host, rest = u.Hostname(), u.Path
	case strings.Contains(r, ":") && !strings.Contains(r[:strings.Index(r, ":")], "/"):
		// scp-like: [user@]host:owner/repo
		host, rest, _ = strings.Cut(r, ":")
		if at := strings.LastIndex(host, "@"); at >= 0 {
			if !userRule.MatchString(host[:at]) {
				return ""
			}
			host = host[at+1:]
		}
	case strings.Count(r, "/") == 1:
		host, rest = "github.com", r
	default:
		host, rest, _ = strings.Cut(r, "/")
	}
	rest = strings.Trim(rest, "/")
	if host == "" || rest == "" || strings.Contains(host, "@") {
		return ""
	}
	return strings.ToLower(host + "/" + rest)
}

// userRule is the user part SameRepo accepts in a URL (git@, me@): a
// plain name, with no password and nothing that could hide a host.
var userRule = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func plainUser(u *url.Userinfo) bool {
	if u == nil {
		return true
	}
	_, hasPassword := u.Password()
	return !hasPassword && userRule.MatchString(u.Username())
}

// Redact takes the secret out of a remote URL before cadrei stores or
// shows it: all of an http or https URL's user part (a token often goes
// there, alone or as a password), and an ssh URL's password (its user,
// like git@, is kept). An scp-style git@host:path has no password and is
// kept as it is.
func Redact(repo string) string {
	u, err := url.Parse(repo)
	if err != nil {
		// Git takes URLs Go won't parse (a bare % in a password, say): the
		// user part goes whole, whatever is in it.
		return cutUser(repo)
	}
	if u.User == nil || u.Host == "" {
		return repo
	}
	_, password := u.User.Password()
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "git+https", "git+http", "ftp", "ftps", "git":
		u.User = nil
	case "ssh", "git+ssh", "ssh+git":
		if !password {
			return repo
		}
		u.User = url.User(u.User.Username())
	default:
		// Any other scheme: a password goes, with its user.
		if !password {
			return repo
		}
		u.User = nil
	}
	return u.String()
}

// cutUser drops what's between a URL's "://" and the last "@" before its
// path; anything else is kept as it is.
func cutUser(repo string) string {
	scheme, rest, ok := strings.Cut(repo, "://")
	if !ok {
		return repo
	}
	host := rest
	if i := strings.Index(rest, "/"); i >= 0 {
		host = rest[:i]
	}
	at := strings.LastIndex(host, "@")
	if at < 0 {
		return repo
	}
	return scheme + "://" + rest[at+1:]
}

// OwnerRepo reduces a repository spelling (owner/repo, an https or ssh URL,
// with or without .git) to owner/repo, lower case, for comparing.
func OwnerRepo(repo string) string {
	r := strings.TrimSuffix(strings.TrimSpace(repo), "/")
	r = strings.TrimSuffix(r, ".git")
	if i := strings.LastIndexAny(r, ":/"); i >= 0 {
		if j := strings.LastIndexAny(r[:i], ":/"); j >= 0 {
			r = r[j+1:]
		}
	}
	return strings.ToLower(r)
}

// Origin returns a folder's remote.origin.url, or "".
func Origin(dir string) string {
	out, err := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TopLevel reports whether dir (physical) is the top folder of a git
// repository; a linked worktree counts.
func TopLevel(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	return err == nil && paths.Real(strings.TrimSpace(string(out))) == dir
}

// Spec is a project to add.
type Spec struct {
	Name, Repo, Team, About string
}

// Added says what Add or Link did.
type Added struct {
	Dir   string // the project's folder
	Where string // "cloned", "already" or "linked"
	Notes []string
}

// Add registers a project and clones it into the projects folder, and
// records that folder as the project's place on this machine. A folder
// already there is used when it is the same repository and refused
// otherwise. The clone comes first, so a failed clone registers nothing.
func Add(c cadreis.Cadrei, s Spec, projectsDir string, protected []string) (Added, error) {
	var a Added
	if err := CheckName(s.Name); err != nil {
		return a, err
	}
	reg, err := registry.Load(c.Registry())
	if err != nil {
		return a, err
	}
	if reg.Get(s.Name) != nil {
		return a, fmt.Errorf("%s is already a project in this crew", s.Name)
	}
	dir := filepath.Join(projectsDir, s.Name)
	if why := DestRefusal(paths.Real(dir), protected); why != "" {
		return a, fmt.Errorf("can't clone into %s: %s", cadreis.Tilde(dir), why)
	}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		if !SameRepo(Origin(dir), s.Repo) {
			return a, fmt.Errorf("%s exists and isn't a clone of %s. Link it with cadrei project add %s --path %s, or pick another name", cadreis.Tilde(dir), Redact(s.Repo), s.Name, cadreis.Tilde(dir))
		}
		a.Where = "already"
	} else {
		if err := Clone(s.Repo, dir); err != nil {
			return a, err
		}
		a.Where = "cloned"
	}
	a.Dir = dir
	reg.Add(s.Name, registry.Field{Key: "repo", Value: Redact(s.Repo)}, registry.Field{Key: "team", Value: s.Team},
		registry.Field{Key: "about", Value: s.About})
	if err := reg.Save(c.Registry()); err != nil {
		return a, err
	}
	if err := cadreis.SetPlace(c, s.Name, dir); err != nil {
		return a, err
	}
	// Without a git identity the change stays uncommitted, silently (D3).
	_, err = cadreis.Commit(c.Path, "Add project "+s.Name, "projects.yaml")
	return a, err
}

// oldCadrei refuses the top folder of a 0.1.x cadre, which is brought in,
// not linked or trusted as a project; its projects/<name> folders are
// projects like any other.
func oldCadrei(dir string) string {
	if cadreis.OldCadre(dir) {
		return "it is a cadre from 0.1.x; to bring it in, tell the orchestrator: bring in my old cadre from " + cadreis.Tilde(dir)
	}
	return ""
}

// LinkRefusal says why a folder (physical) cannot be linked as a project,
// or "".
func LinkRefusal(dir string, protected []string) string {
	if why := oldCadrei(dir); why != "" {
		return why
	}
	if why := DestRefusal(dir, protected); why != "" {
		return why
	}
	if !TopLevel(dir) {
		return "it is not the top folder of a git repository"
	}
	return ""
}

// DestRefusal says why a folder (physical) cannot hold a project, whether
// linked or cloned there, or "": a registry may be shared, so its paths are
// checked like a link. protected lists the folders the runtime loads code
// from (a project there would be loaded as a skill, for example).
func DestRefusal(dir string, protected []string) string {
	home := paths.Home()
	root := paths.Real(cadreis.Root())
	switch {
	case dir == "/":
		return "it is the root folder"
	case same(dir, home):
		return "it is your home folder"
	case paths.Inside(dir, root):
		return "it is inside ~/.cadrei, where cadrei keeps its own files"
	case paths.Holds(dir, root):
		return "it contains ~/.cadrei"
	}
	for _, p := range protected {
		switch {
		case paths.Inside(dir, p):
			return "it is inside " + cadreis.Tilde(p)
		case paths.Holds(dir, p):
			return "it contains " + cadreis.Tilde(p)
		}
	}
	list, _ := cadreis.List()
	for _, c := range list {
		if paths.Holds(dir, c.Path) {
			return "it holds the crew " + c.Name
		}
		if paths.Inside(dir, c.Path) {
			return "it's inside the crew " + c.Name
		}
	}
	return ""
}

// Link registers a folder the user already has: it must be the top folder
// of a git repository and not cadrei's own. The repo is the one given, else
// the folder's origin. The folder is recorded as the project's place on
// this machine, never in the registry.
func Link(c cadreis.Cadrei, s Spec, dir string, protected []string) (Added, error) {
	var a Added
	if err := CheckName(s.Name); err != nil {
		return a, err
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return a, fmt.Errorf("%s isn't a folder", cadreis.Tilde(dir))
	}
	phys := paths.Real(dir)
	if why := LinkRefusal(phys, protected); why != "" {
		return a, fmt.Errorf("%s can't be linked: %s", cadreis.Tilde(dir), why)
	}
	reg, err := registry.Load(c.Registry())
	if err != nil {
		return a, err
	}
	if reg.Get(s.Name) != nil {
		return a, fmt.Errorf("%s is already a project in this crew", s.Name)
	}
	for _, e := range reg.Entries() {
		if d := cadreis.ProjectDir(c, e); d != "" && paths.Real(d) == phys {
			return a, fmt.Errorf("%s is already linked as %s", cadreis.Tilde(dir), e.Name)
		}
	}
	for _, other := range cadreis.Linking(phys) {
		if other.Path != c.Path {
			a.Notes = append(a.Notes, "The crew "+other.Name+" links this folder too.")
		}
	}
	repo := s.Repo
	if repo == "" {
		repo = Origin(phys)
	}
	if repo == "" {
		a.Notes = append(a.Notes, s.Name+" has no GitHub repo, so it won't come along when you restore on another machine.")
	}
	reg.Add(s.Name, registry.Field{Key: "repo", Value: Redact(repo)}, registry.Field{Key: "team", Value: s.Team},
		registry.Field{Key: "about", Value: s.About})
	if err := reg.Save(c.Registry()); err != nil {
		return a, err
	}
	if err := cadreis.SetPlace(c, s.Name, phys); err != nil {
		return a, err
	}
	// Without a git identity the change stays uncommitted, silently (D3).
	_, err = cadreis.Commit(c.Path, "Add project "+s.Name, "projects.yaml")
	a.Dir, a.Where = phys, "linked"
	return a, err
}

// SyncResult is what Sync did with one project.
type SyncResult struct {
	Name, State, Dir string // State: "present", "cloned", "found", "no repo", "no folder", "drive", "failed"
	Err              error
}

// Sync gives every registry project with a repo a folder on this machine:
// it clones each one that has none, to its recorded place, else into the
// projects folder (projectsDir, "" when not set), and records the place.
// A clone of the same repository already there is used as it is. A place
// on a drive that is not connected is left alone. Parent folders are made
// under the home folder only.
func Sync(c cadreis.Cadrei, projectsDir string, protected []string) ([]SyncResult, error) {
	reg, err := registry.Load(c.Registry())
	if err != nil {
		return nil, err
	}
	var out []SyncResult
	for _, e := range reg.Entries() {
		d := cadreis.ProjectDir(c, e)
		where := cadreis.Where(d)
		if d == "" && projectsDir != "" {
			d = filepath.Join(projectsDir, e.Name)
			// Entries hold valid names only; a clone still never goes
			// anywhere but straight into the projects folder.
			if filepath.Dir(d) != filepath.Clean(projectsDir) {
				out = append(out, SyncResult{Name: e.Name, State: "failed", Err: fmt.Errorf("%s can't be a folder name", e.Name)})
				continue
			}
		}
		r := SyncResult{Name: e.Name, Dir: d}
		st, statErr := os.Stat(d)
		switch {
		case where == "present":
			r.State = "present"
		case where == "drive":
			r.State = "drive"
		case e.Get("repo") == "":
			r.State = "no repo"
		case d == "":
			r.State = "no folder"
		case DestRefusal(paths.Real(d), protected) != "":
			r.State, r.Err = "failed", fmt.Errorf("it can't go into %s: %s", cadreis.Tilde(d), DestRefusal(paths.Real(d), protected))
		case statErr == nil && st.IsDir():
			if !SameRepo(Origin(d), e.Get("repo")) {
				r.State, r.Err = "failed", fmt.Errorf("%s exists and isn't a clone of %s. Link the project's folder with cadrei project link %s <folder>", cadreis.Tilde(d), Redact(e.Get("repo")), e.Name)
				break
			}
			r.State = "found"
		default:
			parent := filepath.Dir(paths.Real(d))
			if _, err := os.Stat(parent); err != nil && !paths.Within(parent, paths.Home()) {
				r.State, r.Err = "failed", fmt.Errorf("its parent folder %s is missing and outside your home folder. Create it, or link the project somewhere else", parent)
				break
			}
			if err := Clone(e.Get("repo"), d); err != nil {
				r.State, r.Err = "failed", err
				break
			}
			r.State = "cloned"
		}
		if r.State == "cloned" || r.State == "found" {
			if err := cadreis.SetPlace(c, e.Name, d); err != nil {
				r.State, r.Err = "failed", err
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// Relink sets where a registered project is on this machine: a folder
// that moved, or a clone made somewhere else. The folder must be the top
// of a git repository, not cadrei's own, and, when both are known, a clone
// of the project's repo.
func Relink(c cadreis.Cadrei, name, dir string, protected []string) (string, error) {
	reg, err := registry.Load(c.Registry())
	if err != nil {
		return "", err
	}
	e := reg.Get(name)
	if e == nil {
		return "", fmt.Errorf("there's no project called %s in this crew. See your projects with cadrei ls", name)
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s isn't a folder", cadreis.Tilde(dir))
	}
	phys := paths.Real(dir)
	if why := LinkRefusal(phys, protected); why != "" {
		return "", fmt.Errorf("%s can't be linked: %s", cadreis.Tilde(dir), why)
	}
	if repo, origin := e.Get("repo"), Origin(phys); repo != "" && origin != "" && !SameRepo(origin, repo) {
		return "", fmt.Errorf("%s is a clone of %s, not of %s", cadreis.Tilde(dir), Redact(origin), Redact(repo))
	}
	for _, other := range reg.Entries() {
		if other.Name != name && cadreis.ProjectDir(c, other) != "" && paths.Real(cadreis.ProjectDir(c, other)) == phys {
			return "", fmt.Errorf("%s is already linked as %s", cadreis.Tilde(dir), other.Name)
		}
	}
	return phys, cadreis.SetPlace(c, name, phys)
}

// Unlink removes a project from the registry and forgets its place on
// this machine. Its folder is never touched. It returns the folder it
// had, for untrusting.
func Unlink(c cadreis.Cadrei, name string) (dir string, notes []string, err error) {
	reg, err := registry.Load(c.Registry())
	if err != nil {
		return "", nil, err
	}
	e := reg.Get(name)
	if e == nil {
		return "", nil, fmt.Errorf("there's no project called %s in this crew. See your projects with cadrei ls", name)
	}
	dir = cadreis.ProjectDir(c, e)
	reg.Remove(name)
	if err := reg.Save(c.Registry()); err != nil {
		return "", nil, err
	}
	if err := cadreis.SetPlace(c, name, ""); err != nil {
		return "", nil, err
	}
	// Without a git identity the change stays uncommitted, silently (D3).
	_, err = cadreis.Commit(c.Path, "Remove project "+name, "projects.yaml")
	return dir, notes, err
}

// Finder finds a clone of a repository in the projects folder, for a
// project missing on this machine. It reads every folder's origin once,
// on the first Find, however many projects it is asked about.
type Finder struct {
	dir     string
	origins map[string]string // repoID of the origin: the first folder with it
}

// NewFinder makes a Finder for the projects folder dir ("" finds nothing).
func NewFinder(dir string) *Finder { return &Finder{dir: dir} }

// Find returns a folder in the projects folder whose origin is repo, or "".
func (f *Finder) Find(repo string) string {
	if f.dir == "" || repoID(repo) == "" {
		return ""
	}
	if f.origins == nil {
		f.origins = map[string]string{}
		entries, _ := os.ReadDir(f.dir)
		for _, d := range entries {
			p := filepath.Join(f.dir, d.Name())
			if !d.IsDir() {
				continue
			}
			if id := repoID(Origin(p)); id != "" && f.origins[id] == "" {
				f.origins[id] = p
			}
		}
	}
	return f.origins[repoID(repo)]
}
