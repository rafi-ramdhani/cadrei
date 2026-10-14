package memory

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/gitx"
)

// UnionLine is the .gitattributes line that merges two machines' indexes
// by keeping both sides' lines.
const UnionLine = "/" + Folder + "/" + Index + " merge=union"

// Attributes adds UnionLine to the crew's .gitattributes when it isn't
// there. It reports whether it added it. The line is committed with the
// memory (Commit).
func Attributes(crew string) (bool, error) {
	file := filepath.Join(crew, ".gitattributes")
	raw, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == UnionLine {
			return false, nil
		}
	}
	text := string(raw)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "# The orchestrator's memory index: two machines' lines are both kept.\n" + UnionLine + "\n"
	return true, fsx.WriteFile(file, []byte(text), 0o644)
}

// Busy says why git can't take a commit in the crew now, or "": another
// git command holds its index, or a rebase, merge or cherry-pick is under
// way.
func Busy(crew string) string {
	dir := gitDir(crew)
	if dir == "" {
		return "no git repository"
	}
	for _, f := range []string{"index.lock", "rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		if _, err := os.Lstat(filepath.Join(dir, f)); err == nil {
			return f
		}
	}
	return ""
}

// gitDir is the crew's own git folder, or "".
func gitDir(crew string) string {
	dir := filepath.Join(crew, ".git")
	if st, err := os.Lstat(dir); err == nil && st.IsDir() {
		return dir
	}
	return ""
}

// git runs git in the crew without the crew's own hooks or fsmonitor.
func git(crew string, args ...string) (string, error) {
	out, err := gitx.Reading(crew, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Commit commits the changes to the crew's memory folder and its
// .gitattributes as one commit with msg, and nothing else. It does
// nothing while git is busy (Busy), without a git identity, or with
// nothing to commit, and never fails: it reports whether it committed.
func Commit(crew, msg string) bool { return commit(crew, msg, Folder, ".gitattributes") }

// CommitRemoval commits the removal of the crew's memory folder with msg,
// and nothing else, as Commit does.
func CommitRemoval(crew, msg string) bool {
	if _, err := os.Lstat(Dir(crew)); err == nil || !tracked(crew, Folder) {
		return false
	}
	return commit(crew, msg, Folder)
}

func commit(crew, msg string, paths ...string) bool {
	if Busy(crew) != "" {
		return false
	}
	if name, err := git(crew, "config", "user.email"); err != nil || name == "" {
		return false
	}
	var spec []string
	for _, p := range paths {
		if _, err := os.Lstat(filepath.Join(crew, p)); err == nil || tracked(crew, p) {
			spec = append(spec, p)
		}
	}
	if len(spec) == 0 {
		return false
	}
	if _, err := git(crew, append([]string{"add", "-A", "--"}, spec...)...); err != nil {
		return false
	}
	if _, err := git(crew, append([]string{"diff", "--cached", "--quiet", "--"}, spec...)...); err == nil {
		return false // nothing to commit
	}
	_, err := git(crew, append([]string{"commit", "-q", "-m", msg, "--only", "--"}, spec...)...)
	return err == nil
}

// tracked reports whether git tracks anything at p in the crew.
func tracked(crew, p string) bool {
	out, err := git(crew, "ls-files", "--", p)
	return err == nil && out != ""
}

// Unpushed is how many commits the crew's branch has that its upstream
// lacks, as git last saw it (no network): -1 when it has no remote or no
// upstream.
func Unpushed(crew string) int {
	if out, err := git(crew, "remote"); err != nil || out == "" {
		return -1
	}
	out, err := git(crew, "rev-list", "--count", "@{upstream}..HEAD")
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return -1
	}
	return n
}

// HasRemote reports whether the crew has a git remote: a backup.
func HasRemote(crew string) bool {
	out, err := git(crew, "remote")
	return err == nil && out != ""
}
