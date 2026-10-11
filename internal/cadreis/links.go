package cadreis

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/gitx"
)

// CommittedLinks lists the symbolic links committed in a cadrei repository
// where cadrei would follow them: in its member and team folders, in the
// runtime's folder (runtimeDir, relative, holding the build folder), and
// at its top level (playbook.md, projects.yaml and the like). Git checks
// out a committed link as a link, so a cloned cadrei could point cadrei at
// files outside it. Nothing in the repo's own git settings runs: it may
// be an archive the user hasn't agreed to bring back.
func CommittedLinks(dir, runtimeDir string) ([]string, error) {
	linkedDirs := []string{"members/", "teams/", runtimeDir + "/"}
	out, err := gitx.Reading(dir, "ls-files", "-s", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("could not list the files of %s: %v", dir, err)
	}
	var links []string
	for _, entry := range strings.Split(string(out), "\x00") {
		// <mode> <object> <stage>\t<path>
		meta, p, ok := strings.Cut(entry, "\t")
		if !ok || !strings.HasPrefix(meta, "120000 ") {
			continue
		}
		if !strings.Contains(p, "/") {
			links = append(links, p)
			continue
		}
		for _, d := range linkedDirs {
			if strings.HasPrefix(p, d) {
				links = append(links, p)
				break
			}
		}
	}
	return links, nil
}

// LinksIn lists the symbolic links on disk where cadrei would follow them
// in a cadrei folder, committed or not: at its top level, in members and
// teams and each team folder there, and in the runtime's folder (relative)
// and its folders. Links deeper in a team's work (node_modules/.bin and
// the like) are that work's own, as in any project.
func LinksIn(dir, runtimeDir string) []string {
	var out []string
	look := func(rel string, depth int) {
		var walk func(rel string, depth int)
		walk = func(rel string, depth int) {
			entries, err := os.ReadDir(filepath.Join(dir, rel))
			if err != nil {
				return
			}
			for _, e := range entries {
				p := filepath.Join(rel, e.Name())
				switch {
				case e.Type()&fs.ModeSymlink != 0:
					out = append(out, p)
				case e.IsDir() && depth > 0:
					walk(p, depth-1)
				}
			}
		}
		walk(rel, depth)
	}
	look(".", 0)
	look("members", 1)
	look("teams", 0)
	look(runtimeDir, 1)
	sort.Strings(out)
	return slices.Compact(out)
}
