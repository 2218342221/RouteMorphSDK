package chatmessages

import (
	"bytes"
	"encoding/json"
	"fmt"

	messageswire "github.com/2218342221/RouteMorphSDK/internal/wire/messages"
)

type messagesRequest = messageswire.Request
type messagesMessage = messageswire.Message
type messagesBlock = messageswire.Block
type messagesTool = messageswire.Tool
type messagesThinking = messageswire.Thinking
type messagesOutputConfig = messageswire.OutputConfig

func validateMessagesThinking(thinking *messagesThinking, path string) error {
	if thinking == nil {
		return nil
	}
	switch thinking.Type {
	case "disabled", "adaptive":
		if thinking.BudgetTokens != 0 {
			return invalid(ProtocolMessages, path+".budget_tokens", "budget_tokens is only valid for enabled thinking")
		}
	case "enabled":
		if thinking.BudgetTokens < 1024 {
			return invalid(ProtocolMessages, path+".budget_tokens", "must be at least 1024 for enabled thinking")
		}
	default:
		return unsupported(ProtocolMessages, path+".type", "thinking type %q is not portable", thinking.Type)
	}
	if thinking.Display != "" {
		if thinking.Type != "adaptive" && thinking.Type != "enabled" {
			return invalid(ProtocolMessages, path+".display", "display is only valid for adaptive or enabled thinking")
		}
		if thinking.Display != "summarized" && thinking.Display != "omitted" {
			return unsupported(ProtocolMessages, path+".display", "thinking display %q is not portable", thinking.Display)
		}
	}
	return nil
}

func validateMessagesThinkingBudget(thinking *messagesThinking, maxTokens int, path string) error {
	if err := validateMessagesThinking(thinking, path); err != nil {
		return err
	}
	if thinking != nil && thinking.Type == "enabled" && thinking.BudgetTokens >= maxTokens {
		return invalid(ProtocolMessages, path+".budget_tokens", "must be less than max_tokens")
	}
	return nil
}

func validateMessagesOutputConfig(config *messagesOutputConfig, path string) error {
	if config == nil {
		return nil
	}
	if config.Effort != "" {
		switch config.Effort {
		case "low", "medium", "high", "xhigh", "max":
		default:
			return invalid(ProtocolMessages, path+".effort", "unsupported effort %q", config.Effort)
		}
	}
	if config.Format != nil {
		if config.Format.Type != "json_schema" {
			return unsupported(ProtocolMessages, path+".format.type", "format %q is not portable", config.Format.Type)
		}
		if !jsonValuePresent(config.Format.Schema) {
			return invalid(ProtocolMessages, path+".format.schema", "schema is required")
		}
	}
	return nil
}

func decodeMessagesContent(raw json.RawMessage, path string) ([]portablePart, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		return textParts(rawString(raw)), nil
	}
	var source []messagesBlock
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, invalid(ProtocolMessages, path, "content must be string or block array: %v", err)
	}
	parts := make([]portablePart, 0, len(source))
	for i, block := range source {
		if err := rejectMessagesBlockMetadata(block, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return nil, err
		}
		switch block.Type {
		case "text":
			parts = append(parts, portablePart{Kind: partText, Text: block.Text})
		case "thinking":
			parts = append(parts, portablePart{Kind: partReasoning, Text: block.Thinking, Opaque: block.Signature})
		case "redacted_thinking":
			parts = append(parts, portablePart{Kind: partReasoning, Opaque: block.Data})
		case "tool_use":
			if block.ID == "" || block.Name == "" {
				return nil, invalid(ProtocolMessages, fmt.Sprintf("%s[%d]", path, i), "tool_use requires id and name")
			}
			arguments, err := normalizeArguments(ProtocolMessages, fmt.Sprintf("%s[%d].input", path, i), block.Input)
			if err != nil {
				return nil, err
			}
			parts = append(parts, portablePart{Kind: partToolCall, ToolCall: &portableToolCall{ID: block.ID, Name: block.Name, Arguments: arguments}})
		case "tool_result":
			if block.ToolUseID == "" {
				return nil, invalid(ProtocolMessages, fmt.Sprintf("%s[%d].tool_use_id", path, i), "tool_use_id is required")
			}
			content, err := decodeMessagesContent(block.Content, fmt.Sprintf("%s[%d].content", path, i))
			if err != nil {
				return nil, err
			}
			parts = append(parts, portablePart{Kind: partToolResult, ToolResult: &portableToolResult{CallID: block.ToolUseID, Content: content, IsError: block.IsError}})
		case "image", "document":
			if block.Source == nil {
				return nil, invalid(ProtocolMessages, fmt.Sprintf("%s[%d].source", path, i), "source is required")
			}
			if block.Source.Type != "base64" && block.Source.Type != "url" {
				return nil, unsupported(ProtocolMessages, fmt.Sprintf("%s[%d].source.type", path, i), "source type %q is not portable", block.Source.Type)
			}
			if block.Source.Type == "base64" && block.Source.Data == "" {
				return nil, invalid(ProtocolMessages, fmt.Sprintf("%s[%d].source.data", path, i), "base64 source data is required")
			}
			if block.Source.Type == "url" && block.Source.URL == "" {
				return nil, invalid(ProtocolMessages, fmt.Sprintf("%s[%d].source.url", path, i), "URL source is required")
			}
			kind := partImage
			if block.Type == "document" {
				kind = partFile
			}
			media := &portableMedia{MIMEType: block.Source.MediaType, Data: block.Source.Data, URL: block.Source.URL}
			parts = append(parts, portablePart{Kind: kind, Media: media})
		default:
			return nil, unsupported(ProtocolMessages, fmt.Sprintf("%s[%d].type", path, i), "content block %q requires a native Messages provider", block.Type)
		}
	}
	return parts, nil
}

func rejectMessagesBlockMetadata(block messagesBlock, path string) error {
	if len(block.CacheControl) > 0 && string(block.CacheControl) != "null" {
		return unsupported(ProtocolMessages, path+".cache_control", "cache control has no portable cross-protocol equivalent")
	}
	if len(block.Citations) > 0 && string(block.Citations) != "null" && string(block.Citations) != "[]" {
		return unsupported(ProtocolMessages, path+".citations", "citations cannot be represented cross-protocol")
	}
	if jsonValuePresent(block.Caller) {
		return unsupported(ProtocolMessages, path+".caller", "tool caller metadata has no portable cross-protocol equivalent")
	}
	if block.ToolsetName != "" {
		return unsupported(ProtocolMessages, path+".toolset_name", "toolset membership has no portable cross-protocol equivalent")
	}
	return nil
}

func encodeMessagesContent(parts []portablePart) ([]messagesBlock, error) {
	blocks := make([]messagesBlock, 0, len(parts))
	for _, part := range parts {
		switch part.Kind {
		case partText:
			blocks = append(blocks, messagesBlock{Type: "text", Text: part.Text})
		case partReasoning:
			blocks = append(blocks, messagesBlock{Type: "thinking", Thinking: part.Text, Signature: part.Opaque})
		case partToolCall:
			if part.ToolCall == nil {
				return nil, invalid(ProtocolMessages, "$.messages.content", "nil tool call")
			}
			blocks = append(blocks, messagesBlock{Type: "tool_use", ID: part.ToolCall.ID, Name: part.ToolCall.Name, Input: part.ToolCall.Arguments})
		case partToolResult:
			if part.ToolResult == nil {
				return nil, invalid(ProtocolMessages, "$.messages.content", "nil tool result")
			}
			content, err := encodeMessagesContent(part.ToolResult.Content)
			if err != nil {
				return nil, err
			}
			raw, _ := json.Marshal(content)
			blocks = append(blocks, messagesBlock{Type: "tool_result", ToolUseID: part.ToolResult.CallID, Content: raw, IsError: part.ToolResult.IsError})
		case partImage, partFile:
			if part.Media == nil {
				return nil, invalid(ProtocolMessages, "$.messages.content", "nil media")
			}
			if part.Media.Detail != "" {
				return nil, unsupported(ProtocolMessages, "$.messages.content", "image detail cannot be represented by Messages")
			}
			if part.Media.URL == "" && part.Media.Data == "" {
				return nil, unsupported(ProtocolMessages, "$.messages.content", "file-id-only media cannot be represented by Messages")
			}
			if part.Media.Filename != "" {
				return nil, unsupported(ProtocolMessages, "$.messages.content", "file names cannot be represented by Messages content blocks")
			}
			blockType := "image"
			if part.Kind == partFile {
				blockType = "document"
			}
			sourceType := "base64"
			if part.Media.URL != "" {
				sourceType = "url"
			}
			block := messagesBlock{Type: blockType}
			block.Source = &struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type,omitempty"`
				Data      string `json:"data,omitempty"`
				URL       string `json:"url,omitempty"`
			}{Type: sourceType, MediaType: part.Media.MIMEType, Data: part.Media.Data, URL: part.Media.URL}
			blocks = append(blocks, block)
		case partRefusal:
			blocks = append(blocks, messagesBlock{Type: "text", Text: part.Text})
		default:
			return nil, unsupported(ProtocolMessages, "$.messages.content", "part %q is not supported", part.Kind)
		}
	}
	return blocks, nil
}

func decodeMessagesToolChoice(raw json.RawMessage) (toolChoice, *bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return toolChoice{}, nil, nil
	}
	var value struct {
		Type                   string `json:"type"`
		Name                   string `json:"name"`
		DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return toolChoice{}, nil, invalid(ProtocolMessages, "$.tool_choice", "invalid tool choice")
	}
	var parallel *bool
	if value.DisableParallelToolUse != nil && value.Type != "none" {
		enabled := !*value.DisableParallelToolUse
		parallel = &enabled
	}
	switch value.Type {
	case "auto":
		return toolChoice{Mode: toolChoiceAuto}, parallel, nil
	case "none":
		return toolChoice{Mode: toolChoiceNone}, nil, nil
	case "any":
		return toolChoice{Mode: toolChoiceRequired}, parallel, nil
	case "tool":
		if value.Name == "" {
			return toolChoice{}, nil, invalid(ProtocolMessages, "$.tool_choice.name", "name is required for tool choice type tool")
		}
		return toolChoice{Mode: toolChoiceNamed, Name: value.Name}, parallel, nil
	default:
		return toolChoice{}, nil, unsupported(ProtocolMessages, "$.tool_choice.type", "tool choice %q is not portable", value.Type)
	}
}

func encodeMessagesToolChoice(choice toolChoice, parallel *bool) json.RawMessage {
	typeName := string(choice.Mode)
	if choice.Mode == toolChoiceRequired {
		typeName = "any"
	} else if choice.Mode == toolChoiceNamed {
		typeName = "tool"
	}
	if typeName == "" {
		typeName = "auto"
	}
	value := map[string]any{"type": typeName}
	if choice.Mode == toolChoiceNamed {
		value["name"] = choice.Name
	}
	if parallel != nil && typeName != "none" {
		value["disable_parallel_tool_use"] = !*parallel
	}
	data, _ := json.Marshal(value)
	return data
}

// normalizeMessagesInputSchema follows Anthropic's required function-tool
// shape while preserving every vendor JSON Schema keyword. OpenAI-compatible
// clients commonly omit parameters entirely for parameterless functions.
func normalizeMessagesInputSchema(raw json.RawMessage, path string) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	var schema map[string]json.RawMessage
	if len(raw) != 0 && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
			return nil, invalid(ProtocolMessages, path, "input_schema must be a JSON object")
		}
	}
	if schema == nil {
		schema = make(map[string]json.RawMessage, 2)
	}
	if value, ok := schema["type"]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		schema["type"] = json.RawMessage(`"object"`)
	}
	if value, ok := schema["properties"]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		schema["properties"] = json.RawMessage(`{}`)
	}
	return json.Marshal(schema)
}

type messagesResponse = messageswire.Response

func validateMessagesResponse(source messagesResponse) error {
	if source.Type != "message" {
		return upstreamResponseError(ProtocolMessages, "$.type", "unexpected response type %q", source.Type)
	}
	if source.Role != "assistant" {
		return upstreamResponseError(ProtocolMessages, "$.role", "unexpected response role %q", source.Role)
	}
	if source.ID == "" || source.Model == "" {
		return upstreamResponseError(ProtocolMessages, "$", "response id and model are required")
	}
	if len(bytes.TrimSpace(source.Content)) == 0 || bytes.TrimSpace(source.Content)[0] != '[' {
		return upstreamResponseError(ProtocolMessages, "$.content", "content must be a block array")
	}
	if source.Usage.InputTokens < 0 || source.Usage.OutputTokens < 0 || source.Usage.CacheCreationInputTokens < 0 || source.Usage.CacheReadInputTokens < 0 {
		return upstreamResponseError(ProtocolMessages, "$.usage", "token counts must not be negative")
	}
	if _, err := parseMessagesFinish(source.StopReason); err != nil {
		return err
	}
	if source.StopReason == "stop_sequence" && source.StopSequence == "" {
		return upstreamResponseError(ProtocolMessages, "$.stop_sequence", "stop_sequence is required when stop_reason is stop_sequence")
	}
	return nil
}

func parseMessagesFinish(value string) (finishReason, error) {
	switch value {
	case "max_tokens", "model_context_window_exceeded":
		return finishLength, nil
	case "tool_use":
		return finishToolCalls, nil
	case "pause_turn":
		return "", upstreamResponseError(ProtocolMessages, "$.stop_reason", "pause_turn requires native Messages continuation semantics")
	case "refusal":
		return finishContentFilter, nil
	case "end_turn", "stop_sequence":
		return finishStop, nil
	default:
		return "", upstreamResponseError(ProtocolMessages, "$.stop_reason", "unsupported stop reason %q", value)
	}
}

func messagesStop(value finishReason) string {
	switch value {
	case finishLength:
		return "max_tokens"
	case finishToolCalls:
		return "tool_use"
	case finishContentFilter:
		return "refusal"
	default:
		return "end_turn"
	}
}
