// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

package cmdblock

import (
	"bytes"
	"strings"
)

// Streaming parser for OSC 16162 shell-integration sequences.
//
// An OSC 16162 sequence looks like:
//
//	ESC ] 16162 ; <payload> ST
//
// where ST is either BEL (0x07) or the two-byte "ESC \\". The payload has the
// form "CMD" or "CMD;JSON", where CMD is a single letter (A/C/D/M/I/R). The
// parser tracks partial sequences across Feed() calls and emits one Event per
// fully-received sequence. Everything else in the stream is ignored.
//
// Absolute byte offsets into the original stream are preserved so callers can
// align events to byte ranges in a blockfile.

const (
	oscPrefix   = "\x1b]16162;"
	maxBuffered = 8 * 1024 // cap when a malformed stream never sends a terminator
)

type Event struct {
	Command string // "A", "C", "D", "M", "I", "R"
	Payload string // raw bytes between the leading ';' and the terminator; may be ""
	Offset  int64  // absolute offset of the leading ESC in the stream
	SeqLen  int    // full byte length of the OSC sequence including terminator
}

type Parser struct {
	buf     []byte
	bufBase int64 // absolute offset of buf[0] in the original stream
}

func MakeParser() *Parser {
	return &Parser{buf: make([]byte, 0, 256)}
}

// Feed appends a chunk of PTY bytes and returns any events completed by this
// chunk. The returned slice is only valid until the next Feed call.
func (p *Parser) Feed(chunk []byte) []Event {
	if len(chunk) == 0 {
		return nil
	}
	p.buf = append(p.buf, chunk...)

	var events []Event
	scanFrom := 0

	for {
		// find the next OSC 16162 prefix starting at scanFrom
		rel := bytes.Index(p.buf[scanFrom:], []byte(oscPrefix))
		if rel < 0 {
			// no prefix in sight; retain only last (len(oscPrefix)-1) bytes in case
			// the prefix is straddling the chunk boundary.
			keep := len(oscPrefix) - 1
			if len(p.buf)-scanFrom > keep {
				scanFrom = len(p.buf) - keep
			}
			break
		}
		absStart := scanFrom + rel
		payloadStart := absStart + len(oscPrefix)
		if payloadStart >= len(p.buf) {
			scanFrom = absStart
			break
		}

		termAt := -1
		termLen := 0
		for j := payloadStart; j < len(p.buf); j++ {
			if p.buf[j] == 0x07 {
				termAt = j
				termLen = 1
				break
			}
			if p.buf[j] == 0x1b && j+1 < len(p.buf) && p.buf[j+1] == '\\' {
				termAt = j
				termLen = 2
				break
			}
			if p.buf[j] == 0x1b && j+1 >= len(p.buf) {
				// possible start of ST "ESC \\" but the backslash is not here yet
				break
			}
		}
		if termAt < 0 {
			scanFrom = absStart
			break
		}

		payload := string(p.buf[payloadStart:termAt])
		ev := Event{
			Offset: p.bufBase + int64(absStart),
			SeqLen: (termAt + termLen) - absStart,
		}
		if idx := strings.IndexByte(payload, ';'); idx >= 0 {
			ev.Command = payload[:idx]
			ev.Payload = payload[idx+1:]
		} else {
			ev.Command = payload
		}
		events = append(events, ev)
		scanFrom = termAt + termLen
	}

	if scanFrom > 0 {
		copy(p.buf, p.buf[scanFrom:])
		p.buf = p.buf[:len(p.buf)-scanFrom]
		p.bufBase += int64(scanFrom)
	}

	if len(p.buf) > maxBuffered {
		drop := len(p.buf) - maxBuffered/2
		copy(p.buf, p.buf[drop:])
		p.buf = p.buf[:len(p.buf)-drop]
		p.bufBase += int64(drop)
	}

	return events
}

// Offset returns the absolute byte offset representing the end of the stream
// consumed so far (bufBase + len(buf)).
func (p *Parser) Offset() int64 {
	return p.bufBase + int64(len(p.buf))
}
