// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import "testing"

func TestLookupMode_ValidModes(t *testing.T) {
	for _, name := range []string{ModeAsk, ModePlan, ModeDo, ModeBench} {
		m, ok := LookupMode(name)
		if !ok {
			t.Fatalf("LookupMode(%q) returned ok=false", name)
		}
		if m.Name != name {
			t.Fatalf("LookupMode(%q).Name = %q", name, m.Name)
		}
	}
}

func TestLookupMode_EmptyDefaultsDo(t *testing.T) {
	m, ok := LookupMode("")
	if !ok {
		t.Fatal("LookupMode(\"\") returned ok=false")
	}
	if m.Name != ModeDo {
		t.Fatalf("expected do, got %q", m.Name)
	}
}

func TestLookupMode_Unknown(t *testing.T) {
	_, ok := LookupMode("invalid")
	if ok {
		t.Fatal("LookupMode(\"invalid\") should return ok=false")
	}
}

func TestModeToolNames(t *testing.T) {
	askMode, _ := LookupMode(ModeAsk)
	if len(askMode.ToolNames) != 6 {
		t.Fatalf("ask mode should have 6 tools, got %d", len(askMode.ToolNames))
	}

	planMode, _ := LookupMode(ModePlan)
	if len(planMode.ToolNames) != 7 {
		t.Fatalf("plan mode should have 7 tools, got %d", len(planMode.ToolNames))
	}

	doMode, _ := LookupMode(ModeDo)
	if len(doMode.ToolNames) != 19 {
		t.Fatalf("do mode should have 19 tools, got %d", len(doMode.ToolNames))
	}

	benchMode, _ := LookupMode(ModeBench)
	if len(benchMode.ToolNames) != 11 {
		t.Fatalf("bench mode should have 11 tools, got %d", len(benchMode.ToolNames))
	}
}

func TestModeAllowMutation(t *testing.T) {
	askMode, _ := LookupMode(ModeAsk)
	if askMode.AllowMutation {
		t.Fatal("ask mode should not allow mutation")
	}
	planMode, _ := LookupMode(ModePlan)
	if planMode.AllowMutation {
		t.Fatal("plan mode should not allow mutation")
	}
	doMode, _ := LookupMode(ModeDo)
	if !doMode.AllowMutation {
		t.Fatal("do mode should allow mutation")
	}
	benchMode, _ := LookupMode(ModeBench)
	if !benchMode.AllowMutation {
		t.Fatal("bench mode should allow mutation")
	}
}

func TestModeBenchHasLargerStepBudget(t *testing.T) {
	benchMode, _ := LookupMode(ModeBench)
	if benchMode.StepBudget <= DefaultStepBudget {
		t.Fatalf("bench step budget %d should exceed default %d", benchMode.StepBudget, DefaultStepBudget)
	}
}
