package chatgemini

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
			return nil, unsupported(ProtocolChat, fmt.Sprintf("%s[%d].prompt_cache_breakpoint", path, i), "prompt cache breakpoints have no Gemini equivalent")
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
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(trimmed, &discriminator); err != nil {
		return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "must be a string or object")
	}
	if discriminator.Type == "allowed_tools" {
		fields, err := rejectUnknownObjectFields(ProtocolChat, trimmed, "$.tool_choice", "type", "allowed_tools")
		if err != nil {
			return toolChoice{}, err
		}
		allowedFields, err := rejectUnknownObjectFields(ProtocolChat, fields["allowed_tools"], "$.tool_choice.allowed_tools", "mode", "tools")
		if err != nil {
			return toolChoice{}, err
		}
		var mode string
		if err := json.Unmarshal(allowedFields["mode"], &mode); err != nil || mode == "" {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.allowed_tools.mode", "mode is required")
		}
		if mode != string(toolChoiceRequired) && mode != string(toolChoiceAuto) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.allowed_tools.mode", "must be %q or %q", toolChoiceAuto, toolChoiceRequired)
		}
		var tools []json.RawMessage
		if err := json.Unmarshal(allowedFields["tools"], &tools); err != nil || len(tools) == 0 {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.allowed_tools.tools", "at least one function reference is required")
		}
		names := make([]string, 0, len(tools))
		seen := make(map[string]struct{}, len(tools))
		for index, rawTool := range tools {
			itemPath := fmt.Sprintf("$.tool_choice.allowed_tools.tools[%d]", index)
			item, err := rejectUnknownObjectFields(ProtocolChat, rawTool, itemPath, "type", "function")
			if err != nil {
				return toolChoice{}, err
			}
			var kind string
			if err := json.Unmarshal(item["type"], &kind); err != nil || kind != "function" {
				return toolChoice{}, unsupported(ProtocolChat, itemPath+".type", "only function references are portable to Gemini")
			}
			function, err := rejectUnknownObjectFields(ProtocolChat, item["function"], itemPath+".function", "name")
			if err != nil {
				return toolChoice{}, err
			}
			var name string
			if err := json.Unmarshal(function["name"], &name); err != nil || name == "" {
				return toolChoice{}, invalid(ProtocolChat, itemPath+".function.name", "name is required")
			}
			if _, duplicate := seen[name]; duplicate {
				return toolChoice{}, invalid(ProtocolChat, itemPath+".function.name", "duplicate allowed function %q", name)
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		return toolChoice{Mode: toolChoiceAllowed, AllowedMode: toolChoiceMode(mode), AllowedNames: names}, nil
	}
	fields, err := rejectUnknownObjectFields(ProtocolChat, trimmed, "$.tool_choice", "type", "function", "custom", "allowed_tools")
	if err != nil {
		return toolChoice{}, err
	}
	var kind string
	if err := json.Unmarshal(fields["type"], &kind); err != nil || kind == "" {
		return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.type", "non-empty string is required")
	}
	if kind != "function" {
		if kind == "custom" {
			if jsonValuePresent(fields["function"]) || jsonValuePresent(fields["allowed_tools"]) {
				return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "custom choice contains fields for another choice type")
			}
			custom, err := rejectUnknownObjectFields(ProtocolChat, fields["custom"], "$.tool_choice.custom", "name")
			if err != nil {
				return toolChoice{}, err
			}
			var name string
			if err := json.Unmarshal(custom["name"], &name); err != nil || name == "" {
				return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.custom.name", "non-empty string is required")
			}
		}
		return toolChoice{}, unsupported(ProtocolChat, "$.tool_choice.type", "tool choice type %q has no Gemini equivalent", kind)
	}
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
}

func encodeChatToolChoice(choice toolChoice) json.RawMessage {
	if choice.Mode == toolChoiceAllowed {
		tools := make([]any, 0, len(choice.AllowedNames))
		for _, name := range choice.AllowedNames {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": name}})
		}
		data, _ := json.Marshal(map[string]any{"type": "allowed_tools", "allowed_tools": map[string]any{"mode": string(choice.AllowedMode), "tools": tools}})
		return data
	}
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
