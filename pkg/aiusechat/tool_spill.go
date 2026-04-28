// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package aiusechat

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/wavebase"
)

// spillToolResultIfOversized spills a large tool result to disk and replaces
// the inline text with a head + tail preview. Without this, a single
// `read_text_file` of a 1MB log or a chatty `shell_exec` poisons every
// subsequent step's input tokens. The on-disk path is referenced in the
// preview so the model can re-read targeted slices via existing tools.
//
// Errors during spill are logged but non-fatal: we keep the original (large)
// text so the model still gets the answer, just at a context cost.
func spillToolResultIfOversized(result *uctypes.AIToolResult, toolDef *uctypes.ToolDefinition, chatId string) {
	if result == nil || result.Text == "" {
		return
	}
	cap := uctypes.DefaultMaxToolResultSizeChars
	if toolDef != nil && toolDef.MaxResultSizeChars > 0 {
		cap = toolDef.MaxResultSizeChars
	}
	if len(result.Text) <= cap {
		return
	}

	dir := filepath.Join(wavebase.GetWaveDataDir(), "agent-tool-overflow", sanitizeFileSegment(chatId))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("tool spill: mkdir %s: %v\n", dir, err)
		return
	}
	id := result.ToolUseID
	if id == "" {
		id = "noid"
	}
	path := filepath.Join(dir, sanitizeFileSegment(id)+".txt")
	if err := os.WriteFile(path, []byte(result.Text), 0o600); err != nil {
		log.Printf("tool spill: write %s: %v\n", path, err)
		return
	}

	headSize := cap / 2
	tailSize := cap - headSize - 200 // leave slack for the marker
	if tailSize < 0 {
		tailSize = 0
	}
	if headSize > len(result.Text) {
		headSize = len(result.Text)
	}
	tailStart := len(result.Text) - tailSize
	if tailStart < headSize {
		tailStart = headSize
	}
	head := result.Text[:headSize]
	tail := result.Text[tailStart:]
	omitted := tailStart - headSize

	preview := head +
		fmt.Sprintf("\n\n[... %d chars truncated. Full result saved to %s — read it directly with read_text_file or grep with the search tool if you need more context. ...]\n\n", omitted, path) +
		tail
	log.Printf("tool spill: %s -> %s (%d chars, kept %d head + %d tail)\n", result.ToolName, path, len(result.Text), headSize, tailSize)
	result.Text = preview
}

// sanitizeFileSegment makes a string safe to use as a path segment without
// being clever — anything outside [A-Za-z0-9._-] becomes '_'. We don't need
// reversibility, just collision-resistance for chat ids and tool-use ids
// which are already UUIDs/prefix+UUIDs.
func sanitizeFileSegment(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			out[i] = c
		default:
			out[i] = '_'
		}
	}
	if len(out) == 0 {
		return "_"
	}
	return string(out)
}
