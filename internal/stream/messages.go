package stream

import (
	"bytes"
	"encoding/json"

	messageswire "github.com/2218342221/RouteMorphSDK/internal/wire/messages"
)

type messagesBlock = messageswire.Block

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
