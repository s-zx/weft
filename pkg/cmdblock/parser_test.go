// Copyright 2026, s-zx
// SPDX-License-Identifier: Apache-2.0

package cmdblock

import (
	"testing"
)

func TestParserSimpleBel(t *testing.T) {
	p := MakeParser()
	evs := p.Feed([]byte("\x1b]16162;A\x07"))
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Command != "A" || evs[0].Payload != "" {
		t.Errorf("bad event: %+v", evs[0])
	}
	if evs[0].Offset != 0 {
		t.Errorf("offset want 0, got %d", evs[0].Offset)
	}
	if evs[0].SeqLen != len("\x1b]16162;A\x07") {
		t.Errorf("seqlen want 10, got %d", evs[0].SeqLen)
	}
}

func TestParserWithJSONPayload(t *testing.T) {
	p := MakeParser()
	data := `\x1b]16162;D;{"exitcode":42}\x07`
	_ = data
	chunk := []byte("\x1b]16162;D;{\"exitcode\":42}\x07")
	evs := p.Feed(chunk)
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Command != "D" {
		t.Errorf("cmd want D, got %q", evs[0].Command)
	}
	if evs[0].Payload != `{"exitcode":42}` {
		t.Errorf("payload want JSON, got %q", evs[0].Payload)
	}
}

func TestParserStTerminator(t *testing.T) {
	p := MakeParser()
	chunk := []byte("\x1b]16162;C;{\"cmd64\":\"bHM=\"}\x1b\\")
	evs := p.Feed(chunk)
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Command != "C" || evs[0].Payload != `{"cmd64":"bHM="}` {
		t.Errorf("bad event: %+v", evs[0])
	}
	if evs[0].SeqLen != len(chunk) {
		t.Errorf("seqlen want %d, got %d", len(chunk), evs[0].SeqLen)
	}
}

func TestParserSplitAcrossFeeds(t *testing.T) {
	p := MakeParser()
	evs := p.Feed([]byte("prelude\x1b]1616"))
	if len(evs) != 0 {
		t.Fatalf("want 0 events on partial, got %d", len(evs))
	}
	evs = p.Feed([]byte("2;A\x07"))
	if len(evs) != 1 {
		t.Fatalf("want 1 event after completion, got %d", len(evs))
	}
	if evs[0].Command != "A" {
		t.Errorf("cmd want A, got %q", evs[0].Command)
	}
	wantOffset := int64(len("prelude"))
	if evs[0].Offset != wantOffset {
		t.Errorf("offset want %d, got %d", wantOffset, evs[0].Offset)
	}
}

func TestParserMultiplePerFeed(t *testing.T) {
	p := MakeParser()
	chunk := []byte("\x1b]16162;A\x07hello\x1b]16162;D;{\"exitcode\":0}\x07")
	evs := p.Feed(chunk)
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	if evs[0].Command != "A" || evs[1].Command != "D" {
		t.Errorf("want A then D, got %q then %q", evs[0].Command, evs[1].Command)
	}
	gap := evs[1].Offset - (evs[0].Offset + int64(evs[0].SeqLen))
	if gap != 5 { // "hello" between them
		t.Errorf("want 5 bytes between seqs, got %d", gap)
	}
}

func TestParserIgnoresOtherOSC(t *testing.T) {
	p := MakeParser()
	// OSC 7 (cwd) should not fire as a 16162 event
	evs := p.Feed([]byte("\x1b]7;file://localhost/tmp\x07\x1b]16162;A\x07"))
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Command != "A" {
		t.Errorf("want A, got %q", evs[0].Command)
	}
	// offset should land on the 16162 sequence, not the OSC 7
	wantOffset := int64(len("\x1b]7;file://localhost/tmp\x07"))
	if evs[0].Offset != wantOffset {
		t.Errorf("offset want %d, got %d", wantOffset, evs[0].Offset)
	}
}

func TestParserNoSpuriousMatchOnPrefixSplit(t *testing.T) {
	p := MakeParser()
	// Feed "\x1b]1616" (looks like it could be start of 16162 but could also be 1616)
	// The parser must retain partial prefix state and match only when ";" follows.
	evs := p.Feed([]byte("\x1b]1616;nope\x07"))
	if len(evs) != 0 {
		t.Fatalf("want 0 events (not our OSC), got %+v", evs)
	}
}

func TestParserOffsetAccumulatesAcrossFeeds(t *testing.T) {
	p := MakeParser()
	p.Feed([]byte("aaaa"))
	p.Feed([]byte("bbbb"))
	evs := p.Feed([]byte("\x1b]16162;A\x07"))
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if evs[0].Offset != 8 {
		t.Errorf("offset want 8, got %d", evs[0].Offset)
	}
}

func TestParserBufferCapOnRuntInput(t *testing.T) {
	// Stream that starts a prefix but never terminates should not grow unbounded.
	p := MakeParser()
	p.Feed([]byte("\x1b]16162;"))
	big := make([]byte, 20*1024)
	for i := range big {
		big[i] = 'x'
	}
	p.Feed(big)
	if len(p.buf) > maxBuffered {
		t.Errorf("buffer grew past cap: %d bytes", len(p.buf))
	}
}
