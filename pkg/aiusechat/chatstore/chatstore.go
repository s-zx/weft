// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package chatstore

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
)

type ChatStore struct {
	lock  sync.Mutex
	chats map[string]*uctypes.AIChat
}

var DefaultChatStore = &ChatStore{
	chats: make(map[string]*uctypes.AIChat),
}

func (cs *ChatStore) Get(chatId string) *uctypes.AIChat {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	chat := cs.chats[chatId]
	if chat == nil {
		return nil
	}

	// Copy the chat to prevent concurrent access issues
	copyChat := &uctypes.AIChat{
		ChatId:         chat.ChatId,
		APIType:        chat.APIType,
		Model:          chat.Model,
		APIVersion:     chat.APIVersion,
		NativeMessages: make([]uctypes.GenAIMessage, len(chat.NativeMessages)),
	}
	copy(copyChat.NativeMessages, chat.NativeMessages)

	return copyChat
}

// GetForLLM returns the chat with transcript-only messages filtered out —
// any message implementing LLMVisibleProvider with LLMVisible()==false is
// dropped from NativeMessages. Use this at the LLM-serialization boundary
// (backend RunChatStep) when transcript-only message types are in play.
//
// Today no built-in message type returns false from LLMVisible(), so the
// output is identical to Get(). The helper exists so future transcript-only
// types (subagent transcripts, denial notes, branch markers) can be added
// without backend changes — they go in the chatstore via PostMessage and
// are filtered out here.
func (cs *ChatStore) GetForLLM(chatId string) *uctypes.AIChat {
	chat := cs.Get(chatId)
	if chat == nil {
		return nil
	}
	// Get already returned a copy with its own NativeMessages slice,
	// so reassigning here doesn't touch the store's state.
	chat.NativeMessages = uctypes.FilterLLMVisible(chat.NativeMessages)
	return chat
}

func (cs *ChatStore) Delete(chatId string) {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	delete(cs.chats, chatId)
}

func (cs *ChatStore) CountUserMessages(chatId string) int {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	chat := cs.chats[chatId]
	if chat == nil {
		return 0
	}

	count := 0
	for _, msg := range chat.NativeMessages {
		if msg.GetRole() == "user" {
			count++
		}
	}
	return count
}

// UpdateMessage atomically applies fn to the message with the given messageId
// while holding the chat lock. fn is called with the live pointer; if it
// returns a non-nil replacement, the slot is replaced. Fixes a TOCTOU race
// where concurrent UpdateToolUseData calls (e.g. parallel tool fan-out) would
// each Get the chat, modify a clone, and PostMessage — last-writer-wins
// silently dropped earlier updates. Returns false if the message wasn't found.
func (cs *ChatStore) UpdateMessage(chatId string, messageId string, fn func(uctypes.GenAIMessage) uctypes.GenAIMessage) bool {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	chat := cs.chats[chatId]
	if chat == nil {
		return false
	}
	for i, msg := range chat.NativeMessages {
		if msg.GetMessageId() != messageId {
			continue
		}
		if replacement := fn(msg); replacement != nil {
			chat.NativeMessages[i] = replacement
		}
		return true
	}
	return false
}

// FindMessageIdByPredicate returns the messageId of the first message satisfying pred,
// scanning under the chat lock. Used by backends to bridge "find message by tool call id"
// over to UpdateMessage without exposing internals.
func (cs *ChatStore) FindMessageIdByPredicate(chatId string, pred func(uctypes.GenAIMessage) bool) (string, bool) {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	chat := cs.chats[chatId]
	if chat == nil {
		return "", false
	}
	for _, msg := range chat.NativeMessages {
		if pred(msg) {
			return msg.GetMessageId(), true
		}
	}
	return "", false
}

func (cs *ChatStore) PostMessage(chatId string, aiOpts *uctypes.AIOptsType, message uctypes.GenAIMessage) error {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	chat := cs.chats[chatId]
	if chat == nil {
		// Create new chat
		chat = &uctypes.AIChat{
			ChatId:         chatId,
			APIType:        aiOpts.APIType,
			Model:          aiOpts.Model,
			APIVersion:     aiOpts.APIVersion,
			NativeMessages: make([]uctypes.GenAIMessage, 0),
		}
		cs.chats[chatId] = chat
	} else {
		// Verify that the AI options match
		if chat.APIType != aiOpts.APIType {
			return fmt.Errorf("API type mismatch: expected %s, got %s (must start a new chat)", chat.APIType, aiOpts.APIType)
		}
		if !uctypes.AreModelsCompatible(chat.APIType, chat.Model, aiOpts.Model) {
			return fmt.Errorf("model mismatch: expected %s, got %s (must start a new chat)", chat.Model, aiOpts.Model)
		}
		if chat.APIVersion != aiOpts.APIVersion {
			return fmt.Errorf("API version mismatch: expected %s, got %s (must start a new chat)", chat.APIVersion, aiOpts.APIVersion)
		}
	}

	// Check for existing message with same ID (idempotency)
	messageId := message.GetMessageId()
	for i, existingMessage := range chat.NativeMessages {
		if existingMessage.GetMessageId() == messageId {
			// Replace existing message with same ID
			chat.NativeMessages[i] = message
			return nil
		}
	}

	// Append the new message if no duplicate found
	chat.NativeMessages = append(chat.NativeMessages, message)

	return nil
}

func (cs *ChatStore) CompactMessages(chatId string, keepFirst, keepLast int) int {
	_, removed := cs.CompactMessagesWithSummary(chatId, keepFirst, keepLast)
	return removed
}

// CollapseOldToolResults shrinks the inline content of older tool result
// messages without removing them. The most recent keepLastN messages are
// left untouched. For each older message that implements
// uctypes.ToolResultCollapsible (every backend's tool-result-bearing type
// does), the tool result body is replaced with `placeholder`.
//
// Returns the total number of tool result blocks/parts collapsed across
// all touched messages. Zero means nothing to do (e.g. history shorter
// than keepLastN, or all older messages are non-tool-result).
//
// This is the cheap tier between proactive microcompact (which deletes
// whole messages) and reactive summary compact (which is heavier and
// loses message structure).
func (cs *ChatStore) CollapseOldToolResults(chatId string, keepLastN int, placeholder string) int {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	chat := cs.chats[chatId]
	if chat == nil {
		return 0
	}
	total := len(chat.NativeMessages)
	if total <= keepLastN {
		return 0
	}
	collapsed := 0
	limit := total - keepLastN
	for i := 0; i < limit; i++ {
		if c, ok := chat.NativeMessages[i].(uctypes.ToolResultCollapsible); ok {
			collapsed += c.CollapseToolResults(placeholder)
		}
	}
	return collapsed
}

func (cs *ChatStore) CompactMessagesWithSummary(chatId string, keepFirst, keepLast int) (string, int) {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	chat := cs.chats[chatId]
	if chat == nil {
		return "", 0
	}
	total := len(chat.NativeMessages)
	if total <= keepFirst+keepLast {
		return "", 0
	}
	tailStart := total - keepLast
	for tailStart < total {
		if dep, ok := chat.NativeMessages[tailStart].(uctypes.MessageDependsOnPrev); ok && dep.DependsOnPrev() {
			tailStart++
			continue
		}
		break
	}
	droppedRange := chat.NativeMessages[keepFirst:tailStart]
	summary := buildCompactionSummary(droppedRange)
	kept := make([]uctypes.GenAIMessage, 0, keepFirst+(total-tailStart))
	kept = append(kept, chat.NativeMessages[:keepFirst]...)
	kept = append(kept, chat.NativeMessages[tailStart:]...)
	removed := total - len(kept)
	if removed <= 0 {
		return "", 0
	}
	chat.NativeMessages = kept
	return summary, removed
}

func buildCompactionSummary(dropped []uctypes.GenAIMessage) string {
	if len(dropped) == 0 {
		return ""
	}
	roleCounts := make(map[string]int)
	for _, msg := range dropped {
		roleCounts[msg.GetRole()]++
	}
	var parts []string
	for _, role := range []string{"user", "assistant", "tool"} {
		if c := roleCounts[role]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c, role))
		}
	}
	for role, c := range roleCounts {
		if role != "user" && role != "assistant" && role != "tool" {
			parts = append(parts, fmt.Sprintf("%d %s", c, role))
		}
	}
	return fmt.Sprintf("[Context compacted: %d messages summarized (%s). Earlier conversation history has been condensed to fit within the context window. Key decisions and file changes from those messages may need to be re-verified.]",
		len(dropped), strings.Join(parts, ", "))
}

func (cs *ChatStore) PopLastMessages(chatId string, count int) int {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	chat := cs.chats[chatId]
	if chat == nil || count <= 0 {
		return 0
	}
	total := len(chat.NativeMessages)
	if count > total {
		count = total
	}
	chat.NativeMessages = chat.NativeMessages[:total-count]
	return count
}

func (cs *ChatStore) RemoveMessage(chatId string, messageId string) bool {
	cs.lock.Lock()
	defer cs.lock.Unlock()

	chat := cs.chats[chatId]
	if chat == nil {
		return false
	}

	initialLen := len(chat.NativeMessages)
	chat.NativeMessages = slices.DeleteFunc(chat.NativeMessages, func(msg uctypes.GenAIMessage) bool {
		return msg.GetMessageId() == messageId
	})

	return len(chat.NativeMessages) < initialLen
}
