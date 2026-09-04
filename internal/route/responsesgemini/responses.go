package responsesgemini

import (
	"encoding/json"
	"fmt"

	responseswire "github.com/2218342221/RouteMorphSDK/internal/wire/responses"
)

type responsesRequest = responseswire.Request
type responsesTool = responseswire.Tool
type responsesItem = responseswire.Item
type responsesContentPart = responseswire.ContentPart

func decodeResponsesInstructions(raw json.RawMessage) ([]portablePart, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		return textParts(rawString(raw)), nil
	}
	var parts []responsesContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, unsupported(ProtocolResponses, "$.instructions", "only string or text-part instructions are portable")
	}
	return decodeResponsesContent(parts, "$.instructions", true)
}

func decodeResponsesContentRaw(raw json.RawMessage, path string, input bool) ([]portablePart, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		return textParts(rawString(raw)), nil
	}
	var source []responsesContentPart
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, invalid(ProtocolResponses, path, "content must be string or array: %v", err)
	}
	return decodeResponsesContent(source, path, input)
}

func decodeResponsesContent(source []responsesContentPart, path string, input bool) ([]portablePart, error) {
	parts := make([]portablePart, 0, len(source))
	for i, part := range source {
		if len(part.PromptCacheBreakpoint) > 0 && string(part.PromptCacheBreakpoint) != "null" {
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].prompt_cache_breakpoint", path, i), "prompt cache breakpoints have no portable cross-protocol equivalent")
		}
		if len(part.Annotations) > 0 && string(part.Annotations) != "null" && string(part.Annotations) != "[]" {
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].annotations", path, i), "output annotations cannot be represented cross-protocol")
		}
		if len(part.Logprobs) > 0 && string(part.Logprobs) != "null" && string(part.Logprobs) != "[]" {
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].logprobs", path, i), "output log probabilities cannot be represented cross-protocol")
		}
		switch part.Type {
		case "input_text", "output_text", "summary_text", "reasoning_text", "text":
			parts = append(parts, portablePart{Kind: partText, Text: part.Text})
		case "refusal":
			parts = append(parts, portablePart{Kind: partRefusal, Text: part.Refusal})
		case "input_image":
			media := parseDataURL(part.ImageURL)
			media.FileID = part.FileID
			media.Detail = part.Detail
			parts = append(parts, portablePart{Kind: partImage, Media: media})
		case "input_file":
			parts = append(parts, portablePart{Kind: partFile, Media: &portableMedia{FileID: part.FileID, URL: part.FileURL, Data: part.FileData, Filename: part.Filename}})
		case "input_audio":
			var audio struct {
				Data   string `json:"data"`
				Format string `json:"format"`
			}
			if len(part.InputAudio) == 0 || json.Unmarshal(part.InputAudio, &audio) != nil || audio.Data == "" || audio.Format == "" {
				return nil, invalid(ProtocolResponses, fmt.Sprintf("%s[%d].input_audio", path, i), "input_audio requires data and format")
			}
			parts = append(parts, portablePart{Kind: partAudio, Media: &portableMedia{Data: audio.Data, MIMEType: "audio/" + audio.Format}})
		default:
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].type", path, i), "content part %q is not portable", part.Type)
		}
	}
	return parts, nil
}

func encodeResponsesContent(parts []portablePart, input bool) ([]responsesContentPart, error) {
	converted := make([]responsesContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Kind {
		case partText:
			typeName := "input_text"
			annotations := json.RawMessage(nil)
			if !input {
				typeName = "output_text"
				annotations = json.RawMessage(`[]`)
			}
			converted = append(converted, responsesContentPart{Type: typeName, Text: part.Text, Annotations: annotations})
		case partRefusal:
			converted = append(converted, responsesContentPart{Type: "refusal", Refusal: part.Text})
		case partImage:
			if part.Media == nil {
				return nil, invalid(ProtocolResponses, "$.input", "nil image")
			}
			converted = append(converted, responsesContentPart{Type: "input_image", ImageURL: dataURL(part.Media), FileID: part.Media.FileID, Detail: part.Media.Detail})
		case partFile:
			if part.Media == nil {
				return nil, invalid(ProtocolResponses, "$.input", "nil file")
			}
			converted = append(converted, responsesContentPart{Type: "input_file", FileID: part.Media.FileID, FileURL: part.Media.URL, FileData: part.Media.Data, Filename: part.Media.Filename})
		case partAudio:
			if part.Media == nil || part.Media.Data == "" {
				return nil, unsupported(ProtocolResponses, "$.input", "Responses input audio requires inline data")
			}
			format := "wav"
			if part.Media.MIMEType == "audio/mp3" || part.Media.MIMEType == "audio/mpeg" {
				format = "mp3"
			}
			converted = append(converted, responsesContentPart{Type: "input_audio", InputAudio: mustJSON(map[string]any{"data": part.Media.Data, "format": format})})
		default:
			return nil, unsupported(ProtocolResponses, "$.input", "part %q cannot be encoded as message content", part.Kind)
		}
	}
	return converted, nil
}

func decodeResponsesToolChoice(raw json.RawMessage) (toolChoice, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return toolChoice{}, nil
	}
	if raw[0] == '"' {
		value := rawString(raw)
		if value != string(toolChoiceAuto) && value != string(toolChoiceNone) && value != string(toolChoiceRequired) {
			return toolChoice{}, unsupported(ProtocolResponses, "$.tool_choice", "tool choice %q is not portable", value)
		}
		return toolChoice{Mode: toolChoiceMode(value)}, nil
	}
	var value struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &value); err != nil || value.Type != "function" || value.Name == "" {
		return toolChoice{}, unsupported(ProtocolResponses, "$.tool_choice", "only named function choice is portable")
	}
	return toolChoice{Mode: toolChoiceNamed, Name: value.Name}, nil
}

func encodeResponsesToolChoice(choice toolChoice) json.RawMessage {
	if choice.Mode == toolChoiceNamed {
		data, _ := json.Marshal(map[string]any{"type": "function", "name": choice.Name})
		return data
	}
	data, _ := json.Marshal(string(choice.Mode))
	return data
}

func validateResponsesTool(tool responsesTool, path string) error {
	if tool.Type == "function" && tool.Name == "" {
		return invalid(ProtocolResponses, path+".name", "function name is required")
	}
	if tool.DeferLoading {
		return unsupported(ProtocolResponses, path+".defer_loading", "deferred tool loading requires a native Responses provider")
	}
	if len(tool.PromptCacheBreakpoint) > 0 && string(tool.PromptCacheBreakpoint) != "null" {
		return unsupported(ProtocolResponses, path+".prompt_cache_breakpoint", "tool prompt cache breakpoints require a native Responses provider")
	}
	return nil
}

func validateResponsesTools(tools []responsesTool, path string) error {
	for index, tool := range tools {
		if err := validateResponsesTool(tool, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
	}
	return nil
}

func validateResponsesItems(items []responsesItem, path string) error {
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if item.Phase != "" {
			if path != "$.output" || (item.Phase != "final_answer" && item.Phase != "commentary") {
				return unsupported(ProtocolResponses, itemPath+".phase", "message phase %q has no portable cross-protocol equivalent", item.Phase)
			}
		}
		if len(item.EncryptedContent) > 0 && string(item.EncryptedContent) != "null" {
			return unsupported(ProtocolResponses, itemPath+".encrypted_content", "encrypted reasoning content requires a native Responses provider")
		}
		switch item.Type {
		case "message", "":
			if path == "$.output" && item.Type == "" {
				return upstreamResponseError(ProtocolResponses, itemPath+".type", "output item type is required")
			}
			if path == "$.output" && item.Role != "assistant" {
				return upstreamResponseError(ProtocolResponses, itemPath+".role", "output message role must be assistant")
			}
			if item.Role != "user" && item.Role != "assistant" && item.Role != "system" && item.Role != "developer" {
				return invalid(ProtocolResponses, itemPath+".role", "unsupported message role %q", item.Role)
			}
		case "function_call":
			if item.CallID == "" || item.Name == "" {
				return invalid(ProtocolResponses, itemPath, "function_call requires call_id and name")
			}
		case "function_call_output":
			if path == "$.output" {
				return unsupported(ProtocolResponses, itemPath+".type", "function_call_output is not a valid response output item")
			}
			if item.CallID == "" {
				return invalid(ProtocolResponses, itemPath+".call_id", "call_id is required")
			}
		case "reasoning":
		default:
			return unsupported(ProtocolResponses, itemPath+".type", "item type %q is not supported by this cross-protocol route", item.Type)
		}
	}
	return nil
}

func responsesPhaseDiagnostics(items []responsesItem, path string) []Diagnostic {
	var diagnostics []Diagnostic
	for index, item := range items {
		if item.Phase != "" {
			diagnostics = appendDiagnostic(diagnostics, "warning", "responses_output_phase_not_representable", fmt.Sprintf("%s[%d].phase", path, index), fmt.Sprintf("output phase %q is not represented by the target protocol", item.Phase))
		}
	}
	return diagnostics
}

func decodeResponsesTextOptions(raw json.RawMessage) (*jsonSchemaFormat, json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var value struct {
		Verbosity json.RawMessage `json:"verbosity"`
		Format    struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Schema      json.RawMessage `json:"schema"`
			Strict      *bool           `json:"strict"`
		} `json:"format"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, nil, invalid(ProtocolResponses, "$.text", "invalid text format")
	}
	if value.Format.Type == "" || value.Format.Type == "text" {
		return nil, value.Verbosity, nil
	}
	if value.Format.Type != "json_schema" {
		return nil, nil, unsupported(ProtocolResponses, "$.text.format.type", "format %q is not losslessly portable", value.Format.Type)
	}
	return &jsonSchemaFormat{Name: value.Format.Name, Description: value.Format.Description, Schema: value.Format.Schema, Strict: value.Format.Strict}, value.Verbosity, nil
}

type responsesResponse = responseswire.Response

func validateResponsesTerminal(source responsesResponse) error {
	if source.Status == "failed" || source.Status == "cancelled" || source.Error != nil {
		message := "Responses generation failed"
		if source.Error != nil && source.Error.Message != "" {
			message = source.Error.Message
		}
		return upstreamResponseError(ProtocolResponses, "$.error", "%s", message)
	}
	if source.Status != "completed" && source.Status != "incomplete" {
		return upstreamResponseError(ProtocolResponses, "$.status", "unexpected terminal status %q", source.Status)
	}
	if source.Status == "incomplete" && source.IncompleteDetails != nil && source.IncompleteDetails.Reason != "" && source.IncompleteDetails.Reason != "max_output_tokens" && source.IncompleteDetails.Reason != "content_filter" {
		return upstreamResponseError(ProtocolResponses, "$.incomplete_details.reason", "unsupported incomplete reason %q", source.IncompleteDetails.Reason)
	}
	return validateResponsesItems(source.Output, "$.output")
}
