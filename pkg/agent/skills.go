// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"os"
	"path/filepath"
	"strings"
)

const SkillsDir = ".kilocode/skills"
const SkillFile = "SKILL.md"

type SkillInfo struct {
	Name        string
	Description string
	Path        string
}

func DiscoverSkills(cwd string) []SkillInfo {
	if cwd == "" {
		return nil
	}
	dir := filepath.Join(cwd, SkillsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var skills []SkillInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, e.Name(), SkillFile)
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		name, desc := parseFrontmatter(string(data))
		if name == "" {
			name = e.Name()
		}
		skills = append(skills, SkillInfo{
			Name:        name,
			Description: desc,
			Path:        filepath.Join(SkillsDir, e.Name(), SkillFile),
		})
	}
	return skills
}

func BuildSkillsContext(skills []SkillInfo) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_skills>\n")
	b.WriteString("The workspace has skill guides in .kilocode/skills/. ")
	b.WriteString("When the user's task matches a skill, read the full SKILL.md for step-by-step instructions.\n\n")
	for _, s := range skills {
		b.WriteString("- **")
		b.WriteString(s.Name)
		b.WriteString("** (`")
		b.WriteString(s.Path)
		b.WriteString("`)")
		if s.Description != "" {
			b.WriteString(" — ")
			b.WriteString(s.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

func parseFrontmatter(content string) (name string, description string) {
	if !strings.HasPrefix(content, "---") {
		return "", ""
	}
	end := strings.Index(content[3:], "---")
	if end < 0 {
		return "", ""
	}
	fm := content[3 : 3+end]
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		} else if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	return name, description
}
