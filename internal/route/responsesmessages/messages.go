package responsesmessages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	messageswire "github.com/2218342221/RouteMorphSDK/internal/wire/messages"
)

type messagesRequest = messageswire.Request
type messagesMessage = messageswire.Message
type messagesBlock = messageswire.Block
type messagesTool = messageswire.Tool
type messagesThinking = messageswire.Thinking
type messagesOutputConfig = messageswire.OutputConfig
type messagesOutputTokensDetails = messageswire.OutputTokensDetails

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

func validateOpenAIReasoningEffortForMessages(protocol Protocol, path, effort string) error {
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max":
		return nil
	case "none", "minimal":
		return unsupported(protocol, path, "reasoning effort %q has no equivalent Messages output_config effort", effort)
	default:
		return invalid(protocol, path, "unsupported reasoning effort %q", effort)
	}
}

func decodeMessagesContent(raw json.RawMessage, path string) ([]portablePart, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
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
			sourcePath := fmt.Sprintf("%s[%d].source", path, i)
			switch block.Source.Type {
			case "base64":
				if block.Source.URL != "" || block.Source.FileID != "" || jsonValuePresent(block.Source.Content) {
					return nil, invalid(ProtocolMessages, sourcePath, "base64 source cannot contain url, file_id, or content")
				}
				if block.Source.Data == "" {
					return nil, invalid(ProtocolMessages, sourcePath+".data", "base64 source data is required")
				}
				if !validBase64(block.Source.Data) {
					return nil, invalid(ProtocolMessages, sourcePath+".data", "must be valid base64")
				}
				if block.Type == "image" && !validMessagesImageMediaType(block.Source.MediaType) {
					return nil, invalid(ProtocolMessages, sourcePath+".media_type", "unsupported image media type %q", block.Source.MediaType)
				}
				if block.Type == "document" && block.Source.MediaType != "application/pdf" {
					return nil, unsupported(ProtocolMessages, sourcePath+".media_type", "only base64 PDF documents are portable")
				}
			case "url":
				if block.Source.Data != "" || block.Source.MediaType != "" || block.Source.FileID != "" || jsonValuePresent(block.Source.Content) {
					return nil, invalid(ProtocolMessages, sourcePath, "URL source cannot contain data, media_type, file_id, or content")
				}
				if block.Source.URL == "" {
					return nil, invalid(ProtocolMessages, sourcePath+".url", "URL source is required")
				}
				if !validHTTPURL(block.Source.URL) {
					return nil, invalid(ProtocolMessages, sourcePath+".url", "must be an absolute HTTP(S) URL")
				}
				if inferred := mimeTypeFromURL(block.Source.URL); inferred != "" {
					if block.Type == "image" && !validMessagesImageMediaType(inferred) {
						return nil, unsupported(ProtocolMessages, sourcePath+".url", "URL extension is not a supported image type")
					}
					if block.Type == "document" && inferred != "application/pdf" {
						return nil, unsupported(ProtocolMessages, sourcePath+".url", "URL extension is not PDF")
					}
				}
			case "file":
				if block.Source.Data != "" || block.Source.MediaType != "" || block.Source.URL != "" || jsonValuePresent(block.Source.Content) {
					return nil, invalid(ProtocolMessages, sourcePath, "file source cannot contain data, media_type, url, or content")
				}
				if block.Source.FileID == "" {
					return nil, invalid(ProtocolMessages, sourcePath+".file_id", "file source requires file_id")
				}
				return nil, unsupported(ProtocolMessages, sourcePath+".file_id", "provider-scoped file IDs cannot be translated across APIs")
			default:
				return nil, unsupported(ProtocolMessages, sourcePath+".type", "source type %q is not portable", block.Source.Type)
			}
			kind := partImage
			if block.Type == "document" {
				kind = partFile
			}
			media := &portableMedia{MIMEType: block.Source.MediaType, Data: block.Source.Data, URL: block.Source.URL, FileID: block.Source.FileID}
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
	if block.Title != "" {
		return unsupported(ProtocolMessages, path+".title", "document titles have no exact Responses content-part equivalent")
	}
	if block.Context != "" {
		return unsupported(ProtocolMessages, path+".context", "document context has no Responses content-part equivalent")
	}
	if jsonValuePresent(block.Transformations) {
		return unsupported(ProtocolMessages, path+".transformations", "image transformations require a native Messages provider")
	}
	if err := validateDirectMessagesCaller(block.Caller, path+".caller"); err != nil {
		return err
	}
	if block.ToolsetName != "" {
		return unsupported(ProtocolMessages, path+".toolset_name", "toolset membership has no portable cross-protocol equivalent")
	}
	return nil
}

func validMessagesImageMediaType(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func messagesRawNonNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func validateDirectMessagesCaller(raw json.RawMessage, path string) error {
	if !messagesRawNonNull(raw) {
		return nil
	}
	var caller map[string]json.RawMessage
	if err := json.Unmarshal(raw, &caller); err != nil || caller == nil {
		return invalid(ProtocolMessages, path, "caller must be an object")
	}
	var kind string
	if err := json.Unmarshal(caller["type"], &kind); err != nil || kind == "" {
		return invalid(ProtocolMessages, path+".type", "caller type is required")
	}
	if kind != "direct" {
		return unsupported(ProtocolMessages, path+".type", "programmatic caller %q cannot be represented by Responses", kind)
	}
	for field := range caller {
		if field != "type" {
			return unsupported(ProtocolMessages, path+"."+field, "direct caller field is not portable")
		}
	}
	return nil
}

func messagesSafetyIdentifier(metadata map[string]string, path string) (string, error) {
	for key := range metadata {
		if key != "user_id" {
			return "", unsupported(ProtocolMessages, path+"."+key, "Messages metadata field has no OpenAI safety-identifier equivalent")
		}
	}
	value := metadata["user_id"]
	if utf8.RuneCountInString(value) > 64 {
		return "", unsupported(ProtocolMessages, path+".user_id", "OpenAI safety_identifier is limited to 64 characters")
	}
	return value, nil
}

func decodeOpenAISafetyIdentifier(raw json.RawMessage, path string) (string, error) {
	if !jsonValuePresent(raw) {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", invalid(ProtocolResponses, path, "must be a string")
	}
	if utf8.RuneCountInString(value) > 64 {
		return "", invalid(ProtocolResponses, path, "must not exceed 64 characters")
	}
	return value, nil
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
				return nil, unsupported(ProtocolMessages, "$.messages.content", "provider-scoped file IDs cannot be translated across APIs")
			}
			if part.Media.Filename != "" {
				return nil, unsupported(ProtocolMessages, "$.messages.content", "file names cannot be represented by Messages content blocks")
			}
			blockType := "image"
			if part.Kind == partFile {
				blockType = "document"
			}
			if part.Media.Data != "" {
				if blockType == "image" && !validMessagesImageMediaType(part.Media.MIMEType) {
					return nil, unsupported(ProtocolMessages, "$.messages.content", "Messages base64 images require JPEG, PNG, GIF, or WebP media types")
				}
				if blockType == "document" && part.Media.MIMEType != "application/pdf" {
					return nil, unsupported(ProtocolMessages, "$.messages.content", "Messages base64 documents require application/pdf")
				}
			} else if blockType == "document" {
				if !validHTTPURL(part.Media.URL) {
					return nil, invalid(ProtocolResponses, "$.input", "file_url must be an absolute HTTP(S) URL")
				}
				if mimeTypeFromURL(part.Media.URL) != "application/pdf" {
					return nil, unsupported(ProtocolMessages, "$.messages.content", "Messages URL documents require a URL whose PDF MIME type can be determined")
				}
			}
			sourceType := "base64"
			if part.Media.URL != "" {
				sourceType = "url"
			}
			block := messagesBlock{Type: blockType}
			block.Source = &struct {
				Type      string          `json:"type"`
				MediaType string          `json:"media_type,omitempty"`
				Data      string          `json:"data,omitempty"`
				URL       string          `json:"url,omitempty"`
				FileID    string          `json:"file_id,omitempty"`
				Content   json.RawMessage `json:"content,omitempty"`
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
	if err := validateMessagesServerToolUsage(source.Usage.ServerToolUse); err != nil {
		return err
	}
	if source.Usage.ServiceTier != "" && source.Usage.ServiceTier != "standard" && source.Usage.ServiceTier != "priority" && source.Usage.ServiceTier != "batch" {
		return upstreamResponseError(ProtocolMessages, "$.usage.service_tier", "unsupported service tier %q", source.Usage.ServiceTier)
	}
	if err := validateMessagesResponseState(source); err != nil {
		return err
	}
	if _, err := parseMessagesFinish(source.StopReason); err != nil {
		return err
	}
	if source.StopReason == "stop_sequence" && source.StopSequence == "" {
		return upstreamResponseError(ProtocolMessages, "$.stop_sequence", "stop_sequence is required when stop_reason is stop_sequence")
	}
	return nil
}

func validateMessagesServerToolUsage(raw json.RawMessage) error {
	if !jsonValuePresent(raw) {
		return nil
	}
	var usage map[string]json.RawMessage
	if err := json.Unmarshal(raw, &usage); err != nil || usage == nil {
		return upstreamResponseError(ProtocolMessages, "$.usage.server_tool_use", "must be an object")
	}
	for name, value := range usage {
		if name != "web_search_requests" && name != "web_fetch_requests" {
			return upstreamResponseError(ProtocolMessages, "$.usage.server_tool_use."+name, "unknown server-tool usage field")
		}
		var count int64
		if err := json.Unmarshal(value, &count); err != nil || count < 0 {
			return upstreamResponseError(ProtocolMessages, "$.usage.server_tool_use."+name, "must be a non-negative integer")
		}
	}
	return nil
}

func validateMessagesResponseState(source messagesResponse) error {
	if messagesRawNonNull(source.Container) {
		var value map[string]json.RawMessage
		if err := json.Unmarshal(source.Container, &value); err != nil || value == nil {
			return upstreamResponseError(ProtocolMessages, "$.container", "must be an object or null")
		}
	}
	if messagesRawNonNull(source.StopDetails) {
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
	return nil
}

func messagesResponseExtensionDiagnostics(source messagesResponse, policy lossPolicy) ([]Diagnostic, error) {
	var diagnostics []Diagnostic
	for _, field := range []struct {
		present bool
		path    string
		code    string
		message string
	}{
		{messagesRawNonNull(source.Container), "$.container", "container_not_representable", "Messages container state was omitted"},
		{messagesRawNonNull(source.StopDetails), "$.stop_details", "stop_details_not_representable", "structured Messages refusal details were omitted"},
		{source.Usage.InferenceGeo != "", "$.usage.inference_geo", "inference_geo_not_representable", "Messages inference geography was omitted"},
		{source.Usage.ServiceTier != "", "$.usage.service_tier", "service_tier_not_representable", "Messages service tier was omitted"},
	} {
		if !field.present {
			continue
		}
		if policy == rejectSemanticLoss {
			return diagnostics, unsupported(ProtocolMessages, field.path, "%s", field.message)
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", field.code, field.path, field.message)
	}
	return diagnostics, nil
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

func joinText(parts []portablePart) string {
	var builder strings.Builder
	for _, part := range parts {
		if part.Kind == partText {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}
