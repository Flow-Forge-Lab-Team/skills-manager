package bundledskills

import "embed"

//go:embed skills-diff-summary/SKILL.md skills-ingest/SKILL.md
var files embed.FS

func SkillMarkdown(name string) string {
	data, err := files.ReadFile(name + "/SKILL.md")
	if err != nil {
		return ""
	}
	return string(data)
}
