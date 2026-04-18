// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

package cmdblock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"sync"
)

// Tracker glues the OSC 16162 parser to the cmdblock store for one shell
// session.  Hook it into a PTY read loop by calling OnBytes each time a chunk
// is appended to the parent blockfile: Tracker translates A/C/D/M events into
// MakePromptStarted / MarkCommandSubmitted / MarkCommandDone calls.
type Tracker struct {
	mu         sync.Mutex
	blockID    string
	parser     *Parser
	currentOID string
	shellType  string
}

func MakeTracker(blockID string) *Tracker {
	return &Tracker{
		blockID: blockID,
		parser:  MakeParser(),
	}
}

// OnBytes must be called with every PTY chunk in the order it lands in the
// parent blockfile, AFTER the chunk has been appended.  The parser maintains
// absolute offsets relative to the first byte fed; those offsets are recorded
// verbatim into the cmdblock rows.
func (t *Tracker) OnBytes(ctx context.Context, chunk []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, ev := range t.parser.Feed(chunk) {
		t.handleEvent(ctx, ev)
	}
}

func (t *Tracker) handleEvent(ctx context.Context, ev Event) {
	switch ev.Command {
	case "A":
		cb, err := MakePromptStarted(ctx, t.blockID, ev.Offset, t.shellType)
		if err != nil {
			log.Printf("cmdblock: MakePromptStarted blockid=%s: %v", t.blockID, err)
			return
		}
		t.currentOID = cb.OID
	case "C":
		if t.currentOID == "" {
			return
		}
		cmd := decodeCmd64(ev.Payload)
		outputStart := ev.Offset + int64(ev.SeqLen)
		if err := MarkCommandSubmitted(ctx, t.currentOID, cmd, "", ev.Offset, outputStart, ""); err != nil {
			log.Printf("cmdblock: MarkCommandSubmitted oid=%s: %v", t.currentOID, err)
		}
	case "D":
		if t.currentOID == "" {
			return
		}
		exit := decodeExitCode(ev.Payload)
		if err := MarkCommandDone(ctx, t.currentOID, exit, ev.Offset); err != nil {
			log.Printf("cmdblock: MarkCommandDone oid=%s: %v", t.currentOID, err)
		}
		t.currentOID = ""
	case "M":
		if shell := decodeShell(ev.Payload); shell != "" {
			t.shellType = shell
		}
	}
}

func decodeCmd64(payload string) string {
	if payload == "" {
		return ""
	}
	var data struct {
		Cmd64 string `json:"cmd64"`
	}
	if err := json.Unmarshal([]byte(payload), &data); err != nil || data.Cmd64 == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(data.Cmd64)
	if err != nil {
		return ""
	}
	return string(decoded)
}

func decodeExitCode(payload string) int64 {
	if payload == "" {
		return -1
	}
	var data struct {
		ExitCode *int64 `json:"exitcode"`
	}
	if err := json.Unmarshal([]byte(payload), &data); err != nil || data.ExitCode == nil {
		return -1
	}
	return *data.ExitCode
}

func decodeShell(payload string) string {
	if payload == "" {
		return ""
	}
	var data struct {
		Shell string `json:"shell"`
	}
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return ""
	}
	return data.Shell
}
