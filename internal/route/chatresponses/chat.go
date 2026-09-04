package chatresponses

import (
	"encoding/json"
	"fmt"

	chatwire "github.com/2218342221/RouteMorphSDK/internal/wire/chat"
)

type chatRequest = chatwire.Request
type chatStreamOptions = chatwire.StreamOptions
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
		switch part.Type {
		case "text", "input_text":
			parts = append(parts, portablePart{Kind: partText, Text: part.Text})
		case "image_url", "input_image":
			var value string
			detail := ""
			if len(part.ImageURL) > 0 && part.ImageURL[0] == '"' {
				_ = json.Unmarshal(part.ImageURL, &value)
			} else {
				var image struct {
					URL    string `json:"url"`
					Detail string `json:"detail"`
				}
				_ = json.Unmarshal(part.ImageURL, &image)
				value = image.URL
				detail = image.Detail
			}
			media := parseDataURL(value)
			if value == "" {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].image_url", path, i), "image URL is required")
			}
			media.Detail = detail
			parts = append(parts, portablePart{Kind: partImage, Media: media})
		case "input_audio":
			if part.InputAudio == nil {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].input_audio", path, i), "input_audio is required")
			}
			parts = append(parts, portablePart{Kind: partAudio, Media: &portableMedia{Data: part.InputAudio.Data, MIMEType: "audio/" + part.InputAudio.Format}})
		case "file":
			if part.File == nil {
				return nil, invalid(ProtocolChat, fmt.Sprintf("%s[%d].file", path, i), "file is required")
			}
			parts = append(parts, portablePart{Kind: partFile, Media: &portableMedia{FileID: part.File.FileID, Data: part.File.FileData, Filename: part.File.Filename}})
		default:
			return nil, unsupported(ProtocolChat, fmt.Sprintf("%s[%d].type", path, i), "content part %q is not portable", part.Type)
		}
	}
	return parts, nil
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
			image := map[string]any{"url": dataURL(part.Media)}
			if part.Media != nil && part.Media.Detail != "" {
				image["detail"] = part.Media.Detail
			}
			converted = append(converted, map[string]any{"type": "image_url", "image_url": image})
		case partAudio:
			if part.Media == nil || part.Media.Data == "" {
				return nil, unsupported(ProtocolChat, "$.messages.content", "chat audio requires inline data")
			}
			format := "wav"
			if part.Media.MIMEType == "audio/mp3" || part.Media.MIMEType == "audio/mpeg" {
				format = "mp3"
			}
			converted = append(converted, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": part.Media.Data, "format": format}})
		case partFile:
			if part.Media == nil {
				return nil, invalid(ProtocolChat, "$.messages.content", "nil file")
			}
			if part.Media.URL != "" {
				return nil, unsupported(ProtocolChat, "$.messages.content.file", "file URLs cannot be represented by Chat file content")
			}
			if part.Media.FileID == "" && part.Media.Data == "" {
				return nil, invalid(ProtocolChat, "$.messages.content.file", "file_id or file_data is required")
			}
			converted = append(converted, map[string]any{"type": "file", "file": map[string]any{"file_id": part.Media.FileID, "file_data": part.Media.Data, "filename": part.Media.Filename}})
		default:
			return nil, unsupported(ProtocolChat, "$.messages.content", "part %q cannot be encoded as chat content", part.Kind)
		}
	}
	return json.Marshal(converted)
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
	var value struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &value); err != nil || value.Type != "function" || value.Function.Name == "" {
		return toolChoice{}, unsupported(ProtocolChat, "$.tool_choice", "only function tool choices are portable")
	}
	return toolChoice{Mode: toolChoiceNamed, Name: value.Function.Name}, nil
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
