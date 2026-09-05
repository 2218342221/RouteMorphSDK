package stream

import (
	"bytes"
	"encoding/json"

	messageswire "github.com/2218342221/RouteMorphSDK/internal/wire/messages"
)

type messagesBlock = messageswire.Block
type messagesCacheCreation = messageswire.CacheCreation
type messagesOutputTokensDetails = messageswire.OutputTokensDetails
type messagesUsage = messageswire.Usage

func decodeMessagesBlocks(raw json.RawMessage, path string) ([]messagesBlock, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, invalid(ProtocolMessages, path, "content is required")
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, invalid(ProtocolMessages, path, "invalid text content: %v", err)
		}
		return []messagesBlock{{Type: "text", Text: text}}, nil
	}
	var blocks []messagesBlock
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return nil, invalid(ProtocolMessages, path, "content must be a string or block array: %v", err)
	}
	if len(blocks) == 0 {
		return nil, invalid(ProtocolMessages, path, "at least one content block is required")
	}
	return blocks, nil
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
	if source.Usage.CacheCreation != nil {
		creation := source.Usage.CacheCreation
		if creation.Ephemeral1hInputTokens < 0 || creation.Ephemeral5mInputTokens < 0 {
			return upstreamResponseError(ProtocolMessages, "$.usage.cache_creation", "token counts must not be negative")
		}
		if creation.Ephemeral1hInputTokens+creation.Ephemeral5mInputTokens > source.Usage.CacheCreationInputTokens {
			return upstreamResponseError(ProtocolMessages, "$.usage.cache_creation", "TTL breakdown exceeds cache_creation_input_tokens")
		}
	}
	if source.Usage.OutputTokensDetails != nil {
		thinking := source.Usage.OutputTokensDetails.ThinkingTokens
		if thinking < 0 || thinking > source.Usage.OutputTokens {
			return upstreamResponseError(ProtocolMessages, "$.usage.output_tokens_details.thinking_tokens", "must be between zero and output_tokens")
		}
	}
	if jsonValuePresent(source.Usage.ServerToolUse) {
		var usage map[string]json.RawMessage
		if err := json.Unmarshal(source.Usage.ServerToolUse, &usage); err != nil || usage == nil {
			return upstreamResponseError(ProtocolMessages, "$.usage.server_tool_use", "must be an object")
		}
		for name, raw := range usage {
			if name != "web_search_requests" && name != "web_fetch_requests" {
				return upstreamResponseError(ProtocolMessages, "$.usage.server_tool_use."+name, "unknown server-tool usage field")
			}
			var count int64
			if err := json.Unmarshal(raw, &count); err != nil || count < 0 {
				return upstreamResponseError(ProtocolMessages, "$.usage.server_tool_use."+name, "must be a non-negative integer")
			}
		}
	}
	if source.Usage.ServiceTier != "" && source.Usage.ServiceTier != "standard" && source.Usage.ServiceTier != "priority" && source.Usage.ServiceTier != "batch" {
		return upstreamResponseError(ProtocolMessages, "$.usage.service_tier", "unsupported service tier %q", source.Usage.ServiceTier)
	}
	if jsonValuePresent(source.Container) {
		var container map[string]json.RawMessage
		if err := json.Unmarshal(source.Container, &container); err != nil || container == nil {
			return upstreamResponseError(ProtocolMessages, "$.container", "must be an object or null")
		}
	}
	if jsonValuePresent(source.StopDetails) {
		var details struct {
			Type     string `json:"type"`
			Category string `json:"category"`
		}
		if err := json.Unmarshal(source.StopDetails, &details); err != nil || details.Type != "refusal" || details.Category == "" {
			return upstreamResponseError(ProtocolMessages, "$.stop_details", "must be a refusal details object")
		}
		if source.StopReason != "refusal" {
			return upstreamResponseError(ProtocolMessages, "$.stop_details", "is only valid when stop_reason is refusal")
		}
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
