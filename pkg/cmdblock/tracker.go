// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

package cmdblock

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/url"
	"strings"
	"sync"

	"github.com/wavetermdev/waveterm/pkg/cmdblock/cbtypes"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wcore"
	"github.com/wavetermdev/waveterm/pkg/wps"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

var altScreenEnterSeq = []byte("\x1b[?1049h")
var altScreenExitSeq = []byte("\x1b[?1049l")
var osc7Prefix = []byte("\x1b]7;")
var clearScreenSeq = []byte("\x1b[2J")
var clearScrollbackSeq = []byte("\x1b[3J")

// Tracker glues the OSC 16162 parser to the cmdblock store for one shell
// session.  Hook it into a PTY read loop by calling OnBytes each time a chunk
// is appended to the parent blockfile: Tracker translates A/C/D events into
// MakePromptStarted / MarkCommandSubmitted / MarkCommandDone calls and
// publishes cmdblock:row + cmdblock:chunk events for live updates.
type Tracker struct {
	mu         sync.Mutex
	blockID    string
	parser     *Parser
	currentOID string
	state      string // matches the current row's state ("prompt"/"running"/"")
	shellType  string
	altScreen  bool
	lastCwd    string
}

func MakeTracker(blockID string) *Tracker {
	if err := MarkRunningAsInterrupted(context.Background(), blockID); err != nil {
		log.Printf("cmdblock: MarkRunningAsInterrupted blockid=%s: %v", blockID, err)
	}
	return &Tracker{
		blockID: blockID,
		parser:  MakeParser(),
	}
}

// OnBytes must be called with every PTY chunk in the order it lands in the
// parent blockfile, AFTER the chunk has been appended. The parser maintains
// absolute offsets relative to the first byte fed; those offsets are recorded
// verbatim into the cmdblock rows and also used as the "offset" for chunk
// events so the frontend can line them up against the blockfile.
func (t *Tracker) OnBytes(ctx context.Context, chunk []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	chunkStart := t.parser.Offset()
	events := t.parser.Feed(chunk)
	for _, ev := range events {
		t.handleEvent(ctx, ev)
	}
	if t.state == StateRunning && t.currentOID != "" && len(chunk) > 0 {
		t.publishChunk(t.currentOID, chunkStart, chunk)
	}
	t.detectAltScreen(chunk)
	t.detectOsc7(ctx, chunk)
	t.detectClear(chunk)
}

// detectClear watches for CSI 2J or 3J in the PTY stream and emits a
// cmdblock:clear event so the frontend can hide every block above the
// current one, matching `clear` behaviour in a traditional terminal.
func (t *Tracker) detectClear(chunk []byte) {
	if !bytes.Contains(chunk, clearScreenSeq) && !bytes.Contains(chunk, clearScrollbackSeq) {
		return
	}
	wps.Broker.Publish(wps.WaveEvent{
		Event:  wps.Event_CmdBlockClear,
		Scopes: []string{"block:" + t.blockID},
		Data: &cbtypes.CmdBlockClearEvent{
			BlockID:    t.blockID,
			ThroughOID: t.currentOID,
		},
	})
}

// detectOsc7 scans chunk for the "ESC ] 7 ; file://host/path ST" cwd update
// sequence most shells emit from their precmd/chpwd hooks. When the URL
// decodes to a local path, pushes it onto the parent block's meta.cmd:cwd
// so the frontend status bar / meta line has an accurate cwd even without
// an xterm-level OSC 7 handler.
func (t *Tracker) detectOsc7(ctx context.Context, chunk []byte) {
	for i := 0; i < len(chunk); {
		idx := bytes.Index(chunk[i:], osc7Prefix)
		if idx < 0 {
			return
		}
		start := i + idx + len(osc7Prefix)
		end := -1
		endLen := 0
		for j := start; j < len(chunk); j++ {
			if chunk[j] == 0x07 {
				end = j
				endLen = 1
				break
			}
			if chunk[j] == 0x1b && j+1 < len(chunk) && chunk[j+1] == '\\' {
				end = j
				endLen = 2
				break
			}
		}
		if end < 0 {
			return
		}
		payload := string(chunk[start:end])
		i = end + endLen
		if path := parseOsc7Path(payload); path != "" && path != t.lastCwd {
			t.lastCwd = path
			t.pushCwd(ctx, path)
		}
	}
}

func parseOsc7Path(raw string) string {
	if !strings.HasPrefix(raw, "file://") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Path == "" {
		return ""
	}
	// decoded already by url.Parse
	return u.Path
}

func (t *Tracker) pushCwd(ctx context.Context, cwd string) {
	oref := waveobj.MakeORef(waveobj.OType_Block, t.blockID)
	meta := waveobj.MetaMapType{"cmd:cwd": cwd}
	if err := wstore.UpdateObjectMeta(ctx, oref, meta, false); err != nil {
		log.Printf("cmdblock: update cmd:cwd for %s: %v", t.blockID, err)
		return
	}
	wcore.SendWaveObjUpdate(oref)
}

// detectAltScreen scans chunk for the DECSET/DECRST 1049 sequences that
// toggle the alternate screen buffer. When the state flips, publishes a
// cmdblock:altscreen event so the frontend can swap its layout. The scan
// is simplistic — a sequence split exactly across two chunks would be
// missed — but the app itself will usually re-emit on the next toggle.
func (t *Tracker) detectAltScreen(chunk []byte) {
	enter := bytes.LastIndex(chunk, altScreenEnterSeq)
	exit := bytes.LastIndex(chunk, altScreenExitSeq)
	var target bool
	if enter < 0 && exit < 0 {
		return
	}
	if enter > exit {
		target = true
	} else {
		target = false
	}
	if t.altScreen == target {
		return
	}
	t.altScreen = target
	wps.Broker.Publish(wps.WaveEvent{
		Event:  wps.Event_CmdBlockAltScreen,
		Scopes: []string{"block:" + t.blockID},
		Data: &cbtypes.CmdBlockAltScreenEvent{
			BlockID: t.blockID,
			OID:     t.currentOID,
			Enter:   target,
		},
	})
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
		t.state = StatePrompt
		t.publishRow(cb)
	case "C":
		if t.currentOID == "" {
			return
		}
		cmd := decodeCmd64(ev.Payload)
		outputStart := ev.Offset + int64(ev.SeqLen)
		if err := MarkCommandSubmitted(ctx, t.currentOID, cmd, "", ev.Offset, outputStart, ""); err != nil {
			log.Printf("cmdblock: MarkCommandSubmitted oid=%s: %v", t.currentOID, err)
			return
		}
		t.state = StateRunning
		if row, err := getByOID(ctx, t.currentOID); err == nil && row != nil {
			t.publishRow(row)
		}
	case "D":
		if t.currentOID == "" {
			return
		}
		oid := t.currentOID
		exit := decodeExitCode(ev.Payload)
		if err := MarkCommandDone(ctx, oid, exit, ev.Offset); err != nil {
			log.Printf("cmdblock: MarkCommandDone oid=%s: %v", oid, err)
		}
		// MarkCommandDone may have DELETEd the row (empty-Enter case); the
		// publish below degrades gracefully when getByOID returns nil.
		if row, err := getByOID(ctx, oid); err == nil {
			if row != nil {
				t.publishRow(row)
			}
		}
		t.currentOID = ""
		t.state = ""
	case "M":
		if shell := decodeShell(ev.Payload); shell != "" {
			t.shellType = shell
		}
	}
}

func (t *Tracker) publishRow(cb *cbtypes.CmdBlock) {
	wps.Broker.Publish(wps.WaveEvent{
		Event:  wps.Event_CmdBlockRow,
		Scopes: []string{"block:" + t.blockID},
		Data:   cb,
	})
}

func (t *Tracker) publishChunk(oid string, offset int64, data []byte) {
	wps.Broker.Publish(wps.WaveEvent{
		Event:  wps.Event_CmdBlockChunk,
		Scopes: []string{"block:" + t.blockID},
		Data: &cbtypes.CmdBlockChunkEvent{
			BlockID: t.blockID,
			OID:     oid,
			Offset:  offset,
			Data64:  base64.StdEncoding.EncodeToString(data),
		},
	})
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
