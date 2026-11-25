/*
 * Copyright 2024 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package claude

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestEmptyToolResultPreservesAssociation(t *testing.T) {
	cm := &ChatModel{model: "test", maxTokens: 100}
	assistant := schema.AssistantMessage("", []schema.ToolCall{
		{ID: "call_1", Function: schema.FunctionCall{Name: "first", Arguments: "{}"}},
		{ID: "call_2", Function: schema.FunctionCall{Name: "second", Arguments: "{}"}},
	})
	params, _, err := cm.genParamsAndOptions([]*schema.Message{
		schema.UserMessage("run tools"),
		assistant,
		{Role: schema.Tool, ToolCallID: "call_1", Content: "nonempty"},
		{Role: schema.Tool, ToolCallID: "call_2", Content: ""},
	}, WithAutoCacheControl(&CacheControl{TTL: CacheTTL1h}))
	require.NoError(t, err)
	require.Len(t, params.Messages, 3)
	require.Len(t, params.Messages[1].Content, 2)
	require.NotNil(t, params.Messages[1].Content[0].OfToolUse)
	require.Equal(t, "call_1", params.Messages[1].Content[0].OfToolUse.ID)
	require.Len(t, params.Messages[2].Content, 2)
	first := params.Messages[2].Content[0].OfToolResult
	second := params.Messages[2].Content[1].OfToolResult
	require.NotNil(t, first)
	require.NotNil(t, second)
	require.Equal(t, "call_1", first.ToolUseID)
	require.Equal(t, "call_2", second.ToolUseID)
	require.Empty(t, second.Content)
	require.Equal(t, CacheTTL1h, second.CacheControl.TTL)
	raw, err := json.Marshal(params.Messages[2].Content[1])
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	require.Equal(t, "tool_result", wire["type"])
	require.Equal(t, "call_2", wire["tool_use_id"])
	require.NotContains(t, wire, "content")
}

func TestEmptyAssistantWithAutoCache(t *testing.T) {
	cm := &ChatModel{model: "test", maxTokens: 100}
	for _, tc := range []struct {
		name string
		msg  *schema.Message
	}{
		{name: "empty text", msg: schema.AssistantMessage("", nil)},
		{name: "tool only", msg: schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call_1", Function: schema.FunctionCall{Name: "run", Arguments: "{}"}},
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params, _, err := cm.genParamsAndOptions([]*schema.Message{
				schema.UserMessage("hello"), tc.msg,
			}, WithAutoCacheControl(&CacheControl{TTL: CacheTTL1h}))
			require.NoError(t, err)
			require.Len(t, params.Messages, 2)
			require.Len(t, params.Messages[1].Content, 1)
			block := params.Messages[1].Content[0]
			if len(tc.msg.ToolCalls) != 0 {
				require.NotNil(t, block.OfToolUse)
				require.Equal(t, "call_1", block.OfToolUse.ID)
				require.Equal(t, CacheTTL1h, block.OfToolUse.CacheControl.TTL)
			} else {
				require.NotNil(t, block.OfText)
				require.Empty(t, block.OfText.Text)
				require.Equal(t, CacheTTL1h, block.OfText.CacheControl.TTL)
			}
		})
	}
}
