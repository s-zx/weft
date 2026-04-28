// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package tools

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/util/utilfn"
)

const (
	TodoStatusPending    = "pending"
	TodoStatusInProgress = "in_progress"
	TodoStatusCompleted  = "completed"
)

type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type TodoState struct {
	lock  sync.Mutex
	Items []TodoItem
}

var todoStates = sync.Map{}

func getTodoState(chatID string) *TodoState {
	val, _ := todoStates.LoadOrStore(chatID, &TodoState{})
	return val.(*TodoState)
}

func ClearTodoState(chatID string) {
	todoStates.Delete(chatID)
}

func HasPendingTodos(chatID string) bool {
	val, ok := todoStates.Load(chatID)
	if !ok {
		return false
	}
	state := val.(*TodoState)
	state.lock.Lock()
	defer state.lock.Unlock()
	for _, item := range state.Items {
		if item.Status == TodoStatusPending || item.Status == TodoStatusInProgress {
			return true
		}
	}
	return false
}

type todoWriteInput struct {
	Todos []TodoItem `json:"todos"`
}

func TodoWrite(chatID string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "todo_write",
		DisplayName: "Todo Write",
		Description: "Track your progress by updating a todo list. Items are upserted by matching on content text. Mention only the items you want to add or update; unmentioned items are left unchanged.",
		ToolLogName: "agent:todo_write",
		Prompt: `todo_write: Maintains a structured todo list for multi-step work.
- Use it ANY time the user's request decomposes into ≥3 distinct steps. Skip it for trivial one-shot tasks.
- Update is diff-based: only mention items you want to insert or change status. Items NOT mentioned stay as they are.
- Status values: "pending" (not started), "in_progress" (actively working — keep at most one in_progress at a time), "completed" (done).
- Mark an item completed AS SOON AS it's done — don't batch updates. The user watches this list to see progress.
- Don't pre-fill an entire plan and then never update it. Each completed step should produce a todo_write call.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content": map[string]any{"type": "string", "description": "Todo item text. Used as unique key for matching existing items."},
							"status":  map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}, "description": "Item status."},
						},
						"required": []string{"content", "status"},
					},
					"description": "Diff-based update: items mentioned are upserted (matched by content), items not mentioned are unchanged. To remove an item, omit it and it will be removed on the next full rewrite.",
				},
			},
			"required":             []string{"todos"},
			"additionalProperties": false,
		},
		ToolCallDesc: func(input any, _ any, _ *uctypes.UIMessageDataToolUse) string {
			state := getTodoState(chatID)
			state.lock.Lock()
			defer state.lock.Unlock()
			total := len(state.Items)
			pending := 0
			for _, item := range state.Items {
				if item.Status == TodoStatusPending || item.Status == TodoStatusInProgress {
					pending++
				}
			}
			return fmt.Sprintf("updated %d todos (%d pending)", total, pending)
		},
		ToolTextCallback: func(input any) (string, error) {
			params := &todoWriteInput{}
			if input == nil {
				return "", fmt.Errorf("input is required")
			}
			if err := utilfn.ReUnmarshal(params, input); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}
			if len(params.Todos) == 0 {
				return "", fmt.Errorf("todos array is required and must not be empty")
			}
			state := getTodoState(chatID)
			state.lock.Lock()
			defer state.lock.Unlock()
			for _, incoming := range params.Todos {
				found := false
				for i := range state.Items {
					if state.Items[i].Content == incoming.Content {
						state.Items[i].Status = incoming.Status
						found = true
						break
					}
				}
				if !found {
					state.Items = append(state.Items, TodoItem{
						Content: incoming.Content,
						Status:  incoming.Status,
					})
				}
			}
			result, err := json.Marshal(state.Items)
			if err != nil {
				return "", fmt.Errorf("marshal todos: %w", err)
			}
			return string(result), nil
		},
		ToolApproval: func(_ any) string {
			return uctypes.ApprovalAutoApproved
		},
	}
}

func TodoRead(chatID string, approval func(any) string) uctypes.ToolDefinition {
	return uctypes.ToolDefinition{
		Name:        "todo_read",
		DisplayName: "Todo Read",
		Description: "Read the current todo list to check progress and pending items.",
		ToolLogName: "agent:todo_read",
		Parallel:    true,
		Prompt: `todo_read: Returns the current todo list.
- Use when you've been working a while and want to confirm what's still pending before deciding the next step.
- Cheap — no side effects. Parallel-safe.`,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":          map[string]any{},
			"additionalProperties": false,
		},
		ToolCallDesc: func(_ any, _ any, _ *uctypes.UIMessageDataToolUse) string {
			state := getTodoState(chatID)
			state.lock.Lock()
			defer state.lock.Unlock()
			return fmt.Sprintf("read %d todos", len(state.Items))
		},
		ToolTextCallback: func(_ any) (string, error) {
			state := getTodoState(chatID)
			state.lock.Lock()
			defer state.lock.Unlock()
			result, err := json.Marshal(state.Items)
			if err != nil {
				return "", fmt.Errorf("marshal todos: %w", err)
			}
			return string(result), nil
		},
		ToolApproval: func(_ any) string {
			return uctypes.ApprovalAutoApproved
		},
	}
}
