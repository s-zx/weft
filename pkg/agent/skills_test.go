// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantDesc string
	}{
		{
			input:    "---\nname: add-config\ndescription: Guide for adding config\n---\n# Content",
			wantName: "add-config",
			wantDesc: "Guide for adding config",
		},
		{
			input:    "---\nname: simple\n---\n# No description",
			wantName: "simple",
			wantDesc: "",
		},
		{
			input:    "# No frontmatter",
			wantName: "",
			wantDesc: "",
		},
		{
			input:    "---\nunknown: field\n---\n# Unknown fields only",
			wantName: "",
			wantDesc: "",
		},
	}
	for _, tt := range tests {
		name, desc := parseFrontmatter(tt.input)
		if name != tt.wantName || desc != tt.wantDesc {
			t.Errorf("parseFrontmatter(%q) = (%q, %q), want (%q, %q)",
				tt.input[:min(len(tt.input), 30)], name, desc, tt.wantName, tt.wantDesc)
		}
	}
}

func TestDiscoverSkills(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, ".kilocode", "skills", "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: test-skill\ndescription: A test skill\n---\n# Test\nSome content."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	skills := DiscoverSkills(tmp)
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "test-skill" {
		t.Errorf("Name = %q, want %q", skills[0].Name, "test-skill")
	}
	if skills[0].Description != "A test skill" {
		t.Errorf("Description = %q", skills[0].Description)
	}
	if !strings.Contains(skills[0].Path, "test-skill/SKILL.md") {
		t.Errorf("Path = %q", skills[0].Path)
	}
}

func TestDiscoverSkills_EmptyCwd(t *testing.T) {
	skills := DiscoverSkills("")
	if skills != nil {
		t.Errorf("expected nil for empty cwd, got %v", skills)
	}
}

func TestDiscoverSkills_NoDir(t *testing.T) {
	skills := DiscoverSkills("/nonexistent/path")
	if skills != nil {
		t.Errorf("expected nil for missing dir, got %v", skills)
	}
}

func TestDiscoverSkills_FallbackName(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, ".kilocode", "skills", "my-dir")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: no name field\n---\n# Content"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	skills := DiscoverSkills(tmp)
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "my-dir" {
		t.Errorf("Name should fall back to dir name, got %q", skills[0].Name)
	}
}

func TestBuildSkillsContext(t *testing.T) {
	skills := []SkillInfo{
		{Name: "add-config", Description: "Guide for config", Path: ".kilocode/skills/add-config/SKILL.md"},
		{Name: "add-rpc", Description: "Guide for RPC", Path: ".kilocode/skills/add-rpc/SKILL.md"},
	}
	ctx := BuildSkillsContext(skills)
	if !strings.Contains(ctx, "<available_skills>") {
		t.Error("missing opening tag")
	}
	if !strings.Contains(ctx, "add-config") {
		t.Error("missing add-config skill")
	}
	if !strings.Contains(ctx, "add-rpc") {
		t.Error("missing add-rpc skill")
	}
}

func TestBuildSkillsContext_Empty(t *testing.T) {
	if ctx := BuildSkillsContext(nil); ctx != "" {
		t.Errorf("expected empty string for nil skills, got %q", ctx)
	}
}
