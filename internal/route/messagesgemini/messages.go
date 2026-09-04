package messagesgemini

import (
	"bytes"
	"encoding/json"

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
