package chatmessages

import (
	"bytes"
	"encoding/json"
	"fmt"

	chatwire "github.com/2218342221/RouteMorphSDK/internal/wire/chat"
)

type chatRequest = chatwire.Request
type chatMessage = chatwire.Message
type chatContentPart = chatwire.ContentPart
type chatTool = chatwire.Tool
type chatToolCall = chatwire.ToolCall

func decodeChatMessage(source chatMessage, index int) (portableMessage, error) {
	path := fmt.Sprintf("$.messages[%d]", index)
	return decodeChatMessageAtPath(source, path)
}

func decodeChatMessageAtPath(source chatMessage, path string) (portableMessage, error) {
	if err := validateChatMessageMetadata(source, path); err != nil {
		return portableMessage{}, err
	}
	message := portableMessage{Role: semanticRole(source.Role), Name: source.Name}
	switch message.Role {
	case roleSystem, roleDeveloper, roleUser, roleAssistant, roleTool:
	default:
		return portableMessage{}, invalid(ProtocolChat, path+".role", "unsupported role %q", source.Role)
	}
	if message.Role == roleTool {
		if source.ToolCallID == "" {
			return portableMessage{}, invalid(ProtocolChat, path+".tool_call_id", "tool_call_id is required")
		}
		content, err := decodeChatContent(source.Content, path+".content")
		if err != nil {
			return portableMessage{}, err
		}
		if err := validateChatContentRole(source.Role, content, path+".content"); err != nil {
			return portableMessage{}, err
		}
		message.Parts = []portablePart{{Kind: partToolResult, ToolResult: &portableToolResult{CallID: source.ToolCallID, Name: source.Name, Content: content}}}
		return message, nil
	}
	parts, err := decodeChatContent(source.Content, path+".content")
	if err != nil {
		return portableMessage{}, err
	}
	if err := validateChatContentRole(source.Role, parts, path+".content"); err != nil {
		return portableMessage{}, err
	}
	message.Parts = append(message.Parts, parts...)
	if source.ReasoningContent != "" {
		message.Parts = append(message.Parts, portablePart{Kind: partReasoning, Text: source.ReasoningContent})
	}
	if source.Refusal != "" {
		message.Parts = append(message.Parts, portablePart{Kind: partRefusal, Text: source.Refusal})
	}
	for j, toolCall := range source.ToolCalls {
		if toolCall.Type != "" && toolCall.Type != "function" {
			return portableMessage{}, unsupported(ProtocolChat, fmt.Sprintf("%s.tool_calls[%d].type", path, j), "tool call type %q is not portable", toolCall.Type)
		}
		if toolCall.ID == "" || toolCall.Function.Name == "" {
			return portableMessage{}, invalid(ProtocolChat, fmt.Sprintf("%s.tool_calls[%d]", path, j), "tool call id and function name are required")
		}
		arguments, err := normalizeOpenAIToolArguments(ProtocolChat, fmt.Sprintf("%s.tool_calls[%d].function.arguments", path, j), toolCall.Function.Arguments)
		if err != nil {
			return portableMessage{}, err
		}
		message.Parts = append(message.Parts, portablePart{Kind: partToolCall, ToolCall: &portableToolCall{ID: toolCall.ID, Name: toolCall.Function.Name, Arguments: arguments}})
	}
	return message, nil
}

func validateChatMessageMetadata(source chatMessage, path string) error {
	if rawJSONValuePresent(source.FunctionCall) {
		return unsupported(ProtocolChat, path+".function_call", "deprecated function_call cannot be represented without semantic loss")
	}
	if source.Role == "assistant" {
		if source.ToolCallID != "" {
			return invalid(ProtocolChat, path+".tool_call_id", "tool_call_id is only valid on tool messages")
		}
		return nil
	}
	if source.ToolCallID != "" && source.Role != "tool" {
		return invalid(ProtocolChat, path+".tool_call_id", "tool_call_id is only valid on tool messages")
	}
	if len(source.ToolCalls) > 0 {
		return invalid(ProtocolChat, path+".tool_calls", "tool_calls are only valid on assistant messages")
	}
	if source.Refusal != "" {
		return invalid(ProtocolChat, path+".refusal", "refusal is only valid on assistant messages")
	}
	if source.ReasoningContent != "" {
		return invalid(ProtocolChat, path+".reasoning_content", "reasoning_content is only valid on assistant messages")
	}
	return nil
}

func validateChatContentRole(role string, parts []portablePart, path string) error {
	if role == "user" {
		return nil
	}
	for index, part := range parts {
		if part.Kind != partText {
			return unsupported(ProtocolChat, fmt.Sprintf("%s[%d]", path, index), "Chat %s messages only support text content across protocols", role)
		}
	}
	return nil
}

func decodeChatContent(raw json.RawMessage, path string) ([]portablePart, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, invalid(ProtocolChat, path, "invalid text: %v", err)
		}
		return textParts(text), nil
	}
	var source []chatContentPart
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, invalid(ProtocolChat, path, "content must be a string or array: %v", err)
	}
	parts := make([]portablePart, 0, len(source))
	for i, part := range source {
		if rawJSONValuePresent(part.PromptCacheBreakpoint) {
			return nil, unsupported(ProtocolChat, fmt.Sprintf("%s[%d].prompt_cache_breakpoint", path, i), "OpenAI prompt cache breakpoints have no exact Messages cache-control equivalent")
		}
		switch part.Type {
		case "text":
			parts = append(parts, portablePart{Kind: partText, Text: part.Text})
		case "image_url":
			var image struct {
				URL    string `json:"url"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(part.ImageURL, &image); err != nil {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].image_url", path, i), "must be an image-url object")
			}
			value, detail := image.URL, image.Detail
			if value == "" {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].image_url", path, i), "image URL is required")
			}
			if detail != "" && detail != "auto" && detail != "low" && detail != "high" {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].image_url.detail", path, i), "detail must be auto, low, or high")
			}
			media, err := parseDataURL(value)
			if err != nil {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].image_url", path, i), "%v", err)
			}
			if media.Data != "" && !validImageMIMEType(media.MIMEType) {
				return nil, unsupported(ProtocolChat, fmt.Sprintf("%s[%d].image_url.url", path, i), "Chat image data URLs require JPEG, PNG, GIF, or WebP")
			}
			media.Detail = detail
			parts = append(parts, portablePart{Kind: partImage, Media: media})
		case "input_audio":
			if part.InputAudio == nil {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].input_audio", path, i), "input_audio is required")
			}
			if !validBase64(part.InputAudio.Data) {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].input_audio.data", path, i), "must be non-empty standard base64")
			}
			mimeType, ok := chatAudioMIMEType(part.InputAudio.Format)
			if !ok {
				return nil, unsupported(ProtocolChat, fmt.Sprintf("%s[%d].input_audio.format", path, i), "only wav and mp3 input audio are portable")
			}
			parts = append(parts, portablePart{Kind: partAudio, Media: &portableMedia{Data: part.InputAudio.Data, MIMEType: mimeType}})
		case "file":
			if part.File == nil {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].file", path, i), "file is required")
			}
			if (part.File.FileID == "") == (part.File.FileData == "") {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].file", path, i), "exactly one of file_id or file_data is required")
			}
			media := &portableMedia{FileID: part.File.FileID, Filename: part.File.Filename}
			if part.File.FileData != "" {
				var err error
				media, err = parseFileData(part.File.FileData, part.File.Filename)
				if err != nil {
					return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].file.file_data", path, i), "%v", err)
				}
			}
			parts = append(parts, portablePart{Kind: partFile, Media: media})
		default:
			return nil, unsupported(ProtocolChat, fmt.Sprintf("%s[%d].type", path, i), "content part %q is not portable", part.Type)
		}
	}
	return parts, nil
}

func encodeChatMessage(message portableMessage, index int) ([]chatMessage, error) {
	path := fmt.Sprintf("$.messages[%d]", index)
	if message.Role == roleTool && len(message.Parts) == 1 && message.Parts[0].ToolResult != nil {
		result := message.Parts[0].ToolResult
		if err := validateChatContentRole("tool", result.Content, path+".content"); err != nil {
			return nil, err
		}
		content, err := encodeChatContent(result.Content)
		if err != nil {
			return nil, err
		}
		return []chatMessage{{Role: "tool", ToolCallID: result.CallID, Content: content}}, nil
	}
	converted := chatMessage{Role: string(message.Role), Name: message.Name}
	var content []portablePart
	for _, part := range message.Parts {
		switch part.Kind {
		case partToolCall:
			if part.ToolCall == nil {
				return nil, invalid(ProtocolChat, path, "nil tool call")
			}
			var call chatToolCall
			call.ID = part.ToolCall.ID
			call.Type = "function"
			call.Function.Name = part.ToolCall.Name
			call.Function.Arguments, _ = json.Marshal(string(part.ToolCall.Arguments))
			converted.ToolCalls = append(converted.ToolCalls, call)
		case partReasoning:
			converted.ReasoningContent += part.Text
		case partRefusal:
			converted.Refusal += part.Text
		case partToolResult:
			return nil, unsupported(ProtocolChat, path, "tool results must be standalone tool messages")
		default:
			content = append(content, part)
		}
	}
	if err := validateChatContentRole(string(message.Role), content, path+".content"); err != nil {
		return nil, err
	}
	encoded, err := encodeChatContent(content)
	if err != nil {
		return nil, err
	}
	converted.Content = encoded
	return []chatMessage{converted}, nil
}

func encodeChatContent(parts []portablePart) (json.RawMessage, error) {
	if len(parts) == 0 {
		return json.RawMessage(`null`), nil
	}
	if len(parts) == 1 && parts[0].Kind == partText {
		return json.Marshal(parts[0].Text)
	}
	converted := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Kind {
		case partText:
			converted = append(converted, map[string]any{"type": "text", "text": part.Text})
		case partImage:
			if part.Media == nil {
				return nil, invalid(ProtocolChat, "$.messages.content", "nil image")
			}
			if part.Media.URL == "" && part.Media.Data == "" {
				return nil, unsupported(ProtocolChat, "$.messages.content.image_url", "file-id-only images cannot be represented by Chat image_url")
			}
			if part.Media.Detail != "" && part.Media.Detail != "auto" && part.Media.Detail != "low" && part.Media.Detail != "high" {
				return nil, unsupported(ProtocolChat, "$.messages.content.image_url.detail", "Chat image detail supports only auto, low, or high")
			}
			image := map[string]any{"url": dataURL(part.Media)}
			if part.Media != nil && part.Media.Detail != "" {
				image["detail"] = part.Media.Detail
			}
			converted = append(converted, map[string]any{"type": "image_url", "image_url": image})
		case partAudio:
			if part.Media == nil || part.Media.Data == "" {
				return nil, unsupported(ProtocolChat, "$.messages.content", "chat audio requires inline data")
			}
			format, ok := chatAudioFormat(part.Media.MIMEType)
			if !ok {
				return nil, unsupported(ProtocolChat, "$.messages.content.input_audio", "Chat input audio supports only WAV and MP3")
			}
			converted = append(converted, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": part.Media.Data, "format": format}})
		case partFile:
			if part.Media == nil {
				return nil, invalid(ProtocolChat, "$.messages.content", "nil file")
			}
			if part.Media.URL != "" {
				return nil, unsupported(ProtocolChat, "$.messages.content.file", "file URLs cannot be represented by Chat file content")
			}
			if part.Media.Detail != "" {
				return nil, unsupported(ProtocolChat, "$.messages.content.file", "file detail cannot be represented by Chat file content")
			}
			if part.Media.FileID == "" && part.Media.Data == "" {
				return nil, invalid(ProtocolChat, "$.messages.content.file", "file_id or file_data is required")
			}
			file := map[string]any{}
			if part.Media.FileID != "" {
				file["file_id"] = part.Media.FileID
			} else {
				file["file_data"] = openAIFileData(part.Media)
				if part.Media.Filename != "" {
					file["filename"] = part.Media.Filename
				}
			}
			converted = append(converted, map[string]any{"type": "file", "file": file})
		default:
			return nil, unsupported(ProtocolChat, "$.messages.content", "part %q cannot be encoded as chat content", part.Kind)
		}
	}
	return json.Marshal(converted)
}

func decodeStop(protocol Protocol, raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, invalid(protocol, "$.stop", "invalid stop value")
		}
		return []string{value}, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, invalid(protocol, "$.stop", "stop must be a string or string array")
	}
	return values, nil
}

func decodeChatToolChoice(raw json.RawMessage) (toolChoice, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return toolChoice{}, nil
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "invalid string choice")
		}
		if value != string(toolChoiceAuto) && value != string(toolChoiceNone) && value != string(toolChoiceRequired) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "unknown tool choice %q", value)
		}
		return toolChoice{Mode: toolChoiceMode(value)}, nil
	}
	fields, err := rejectUnknownObjectFields(ProtocolChat, trimmed, "$.tool_choice", "type", "function", "custom", "allowed_tools")
	if err != nil {
		return toolChoice{}, err
	}
	var kind string
	if err := json.Unmarshal(fields["type"], &kind); err != nil || kind == "" {
		return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.type", "non-empty string is required")
	}
	switch kind {
	case "function":
		if jsonValuePresent(fields["custom"]) || jsonValuePresent(fields["allowed_tools"]) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "function choice contains fields for another choice type")
		}
		function, err := rejectUnknownObjectFields(ProtocolChat, fields["function"], "$.tool_choice.function", "name")
		if err != nil {
			return toolChoice{}, err
		}
		var name string
		if err := json.Unmarshal(function["name"], &name); err != nil || name == "" {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.function.name", "non-empty string is required")
		}
		return toolChoice{Mode: toolChoiceNamed, Name: name}, nil
	case "allowed_tools":
		if jsonValuePresent(fields["function"]) || jsonValuePresent(fields["custom"]) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "allowed_tools choice contains fields for another choice type")
		}
		allowed, err := rejectUnknownObjectFields(ProtocolChat, fields["allowed_tools"], "$.tool_choice.allowed_tools", "mode", "tools")
		if err != nil {
			return toolChoice{}, err
		}
		var mode string
		if err := json.Unmarshal(allowed["mode"], &mode); err != nil || (mode != string(toolChoiceAuto) && mode != string(toolChoiceRequired)) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.allowed_tools.mode", "must be %q or %q", toolChoiceAuto, toolChoiceRequired)
		}
		var references []json.RawMessage
		if err := json.Unmarshal(allowed["tools"], &references); err != nil || len(references) == 0 {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.allowed_tools.tools", "non-empty array is required")
		}
		names := make([]string, 0, len(references))
		seen := make(map[string]struct{}, len(references))
		for index, reference := range references {
			path := fmt.Sprintf("$.tool_choice.allowed_tools.tools[%d]", index)
			item, err := rejectUnknownObjectFields(ProtocolChat, reference, path, "type", "function", "custom")
			if err != nil {
				return toolChoice{}, err
			}
			var itemType string
			if err := json.Unmarshal(item["type"], &itemType); err != nil || itemType == "" {
				return toolChoice{}, invalid(ProtocolChat, path+".type", "non-empty string is required")
			}
			if itemType != "function" {
				return toolChoice{}, unsupported(ProtocolChat, path+".type", "allowed tool type %q has no Messages equivalent", itemType)
			}
			if jsonValuePresent(item["custom"]) {
				return toolChoice{}, invalid(ProtocolChat, path+".custom", "is not valid for a function reference")
			}
			function, err := rejectUnknownObjectFields(ProtocolChat, item["function"], path+".function", "name")
			if err != nil {
				return toolChoice{}, err
			}
			var name string
			if err := json.Unmarshal(function["name"], &name); err != nil || name == "" {
				return toolChoice{}, invalid(ProtocolChat, path+".function.name", "non-empty string is required")
			}
			if _, duplicate := seen[name]; duplicate {
				return toolChoice{}, invalid(ProtocolChat, path+".function.name", "duplicate allowed function %q", name)
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		return toolChoice{Mode: toolChoiceAllowed, AllowedMode: toolChoiceMode(mode), AllowedNames: names}, nil
	case "custom":
		if jsonValuePresent(fields["function"]) || jsonValuePresent(fields["allowed_tools"]) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "custom choice contains fields for another choice type")
		}
		if !jsonValuePresent(fields["custom"]) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.custom", "is required")
		}
		custom, err := rejectUnknownObjectFields(ProtocolChat, fields["custom"], "$.tool_choice.custom", "name")
		if err != nil {
			return toolChoice{}, err
		}
		var name string
		if err := json.Unmarshal(custom["name"], &name); err != nil || name == "" {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.custom.name", "non-empty string is required")
		}
		return toolChoice{}, unsupported(ProtocolChat, "$.tool_choice.type", "custom tool choice has no Messages equivalent")
	default:
		return toolChoice{}, unsupported(ProtocolChat, "$.tool_choice.type", "tool choice type %q has no Messages equivalent", kind)
	}
}

func encodeChatToolChoice(choice toolChoice) json.RawMessage {
	if choice.Mode == toolChoiceNamed {
		data, _ := json.Marshal(map[string]any{"type": "function", "function": map[string]any{"name": choice.Name}})
		return data
	}
	data, _ := json.Marshal(string(choice.Mode))
	return data
}

func decodeChatResponseFormat(raw json.RawMessage) (*jsonSchemaFormat, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Schema      json.RawMessage `json:"schema"`
			Strict      *bool           `json:"strict"`
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, invalid(ProtocolChat, "$.response_format", "invalid response format")
	}
	if value.Type == "text" {
		return nil, nil
	}
	if value.Type != "json_schema" {
		return nil, unsupported(ProtocolChat, "$.response_format.type", "format %q is not losslessly portable", value.Type)
	}
	return &jsonSchemaFormat{Name: value.JSONSchema.Name, Description: value.JSONSchema.Description, Schema: value.JSONSchema.Schema, Strict: value.JSONSchema.Strict}, nil
}

type chatResponse = chatwire.Response
type chatResponseChoice = chatwire.ResponseChoice

func parseChatFinish(value string) (finishReason, error) {
	switch value {
	case "length", "max_tokens":
		return finishLength, nil
	case "tool_calls", "function_call":
		return finishToolCalls, nil
	case "content_filter":
		return finishContentFilter, nil
	case "stop":
		return finishStop, nil
	default:
		return "", upstreamResponseError(ProtocolChat, "$.choices[0].finish_reason", "unsupported finish reason %q", value)
	}
}
