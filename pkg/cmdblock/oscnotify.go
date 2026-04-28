// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

package cmdblock

import (
	"bytes"
	"strings"

	"github.com/s-zx/crest/pkg/cmdblock/cbtypes"
	"github.com/s-zx/crest/pkg/wps"
)

// Notification escape sequences emitted by AI coding agents (Claude Code,
// Aider, etc.) and various TUI apps to signal "please surface this to the
// user".  We parse these directly from the PTY stream so nothing downstream
// has to guess at agent state or poll completion events.
//
//	OSC   9 ; <body>                  ST   — iTerm2 growl notification
//	OSC  99 ; [d=<id>;] <body>        ST   — Kitty desktop notification
//	OSC 777 ; notify ; <title> [; <body>] ST — rxvt-unicode notify module
//
// ST is either BEL (0x07) or the two-byte "ESC \".
var (
	osc9Prefix   = []byte("\x1b]9;")
	osc99Prefix  = []byte("\x1b]99;")
	osc777Prefix = []byte("\x1b]777;notify;")
)

// findOscTerminator returns the index of the BEL/ST that terminates an OSC
// sequence starting at start, and the length of that terminator (1 or 2).
// Returns (-1, 0) if the chunk ends before a terminator arrives.
func findOscTerminator(chunk []byte, start int) (int, int) {
	for j := start; j < len(chunk); j++ {
		if chunk[j] == 0x07 {
			return j, 1
		}
		if chunk[j] == 0x1b && j+1 < len(chunk) && chunk[j+1] == '\\' {
			return j, 2
		}
	}
	return -1, 0
}

// detectOsc9 scans chunk for iTerm2-style OSC 9 notifications.  The
// "OSC 9 ; 4 ; ..." subspace is iTerm2's progress indicator protocol (not a
// notification), so we filter those out.
func (t *Tracker) detectOsc9(chunk []byte) {
	for i := 0; i < len(chunk); {
		idx := bytes.Index(chunk[i:], osc9Prefix)
		if idx < 0 {
			return
		}
		start := i + idx + len(osc9Prefix)
		end, endLen := findOscTerminator(chunk, start)
		if end < 0 {
			return
		}
		payload := string(chunk[start:end])
		i = end + endLen
		// Skip iTerm2 progress-bar updates (OSC 9 ; 4 ; state ; progress).
		if strings.HasPrefix(payload, "4;") {
			continue
		}
		if payload == "" {
			continue
		}
		t.publishNotify("", payload)
	}
}

// detectOsc99 scans chunk for Kitty-style desktop notifications.  Kitty's
// protocol uses a richer "key=value;" parameter list before the body; we keep
// just the body for display.  Sequences with "o=" options that do not end with
// ":p=body" are control messages (hold, action, close, etc.) and are ignored.
func (t *Tracker) detectOsc99(chunk []byte) {
	for i := 0; i < len(chunk); {
		idx := bytes.Index(chunk[i:], osc99Prefix)
		if idx < 0 {
			return
		}
		start := i + idx + len(osc99Prefix)
		end, endLen := findOscTerminator(chunk, start)
		if end < 0 {
			return
		}
		payload := string(chunk[start:end])
		i = end + endLen
		// Kitty separates the optional params from the body with a single ";".
		// Params are "k=v" pairs joined by ":".  Multi-chunk notifications use
		// the "i=<id>" param to identify them; we don't reassemble — first
		// chunk body is usually enough for an alert.
		body := payload
		if semi := strings.Index(payload, ";"); semi >= 0 {
			params := payload[:semi]
			body = payload[semi+1:]
			// Skip control messages (e.g. o=close, o=action) — they carry no
			// user-visible body.
			for _, kv := range strings.Split(params, ":") {
				if strings.HasPrefix(kv, "o=") && kv != "o=" {
					// "o=" alone is "append", which we treat as body-carrying.
					v := kv[2:]
					if v != "" && v != "a" {
						body = ""
					}
				}
			}
		}
		if body == "" {
			continue
		}
		t.publishNotify("", body)
	}
}

// detectOsc777 scans chunk for rxvt-unicode's notify module.  Format is
// "ESC ] 777 ; notify ; <title> [; <body>] ST"; title is required, body
// optional.
func (t *Tracker) detectOsc777(chunk []byte) {
	for i := 0; i < len(chunk); {
		idx := bytes.Index(chunk[i:], osc777Prefix)
		if idx < 0 {
			return
		}
		start := i + idx + len(osc777Prefix)
		end, endLen := findOscTerminator(chunk, start)
		if end < 0 {
			return
		}
		payload := string(chunk[start:end])
		i = end + endLen
		if payload == "" {
			continue
		}
		var title, body string
		if semi := strings.Index(payload, ";"); semi >= 0 {
			title = payload[:semi]
			body = payload[semi+1:]
		} else {
			title = payload
		}
		if title == "" && body == "" {
			continue
		}
		t.publishNotify(title, body)
	}
}

func (t *Tracker) publishNotify(title, body string) {
	wps.Broker.Publish(wps.WaveEvent{
		Event:  wps.Event_CmdBlockNotify,
		Scopes: []string{"block:" + t.blockID},
		Data: &cbtypes.CmdBlockNotifyEvent{
			BlockID: t.blockID,
			OID:     t.currentOID,
			Title:   title,
			Body:    body,
		},
	})
}
