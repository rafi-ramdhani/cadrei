package project

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ghWait is how long BackupRepos gives gh for each call: a slow network
// mustn't hold up a first run.
var ghWait = 10 * time.Second

// errNoGh is BackupRepos' error when gh isn't there or isn't signed in.
var errNoGh = errors.New("gh isn't signed in")

// repoName is owner/name as GitHub spells them: nothing a terminal would
// read as more than text.
var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// BackupRepos lists the crew backups of the user gh is signed in as: their
// repos named cadrei-<something>, as owner/name, the one pushed to last
// first. It fails when gh isn't there, isn't signed in, or can't list
// them in time.
func BackupRepos() ([]string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, errNoGh
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghWait)
	defer cancel()
	if exec.CommandContext(ctx, "gh", "auth", "status").Run() != nil {
		return nil, errNoGh
	}
	ctx, cancel2 := context.WithTimeout(context.Background(), ghWait)
	defer cancel2()
	out, err := exec.CommandContext(ctx, "gh", "repo", "list", "--limit", "1000", "--json", "name,nameWithOwner,pushedAt").Output()
	if err != nil {
		return nil, err
	}
	var repos []struct {
		Name          string    `json:"name"`
		NameWithOwner string    `json:"nameWithOwner"`
		PushedAt      time.Time `json:"pushedAt"`
	}
	if err := json.Unmarshal(out, &repos); err != nil {
		return nil, err
	}
	sort.SliceStable(repos, func(a, b int) bool { return repos[a].PushedAt.After(repos[b].PushedAt) })
	var list []string
	for _, r := range repos {
		if name := strings.ToLower(r.Name); strings.HasPrefix(name, "cadrei-") && name != "cadrei-" && repoName.MatchString(r.NameWithOwner) {
			list = append(list, r.NameWithOwner)
		}
	}
	return list, nil
}
