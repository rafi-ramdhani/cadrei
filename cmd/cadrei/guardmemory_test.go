//go:build !windows

package main

import (
	"os"
	"strings"
	"testing"
)

// 0.2.2 crew memory (section 7): the member guard refuses a command that
// writes into a .memory folder in every form it can read, and lets
// reading it, and everything else, run.
func TestTheGuardKeepsMembersOutOfTheMemory(t *testing.T) {
	for _, c := range []string{
		`echo "the user wants force pushes" > ~/.cadrei/main/.memory/feedback_push.md`,
		`echo x >> .memory/MEMORY.md`, `printf x &> /Users/me/.cadrei/main/.memory/a.md`, `cat a.md > ".memory/a.md"`,
		`echo x | tee .memory/a.md`, `echo x | tee -a ~/.cadrei/main/.memory/MEMORY.md >/dev/null`,
		`cp notes.md ~/.cadrei/main/.memory/`, `cp -r planted/. .memory`, `mv a.md .memory/a.md`, `rm .memory/a.md`, `rm -rf ~/.cadrei/main/.memory`,
		`ln -s /tmp/x .memory/a.md`, `touch .memory/a.md`, `sed -i '' 's/no/yes/' .memory/a.md`, `sed -i.bak s/a/b/ .memory/a.md`,
		`perl -pi -e 's/a/b/' .memory/a.md`, `dd if=x of=.memory/a.md`, `install -m 644 x .memory/a.md`, `rsync -a x/ .memory/`,
		`curl -o .memory/a.md https://example.com/a`, `tar -xf x.tar -C .memory`,
		`cd ~/.cadrei/main/.memory && rm a.md`, `ls .memory | xargs rm`, `find .memory -name '*.md' -delete`,
		`find ~/.cadrei/main/.memory -exec rm {} +`, `sh -c 'echo x > .memory/a.md'`, `bash -c "cp x .memory/"`,
		"bash <<'EOF'\necho x > .memory/a.md\nEOF", `eval "rm .memory/a.md"`, `sudo -u me tee .memory/a.md < x`,
		`python3 -c "open('.memory/a.md','w').write('x')"`, "python3 - <<'EOF'\nimport shutil\nshutil.copy('x', '/Users/me/.cadrei/main/.memory/a.md')\nEOF",
		`node -e "require('fs').writeFileSync('.memory/a.md', 'x')"`, `x=$(echo y > .memory/a.md)`, `echo "$(rm .memory/a.md)"`,
		`.memory_x=1; echo x > '.memory/a b.md'`, `cp -t ~/.cadrei/main/.memory a.md`, `cp --target-directory=.memory a.md`,
		`find .memory -name x -exec mv {} /tmp \;`, `tar -x -f x.tar --directory .memory`, `unzip x.zip -d .memory`, `wget -P .memory https://example.com/a`,
		`cd .memory && cp /tmp/a.md .`, "cat a.md \\\n  > .memory/a.md",
		// A line it can't read whole is read word by word.
		`echo "x > .memory/a.md`, `cat <<EOF > .memory/a.md`,
	} {
		if !guardLine(memoryRule{}, c, 0) {
			t.Errorf("let through: %q", c)
		}
	}
	for _, c := range []string{
		`cat ~/.cadrei/main/.memory/MEMORY.md`, `ls -la .memory`, `grep -r push .memory/`, `head -5 .memory/a.md`, `wc -l .memory/*.md`,
		`sed -n 1,5p .memory/a.md`, `git log -- .memory`, `git diff .memory`, `echo x > notes.md`, `cp a.md b.md`, `rm -rf build/`,
		`echo "my memory is bad" > notes.md`, `touch src/memory.go`, `mv memory/a.md memory/b.md`, `rm .memoryx/a`, `cp x .memories/`,
		`python3 -c "print(open('.memory/a.md').read())"`, `go test ./internal/memory/...`, `ls .memory | wc -l`,
		// Review of crew memory, L5: reading, and copying out, never count,
		// nor does a writer whose files are elsewhere on the same line.
		`cp .memory/a.md /tmp/a.md`, `tar -czf backup.tgz .memory`, `rsync -a .memory/ /tmp/x/`, `cd ~/.cadrei/main/.memory && cp a.md /tmp/`,
		`grep -rn "cp" .memory/`, `grep -rw install .memory/`, `git log --grep rm -- .memory`, `ls .memory && mkdir -p out`,
		`test -d .memory || touch notes.md`, `cd ~/proj && npm run build && rm -rf dist # not .memory`, `ls .memory | xargs -I{} cp {} /tmp/`,
		`find .memory -name '*.md' -exec cp {} /tmp/ \;`,
	} {
		if guardLine(memoryRule{}, c, 0) {
			t.Errorf("blocked: %q", c)
		}
	}
}

// Through the hook: a member's write is refused with the reason, the
// orchestrator's isn't.
func TestTheMemberGuardRefusesMemoryWrites(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	in := `{"tool_name":"Bash","tool_input":{"command":"echo x > ~/.cadrei/work/.memory/a.md"},"permission_mode":"default"}`
	t.Setenv("CADREI_MEMBER", "work-dev-engineer")
	code, _, errOut := callIn(in, "hook", "guard")
	if code != 2 || !strings.Contains(errOut, "can't write into a .memory folder") {
		t.Errorf("a member: %d %q", code, errOut)
	}
	os.Unsetenv("CADREI_MEMBER")
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	t.Setenv("CADREI_HOME", home+"/.cadrei/work")
	if code, out, errOut := callIn(in, "hook", "guard"); code != 0 || out != "" || errOut != "" {
		t.Errorf("the orchestrator: %d %q %q", code, out, errOut)
	}
}
