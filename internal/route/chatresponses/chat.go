package chatresponses

import (
	"encoding/json"
	"fmt"
	"strings"

	chatwire "github.com/2218342221/RouteMorphSDK/internal/wire/chat"
)

type chatRequest = chatwire.Request
type chatStreamOptions = chatwire.StreamOptions
type chatMessage = chatwire.Message
type chatContentPart = chatwire.ContentPart
type chatTool = chatwire.Tool
type chatToolCall = chatwire.ToolCall
type chatCustomTool = chatwire.CustomTool
type chatCustomToolCall = chatwire.CustomToolCall

type portableTextFormat struct {
	Type        string
	Name        string
	Description string
	Schema      json.RawMessage
	Strict      *bool
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
		breakpointPath := fmt.Sprintf("%s[%d].prompt_cache_breakpoint", path, i)
		if err := validatePromptCacheBreakpoint(ProtocolChat, breakpointPath, part.PromptCacheBreakpoint); err != nil {
			return nil, err
		}
		converted := portablePart{PromptCacheBreakpoint: append(json.RawMessage(nil), part.PromptCacheBreakpoint...)}
		switch part.Type {
		case "text":
			converted.Kind, converted.Text = partText, part.Text
			parts = append(parts, converted)
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
			converted.Kind, converted.Media = partImage, media
			parts = append(parts, converted)
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
			if jsonValuePresent(part.PromptCacheBreakpoint) {
				return nil, unsupported(ProtocolChat, breakpointPath, "Responses input_audio has no prompt cache breakpoint")
			}
			converted.Kind, converted.Media = partAudio, &portableMedia{Data: part.InputAudio.Data, MIMEType: mimeType}
			parts = append(parts, converted)
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
			converted.Kind, converted.Media = partFile, media
			parts = append(parts, converted)
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
	if len(parts) == 1 && parts[0].Kind == partText && !jsonValuePresent(parts[0].PromptCacheBreakpoint) {
		return json.Marshal(parts[0].Text)
	}
	converted := make([]map[string]any, 0, len(parts))
	for index, part := range parts {
		if err := validatePromptCacheBreakpoint(ProtocolResponses, fmt.Sprintf("$.input.content[%d].prompt_cache_breakpoint", index), part.PromptCacheBreakpoint); err != nil {
			return nil, err
		}
		withBreakpoint := func(value map[string]any) map[string]any {
			if jsonValuePresent(part.PromptCacheBreakpoint) {
				value["prompt_cache_breakpoint"] = json.RawMessage(part.PromptCacheBreakpoint)
			}
			return value
		}
		switch part.Kind {
		case partText:
			converted = append(converted, withBreakpoint(map[string]any{"type": "text", "text": part.Text}))
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
			converted = append(converted, withBreakpoint(map[string]any{"type": "image_url", "image_url": image}))
		case partAudio:
			if part.Media == nil || part.Media.Data == "" {
				return nil, unsupported(ProtocolChat, "$.messages.content", "chat audio requires inline data")
			}
			if jsonValuePresent(part.PromptCacheBreakpoint) {
				return nil, unsupported(ProtocolResponses, fmt.Sprintf("$.input.content[%d].prompt_cache_breakpoint", index), "Responses input_audio has no prompt cache breakpoint")
			}
			format, ok := chatAudioFormat(part.Media.MIMEType)
			if !ok {
				return nil, unsupported(ProtocolChat, "$.messages.content.input_audio", "Chat input audio supports only WAV and MP3")
			}
			converted = append(converted, withBreakpoint(map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": part.Media.Data, "format": format}}))
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
			converted = append(converted, withBreakpoint(map[string]any{"type": "file", "file": file}))
		default:
			return nil, unsupported(ProtocolChat, "$.messages.content", "part %q cannot be encoded as chat content", part.Kind)
		}
	}
	return json.Marshal(converted)
}

func encodeChatResponseText(parts []portablePart, path string) (json.RawMessage, error) {
	if len(parts) == 0 {
		return json.RawMessage(`null`), nil
	}
	var text strings.Builder
	for index, part := range parts {
		if part.Kind != partText {
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].type", path, index), "Chat response content can only contain text")
		}
		if jsonValuePresent(part.PromptCacheBreakpoint) {
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].prompt_cache_breakpoint", path, index), "output prompt cache breakpoints have no Chat response equivalent")
		}
		text.WriteString(part.Text)
	}
	return json.Marshal(text.String())
}

func decodeChatToolChoice(raw json.RawMessage) (toolChoice, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return toolChoice{}, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "invalid string choice")
		}
		if value != string(toolChoiceAuto) && value != string(toolChoiceNone) && value != string(toolChoiceRequired) {
			return toolChoice{}, unsupported(ProtocolChat, "$.tool_choice", "tool choice %q is not portable", value)
		}
		return toolChoice{Mode: toolChoiceMode(value)}, nil
	}
	fields, err := rejectUnknownObjectFields(ProtocolChat, "$.tool_choice", raw, "type", "function", "custom")
	if err != nil {
		return toolChoice{}, err
	}
	var value struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		Custom struct {
			Name string `json:"name"`
		} `json:"custom"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "invalid named tool choice")
	}
	switch value.Type {
	case "function":
		if nonNullJSON(fields["custom"]) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.custom", "is not valid for a function choice")
		}
		if value.Function.Name == "" {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.function.name", "name is required")
		}
		return toolChoice{Mode: toolChoiceNamed, Name: value.Function.Name, Kind: "function"}, nil
	case "custom":
		if nonNullJSON(fields["function"]) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.function", "is not valid for a custom choice")
		}
		if value.Custom.Name == "" {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.custom.name", "name is required")
		}
		return toolChoice{Mode: toolChoiceNamed, Name: value.Custom.Name, Kind: "custom"}, nil
	default:
		return toolChoice{}, unsupported(ProtocolChat, "$.tool_choice.type", "named tool choice type %q is not portable", value.Type)
	}
}

func encodeChatToolChoice(choice toolChoice) json.RawMessage {
	if choice.Mode == toolChoiceNamed {
		kind := choice.Kind
		if kind == "" {
			kind = "function"
		}
		data, _ := json.Marshal(map[string]any{"type": kind, kind: map[string]any{"name": choice.Name}})
		return data
	}
	data, _ := json.Marshal(string(choice.Mode))
	return data
}

func decodeChatResponseFormat(raw json.RawMessage) (*portableTextFormat, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	fields, err := rejectUnknownObjectFields(ProtocolChat, "$.response_format", raw, "type", "json_schema")
	if err != nil {
		return nil, err
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
	if value.Type == "" {
		return nil, invalid(ProtocolChat, "$.response_format.type", "is required")
	}
	if value.Type == "text" {
		if jsonValuePresent(fields["json_schema"]) {
			return nil, invalid(ProtocolChat, "$.response_format.json_schema", "is only valid when type is json_schema")
		}
		return nil, nil
	}
	if value.Type == "json_object" {
		if jsonValuePresent(fields["json_schema"]) {
			return nil, invalid(ProtocolChat, "$.response_format.json_schema", "is only valid when type is json_schema")
		}
		return &portableTextFormat{Type: "json_object"}, nil
	}
	if value.Type != "json_schema" {
		return nil, unsupported(ProtocolChat, "$.response_format.type", "format %q is not losslessly portable", value.Type)
	}
	if value.JSONSchema.Name == "" || !jsonValuePresent(value.JSONSchema.Schema) {
		return nil, invalid(ProtocolChat, "$.response_format.json_schema", "name and schema are required")
	}
	if _, err := rejectUnknownObjectFields(ProtocolChat, "$.response_format.json_schema", fields["json_schema"], "name", "description", "schema", "strict"); err != nil {
		return nil, err
	}
	return &portableTextFormat{Type: "json_schema", Name: value.JSONSchema.Name, Description: value.JSONSchema.Description, Schema: value.JSONSchema.Schema, Strict: value.JSONSchema.Strict}, nil
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
