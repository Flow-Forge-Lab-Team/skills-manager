package cli

import bundledskills "github.com/Flow-Forge-Lab-Team/skills-manager/bundled-skills"

func readBundledSkillMarkdown(name string) string {
	return bundledskills.SkillMarkdown(name)
}
