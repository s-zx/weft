// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package chatstore

import (
	"fmt"
	"testing"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

type testMsg struct {
	id   string
	role string
}

func (m *testMsg) GetMessageId() string          { return m.id }
func (m *testMsg) GetRole() string               { return m.role }
func (m *testMsg) GetUsage() *uctypes.AIUsage    { return nil }

func makeTestStore(chatId string, n int) *ChatStore {
	cs := &ChatStore{chats: make(map[string]*uctypes.AIChat)}
	chat := &uctypes.AIChat{
		ChatId:  chatId,
		APIType: "mock",
		Model:   "mock",
	}
	for i := 0; i < n; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		chat.NativeMessages = append(chat.NativeMessages, &testMsg{
			id:   fmt.Sprintf("msg-%d", i),
			role: role,
		})
	}
	cs.chats[chatId] = chat
	return cs
}

func TestCompactMessages_RemovesMiddle(t *testing.T) {
	cs := makeTestStore("c1", 20)
	removed := cs.CompactMessages("c1", 2, 3)
	if removed != 15 {
		t.Fatalf("removed = %d, want 15", removed)
	}
	chat := cs.Get("c1")
	if len(chat.NativeMessages) != 5 {
		t.Fatalf("remaining = %d, want 5", len(chat.NativeMessages))
	}
	if chat.NativeMessages[0].GetMessageId() != "msg-0" {
		t.Fatalf("first msg = %s, want msg-0", chat.NativeMessages[0].GetMessageId())
	}
	if chat.NativeMessages[1].GetMessageId() != "msg-1" {
		t.Fatalf("second msg = %s, want msg-1", chat.NativeMessages[1].GetMessageId())
	}
	if chat.NativeMessages[2].GetMessageId() != "msg-17" {
		t.Fatalf("third msg = %s, want msg-17", chat.NativeMessages[2].GetMessageId())
	}
}

func TestCompactMessages_NoopWhenSmall(t *testing.T) {
	cs := makeTestStore("c2", 5)
	removed := cs.CompactMessages("c2", 2, 3)
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 (no compaction needed)", removed)
	}
	chat := cs.Get("c2")
	if len(chat.NativeMessages) != 5 {
		t.Fatalf("messages = %d, want 5", len(chat.NativeMessages))
	}
}

func TestCompactMessages_NilChat(t *testing.T) {
	cs := &ChatStore{chats: make(map[string]*uctypes.AIChat)}
	removed := cs.CompactMessages("nonexistent", 1, 5)
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

func TestCompactMessages_KeepFirstOneLastTen(t *testing.T) {
	cs := makeTestStore("c3", 50)
	removed := cs.CompactMessages("c3", 1, 10)
	if removed != 39 {
		t.Fatalf("removed = %d, want 39", removed)
	}
	chat := cs.Get("c3")
	if len(chat.NativeMessages) != 11 {
		t.Fatalf("remaining = %d, want 11", len(chat.NativeMessages))
	}
	if chat.NativeMessages[0].GetMessageId() != "msg-0" {
		t.Fatal("first message should be msg-0")
	}
	if chat.NativeMessages[1].GetMessageId() != "msg-40" {
		t.Fatalf("second msg = %s, want msg-40", chat.NativeMessages[1].GetMessageId())
	}
}
