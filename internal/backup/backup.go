// Package backup keeps credentials and bulky files out of a cadrei's
// backups: the pre-push hook cadrei installs in each cadrei repository, and
// the check it runs (cadrei hook pre-push).
package backup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// marker is the line that makes a pre-push hook cadrei's own.
const marker = "# cadrei: refuses to push credentials and large files (cadrei hook pre-push)"

// MaxSize is the largest file a push may carry (a variable for tests).
var MaxSize int64 = 50 << 20

// scanSize is the largest file whose content is searched for tokens.
const scanSize = 8 << 20

// hookFile is the cadrei repository's pre-push hook, as git finds it
// (core.hooksPath included).
func hookFile(repo string) (string, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--git-path", "hooks/pre-push").Output()
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository", repo)
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(repo, p)
	}
	return p, nil
}

// hookText is the hook that runs binary. When binary is gone, the push
// stops with a line that says why, rather than git's own error.
func hookText(binary string) string {
	q := "'" + strings.ReplaceAll(binary, "'", `'\''`) + "'"
	return "#!/bin/sh\n" + marker + "\n" +
		"if [ -x " + q + " ]; then exec " + q + " hook pre-push \"$@\"; fi\n" +
		"echo \"cadrei is missing, so this check can't run. Reinstall cadrei, or remove .git/hooks/pre-push.\" >&2\n" +
		"exit 1\n"
}

// ErrForeign is returned when the repository has a pre-push hook that is
// not cadrei's: cadrei leaves it alone.
var ErrForeign = errors.New("has a pre-push hook that is not cadrei's")

// Install makes cadrei's pre-push hook run binary in the cadrei repository
// repo. It reports whether it wrote anything. A hook that is not cadrei's is
// left as it is, with ErrForeign.
func Install(repo, binary string) (bool, error) {
	file, err := hookFile(repo)
	if err != nil {
		return false, err
	}
	want := hookText(binary)
	raw, err := os.ReadFile(file)
	switch {
	case err == nil && string(raw) == want:
		return false, nil
	case err == nil && !bytes.Contains(raw, []byte(marker)):
		return false, fmt.Errorf("%s %w", repo, ErrForeign)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return false, err
	}
	tmp := file + ".cadrei-new"
	if err := os.WriteFile(tmp, []byte(want), 0o755); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, file)
}

// A Finding is a file a push would carry that it must not.
type Finding struct {
	Path string
	Why  string
}

// Local is a cadrei's local folder, relative to the cadrei (the program
// sets it): this machine's own notes, which git ignores. A push that
// carries a file from it is refused.
var Local string

// MachineOnly is the Why of a file from the local folder.
const MachineOnly = "this machine's own file"

var (
	// Files that hold credentials by their name.
	secretNames = map[string]string{
		".credentials.json": "a credentials file",
		"credentials":       "a credentials file",
		".git-credentials":  "a credentials file",
		".netrc":            "a credentials file",
		".npmrc":            "may hold a registry token",
		".pypirc":           "may hold a registry token",
		"id_rsa":            "a private key",
		"id_ecdsa":          "a private key",
		"id_ed25519":        "a private key",
		"id_dsa":            "a private key",
	}
	secretExts = map[string]string{".pem": "a key or certificate", ".key": "a private key",
		".p12": "a key store", ".pfx": "a key store"}
	// Token patterns in a file's content.
	tokens = []struct {
		re   *regexp.Regexp
		what string
	}{
		{regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{16,}`), "an Anthropic API key"},
		{regexp.MustCompile(`ghp_[A-Za-z0-9]{30,}`), "a GitHub token"},
		{regexp.MustCompile(`github_pat_[A-Za-z0-9_]{30,}`), "a GitHub token"},
		{regexp.MustCompile(`gh[ousr]_[A-Za-z0-9]{30,}`), "a GitHub token"},
		{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), "a private key"},
		{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "an AWS access key"},
		{regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`), "a Slack token"},
	}
	// A URL's user part: what's between "://" and the "@" before its host.
	urlUser = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://([^\s/@'"]+)@[^\s/'"<>@]+`)
	// A placeholder for a value set at run time: <token>, ${TOKEN},
	// $TOKEN, {{token}}, or a mask such as *** or xxx.
	placeholder = regexp.MustCompile(`^(<[^<>]*>|\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*|\{\{[^{}]*\}\}|\*+|x+|X+)$`)
	// A user that is a token: long, and of letters and digits both.
	tokenUser = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
)

// urlSecret reports whether data holds a URL with a password, or with a
// token as its user. A user or password that is a placeholder whole isn't
// one; one with a placeholder and more is.
func urlSecret(data []byte) bool {
	for _, m := range urlUser.FindAllSubmatch(data, -1) {
		// A token can be the user with an empty or placeholder password
		// too (https://<token>:x-oauth-basic@, https://<token>:@).
		user, pass, hasPass := strings.Cut(string(m[1]), ":")
		if hasPass && pass != "" && !placeholder.MatchString(pass) {
			return true
		}
		if !placeholder.MatchString(user) && tokenUser.MatchString(user) &&
			strings.ContainsAny(user, "0123456789") && strings.IndexFunc(user, unicode.IsLetter) >= 0 {
			return true
		}
	}
	return false
}

// nameWhy says why a file's name marks it as a credential, or "".
func nameWhy(p string) string {
	base := path.Base(p)
	switch {
	case strings.HasPrefix(p, ".aws/") || strings.Contains(p, "/.aws/"):
		return "is in an .aws folder, which holds AWS credentials"
	case p == ".kube/config" || strings.HasSuffix(p, "/.kube/config"):
		return "looks like a Kubernetes config with credentials"
	case p == ".docker/config.json" || strings.HasSuffix(p, "/.docker/config.json"):
		return "looks like a Docker config with registry credentials"
	}
	if why, ok := secretNames[base]; ok {
		return "looks like " + why
	}
	if base == ".env" || (strings.HasPrefix(base, ".env.") && !envExample(base)) {
		return "looks like an environment file with secrets"
	}
	if why, ok := secretExts[strings.ToLower(path.Ext(base))]; ok {
		return "looks like " + why
	}
	return ""
}

// envExample reports whether a .env.* file is a template meant to be shared.
func envExample(base string) bool {
	switch strings.TrimPrefix(base, ".env.") {
	case "example", "sample", "template", "dist":
		return true
	}
	return false
}

// contentWhy says which token a file's content holds, or "".
func contentWhy(data []byte) string {
	for _, t := range tokens {
		if t.re.Match(data) {
			return "holds what looks like " + t.what
		}
	}
	// A user and password in a URL (a project's repo, a clone's origin).
	if urlSecret(data) {
		return "holds what looks like a password in a URL"
	}
	return ""
}

// Update is one ref a push updates, as git gives it on the hook's stdin.
type Update struct{ LocalRef, LocalSHA, RemoteRef, RemoteSHA string }

// ReadUpdates parses the pre-push hook's stdin.
func ReadUpdates(r io.Reader) []Update {
	var out []Update
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 4 {
			out = append(out, Update{f[0], f[1], f[2], f[3]})
		}
	}
	return out
}

func deleted(sha string) bool { return strings.Trim(sha, "0") == "" }

// remoteRefs is the --not argument that leaves out what the remote already
// has: its remote-tracking refs. A push to a URL, or a name git would read
// as a pattern, excludes nothing, so everything is checked.
func remoteRefs(remote string) string {
	if remote == "" || strings.ContainsAny(remote, "*?[\\:/") {
		return ""
	}
	return "--remotes=" + remote
}

type change struct{ blob, path string }

// rawChanges reads git log --raw -z output: for each changed path, the new
// blob (none for a deletion or a submodule).
func rawChanges(out []byte) []change {
	var cs []change
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := strings.TrimLeft(fields[i], "\n")
		if !strings.HasPrefix(f, ":") {
			continue
		}
		// :<old mode> <new mode> <old sha> <new sha> <status>, then the path
		meta := strings.Fields(f[1:])
		if i+1 >= len(fields) || len(meta) < 5 {
			break
		}
		i++
		if meta[1] == "160000" || deleted(meta[3]) {
			continue
		}
		cs = append(cs, change{meta[3], fields[i]})
	}
	return cs
}

// Scan checks every file the push carries that the remote it goes to
// (remote, the name git passes the hook) does not have yet: in every
// commit pushed, not only the last, since history goes with it. Every path
// a file has in those commits is checked by name, and every file's size
// and content once.
func Scan(repo, remote string, updates []Update) ([]Finding, error) {
	paths := map[string][]string{} // blob -> paths
	seen := map[string]bool{}      // blob and path
	for _, u := range updates {
		if deleted(u.LocalSHA) {
			continue
		}
		// Each pushed commit against each of its parents (-m), a first
		// commit against nothing (--root), with no renames, so a file
		// that came in under one name and moved on is seen under both.
		args := []string{"-C", repo, "log", "--format=", "--raw", "--no-renames", "--no-abbrev", "-m", "--root", "-z", u.LocalSHA}
		if exclude := remoteRefs(remote); exclude != "" {
			args = append(args, "--not", exclude)
		}
		out, err := exec.Command("git", args...).Output()
		if err != nil {
			return nil, fmt.Errorf("could not list what the push carries: %v", err)
		}
		for _, c := range rawChanges(out) {
			if !seen[c.blob+"\x00"+c.path] {
				seen[c.blob+"\x00"+c.path] = true
				paths[c.blob] = append(paths[c.blob], c.path)
			}
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	var local []Finding
	if Local != "" {
		for _, list := range paths {
			for _, p := range list {
				if strings.HasPrefix(p, Local+"/") {
					local = append(local, Finding{Path: p, Why: MachineOnly})
				}
			}
		}
		sort.Slice(local, func(a, b int) bool { return local[a].Path < local[b].Path })
	}
	shas := make([]string, 0, len(paths))
	for sha := range paths {
		shas = append(shas, sha)
	}
	sort.Strings(shas)
	var findings []Finding
	// One git process gives each object's type, size and, when small
	// enough, its content.
	cmd := exec.Command("git", "-C", repo, "cat-file", "--batch")
	in, _ := cmd.StdinPipe()
	outPipe, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		w := bufio.NewWriter(in)
		for _, sha := range shas {
			fmt.Fprintln(w, sha)
		}
		w.Flush()
		in.Close()
	}()
	r := bufio.NewReader(outPipe)
	for _, sha := range shas {
		header, err := r.ReadString('\n')
		if err != nil {
			cmd.Wait()
			return nil, fmt.Errorf("could not read the pushed files: %v", err)
		}
		f := strings.Fields(header)
		if len(f) != 3 {
			continue // missing object
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		var data []byte
		if f[1] == "blob" && size <= scanSize {
			data = make([]byte, size)
			if _, err := io.ReadFull(r, data); err != nil {
				cmd.Wait()
				return nil, err
			}
		} else if _, err := io.CopyN(io.Discard, r, size); err != nil {
			cmd.Wait()
			return nil, err
		}
		r.ReadByte() // the newline after the content
		if f[1] != "blob" {
			continue
		}
		why := ""
		switch {
		case size > MaxSize:
			why = fmt.Sprintf("is %d MB, over the %d MB limit", size>>20, MaxSize>>20)
		case data != nil:
			why = contentWhy(data)
		}
		for _, p := range paths[sha] {
			if w := nameWhy(p); w != "" {
				findings = append(findings, Finding{p, w})
			} else if why != "" {
				findings = append(findings, Finding{p, why})
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("could not read the pushed files: %v", err)
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	return append(local, findings...), nil
}

// Remove takes out cadrei's pre-push hook from a cadrei repository: only a
// hook cadrei wrote (its marker line), never one of the user's. It reports
// whether it removed one.
func Remove(repo string) (bool, error) {
	file, err := hookFile(repo)
	if err != nil {
		return false, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil || !bytes.Contains(raw, []byte(marker)) {
		return false, nil
	}
	// Only the file exactly as cadrei writes it goes: lines the user added
	// to it are theirs.
	if !ours(string(raw)) {
		return false, ErrChanged
	}
	return true, os.Remove(file)
}

// ErrChanged is returned when cadrei's pre-push hook holds lines cadrei did
// not write: cadrei leaves the file as it is.
var ErrChanged = errors.New("cadrei's pre-push hook holds lines of your own, so it was left as it is; remove cadrei's lines from it yourself")

// ours reports whether text is exactly the hook cadrei writes, for the
// binary it names.
func ours(text string) bool {
	_, ok := hookBinary(text)
	return ok
}

// hookBinary is the program a hook in cadrei's exact form runs, and
// whether the hook is in that form.
func hookBinary(text string) (string, bool) {
	const lead = "if [ -x '"
	i := strings.Index(text, lead)
	if i < 0 {
		return "", false
	}
	rest := text[i+len(lead):]
	j := strings.Index(rest, "' ]; then exec ")
	if j < 0 {
		return "", false
	}
	binary := strings.ReplaceAll(rest[:j], `'\''`, "'")
	return binary, text == hookText(binary)
}

// OwnHook reports whether a pre-push hook file is exactly one cadrei
// writes, for binary: the form alone says nothing about the program it
// runs, which a hook in a copied repo can name as it likes.
func OwnHook(file, binary string) bool {
	raw, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	b, ok := hookBinary(string(raw))
	return ok && b == binary
}

// Installed reports whether a cadrei repository has cadrei's pre-push hook.
func Installed(repo string) bool {
	file, err := hookFile(repo)
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(file)
	return err == nil && bytes.Contains(raw, []byte(marker))
}
