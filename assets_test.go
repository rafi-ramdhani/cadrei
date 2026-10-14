package cadrei

import (
	"io/fs"
	"testing"
)

func TestAssetsHoldSkillProtocolAndTemplate(t *testing.T) {
	for _, name := range []string{
		"skills/cadrei/SKILL.md", "protocol.md", "orchestrator.md",
		"template/playbook.md", "template/projects.yaml", "template/cadrei.conf",
		"template/.gitignore", "template/.gitattributes", "template/teams/.gitkeep", "template/members/dev/engineer.md",
	} {
		if _, err := fs.Stat(Assets, name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
}
