package responsesgemini

import (
	"bytes"
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
	if err := validateResponsesContentArray(ProtocolResponses, raw, "$.instructions"); err != nil {
		return nil, err
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
	if err := validateResponsesContentArray(ProtocolResponses, raw, path); err != nil {
		return nil, err
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
			if (part.ImageURL == "") == (part.FileID == "") {
				return nil, invalid(ProtocolResponses, fmt.Sprintf("%s[%d]", path, i), "input_image requires exactly one of image_url or file_id")
			}
			if part.Detail != "" && part.Detail != "auto" && part.Detail != "low" && part.Detail != "high" && part.Detail != "original" {
				return nil, invalid(ProtocolResponses, fmt.Sprintf("%s[%d].detail", path, i), "detail must be auto, low, high, or original")
			}
			media := &portableMedia{}
			if part.ImageURL != "" {
				var err error
				media, err = parseDataURL(part.ImageURL)
				if err != nil {
					return nil, invalid(ProtocolResponses, fmt.Sprintf("%s[%d].image_url", path, i), "%v", err)
				}
				if media.Data != "" && !validImageMIMEType(media.MIMEType) {
					return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].image_url", path, i), "Responses image data URLs require JPEG, PNG, GIF, or WebP")
				}
			}
			media.FileID = part.FileID
			media.Detail = part.Detail
			parts = append(parts, portablePart{Kind: partImage, Media: media})
		case "input_file":
			sources := 0
			for _, value := range []string{part.FileID, part.FileURL, part.FileData} {
				if value != "" {
					sources++
				}
			}
			if sources != 1 {
				return nil, invalid(ProtocolResponses, fmt.Sprintf("%s[%d]", path, i), "input_file requires exactly one of file_id, file_url, or file_data")
			}
			if part.Detail != "" && part.Detail != "auto" && part.Detail != "low" && part.Detail != "high" {
				return nil, invalid(ProtocolResponses, fmt.Sprintf("%s[%d].detail", path, i), "detail must be auto, low, or high")
			}
			media := &portableMedia{FileID: part.FileID, URL: part.FileURL, Filename: part.Filename, Detail: part.Detail}
			if part.FileURL != "" && !validHTTPURL(part.FileURL) {
				return nil, invalid(ProtocolResponses, fmt.Sprintf("%s[%d].file_url", path, i), "must be an absolute HTTP(S) URL")
			}
			if part.FileData != "" {
				var err error
				media, err = parseFileData(part.FileData, part.Filename)
				if err != nil {
					return nil, invalid(ProtocolResponses, fmt.Sprintf("%s[%d].file_data", path, i), "%v", err)
				}
				media.Detail = part.Detail
			}
			parts = append(parts, portablePart{Kind: partFile, Media: media})
		case "input_audio":
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].type", path, i), "input_audio is not part of the Responses Create message-content union")
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
			if (part.Media.FileID == "") == (part.Media.URL == "" && part.Media.Data == "") || (part.Media.URL != "" && part.Media.Data != "") {
				return nil, invalid(ProtocolResponses, "$.input", "input_image requires exactly one source")
			}
			converted = append(converted, responsesContentPart{Type: "input_image", ImageURL: dataURL(part.Media), FileID: part.Media.FileID, Detail: part.Media.Detail})
		case partFile:
			if part.Media == nil {
				return nil, invalid(ProtocolResponses, "$.input", "nil file")
			}
			sources := 0
			for _, value := range []string{part.Media.FileID, part.Media.URL, part.Media.Data} {
				if value != "" {
					sources++
				}
			}
			if sources != 1 {
				return nil, invalid(ProtocolResponses, "$.input", "input_file requires exactly one source")
			}
			converted = append(converted, responsesContentPart{Type: "input_file", FileID: part.Media.FileID, FileURL: part.Media.URL, FileData: openAIFileData(part.Media), Filename: part.Media.Filename})
		case partAudio:
			return nil, unsupported(ProtocolResponses, "$.input", "Responses Create message content has no input_audio part")
		default:
			return nil, unsupported(ProtocolResponses, "$.input", "part %q cannot be encoded as message content", part.Kind)
		}
	}
	return converted, nil
}

func decodeResponsesToolChoice(raw json.RawMessage) (toolChoice, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return toolChoice{}, nil
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice", "must be a string or object")
		}
		if value != string(toolChoiceAuto) && value != string(toolChoiceNone) && value != string(toolChoiceRequired) {
			return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice", "unknown tool choice %q", value)
		}
		return toolChoice{Mode: toolChoiceMode(value)}, nil
	}
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(trimmed, &discriminator); err != nil {
		return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice", "must be a string or object")
	}
	if discriminator.Type == "" {
		return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice.type", "non-empty string is required")
	}
	if discriminator.Type == "allowed_tools" {
		fields, err := rejectUnknownResponsesObject(ProtocolResponses, trimmed, "$.tool_choice", "type", "mode", "tools")
		if err != nil {
			return toolChoice{}, err
		}
		var mode string
		if err := json.Unmarshal(fields["mode"], &mode); err != nil || mode == "" {
			return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice.mode", "mode is required")
		}
		if mode != string(toolChoiceRequired) && mode != string(toolChoiceAuto) {
			return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice.mode", "must be %q or %q", toolChoiceAuto, toolChoiceRequired)
		}
		var tools []json.RawMessage
		if err := json.Unmarshal(fields["tools"], &tools); err != nil || len(tools) == 0 {
			return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice.tools", "at least one function reference is required")
		}
		names := make([]string, 0, len(tools))
		seen := make(map[string]struct{}, len(tools))
		for index, rawTool := range tools {
			itemPath := fmt.Sprintf("$.tool_choice.tools[%d]", index)
			item, err := rejectUnknownResponsesObject(ProtocolResponses, rawTool, itemPath, "type", "name")
			if err != nil {
				return toolChoice{}, err
			}
			var kind, name string
			if err := json.Unmarshal(item["type"], &kind); err != nil || kind != "function" {
				return toolChoice{}, unsupported(ProtocolResponses, itemPath+".type", "only function references are portable to Gemini")
			}
			if err := json.Unmarshal(item["name"], &name); err != nil || name == "" {
				return toolChoice{}, invalid(ProtocolResponses, itemPath+".name", "name is required")
			}
			if _, duplicate := seen[name]; duplicate {
				return toolChoice{}, invalid(ProtocolResponses, itemPath+".name", "duplicate allowed function %q", name)
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		return toolChoice{Mode: toolChoiceAllowed, AllowedMode: toolChoiceMode(mode), AllowedNames: names}, nil
	}
	if _, err := rejectUnknownResponsesObject(ProtocolResponses, trimmed, "$.tool_choice", "type", "name"); err != nil {
		return toolChoice{}, err
	}
	var value struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice", "must be a string or object")
	}
	if value.Type != "function" {
		return toolChoice{}, unsupported(ProtocolResponses, "$.tool_choice.type", "tool choice type %q has no Gemini equivalent", value.Type)
	}
	if value.Name == "" {
		return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice.name", "non-empty string is required")
	}
	return toolChoice{Mode: toolChoiceNamed, Name: value.Name}, nil
}

func encodeResponsesToolChoice(choice toolChoice) json.RawMessage {
	if choice.Mode == toolChoiceAllowed {
		tools := make([]any, 0, len(choice.AllowedNames))
		for _, name := range choice.AllowedNames {
			tools = append(tools, map[string]any{"type": "function", "name": name})
		}
		data, _ := json.Marshal(map[string]any{"type": "allowed_tools", "mode": string(choice.AllowedMode), "tools": tools})
		return data
	}
	if choice.Mode == toolChoiceNamed {
		data, _ := json.Marshal(map[string]any{"type": "function", "name": choice.Name})
		return data
	}
	data, _ := json.Marshal(string(choice.Mode))
	return data
}

func validateResponsesTool(tool responsesTool, path string) error {
	if tool.Type != "function" {
		return unsupported(ProtocolResponses, path+".type", "tool type %q has no Gemini function-declaration equivalent", tool.Type)
	}
	if tool.Name == "" {
		return invalid(ProtocolResponses, path+".name", "function name is required")
	}
	if tool.Async != nil && *tool.Async {
		return unsupported(ProtocolResponses, path+".async", "asynchronous function tools have no Gemini function-declaration equivalent")
	}
	if tool.DeferLoading {
		return unsupported(ProtocolResponses, path+".defer_loading", "deferred tool loading requires a native Responses provider")
	}
	if len(tool.AllowedCallers) > 0 {
		return unsupported(ProtocolResponses, path+".allowed_callers", "Responses caller restrictions have no Gemini function-declaration equivalent")
	}
	if nonNullJSON(tool.OutputSchema) {
		if _, err := normalizeGeminiJSONSchema(ProtocolResponses, path+".output_schema", tool.OutputSchema); err != nil {
			return err
		}
	}
	if nonNullJSON(tool.Format) || tool.Execution != "" || tool.ExternalWebAccess != nil || nonNullJSON(tool.Filters) || nonNullJSON(tool.UserLocation) || tool.SearchContextSize != "" {
		return invalid(ProtocolResponses, path, "function tool contains fields for another tool type")
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
			if path == "$.output" {
				if item.ID == "" || item.Status == "" {
					return upstreamResponseError(ProtocolResponses, itemPath, "output messages require id and status")
				}
				if item.Status != "in_progress" && item.Status != "completed" && item.Status != "incomplete" {
					return upstreamResponseError(ProtocolResponses, itemPath+".status", "invalid message status %q", item.Status)
				}
			}
			if item.Role != "user" && item.Role != "assistant" && item.Role != "system" && item.Role != "developer" {
				return invalid(ProtocolResponses, itemPath+".role", "unsupported message role %q", item.Role)
			}
			if err := validateResponsesMessageContent(item.Content, itemPath+".content", path != "$.output", item.Role); err != nil {
				return err
			}
		case "function_call":
			if !nonNullJSON(item.Arguments) {
				return invalid(ProtocolResponses, itemPath+".arguments", "function_call arguments are required")
			}
			if item.CallID == "" || item.Name == "" {
				return invalid(ProtocolResponses, itemPath, "function_call requires call_id and name")
			}
			if err := validateResponsesFunctionItemFields(item, itemPath); err != nil {
				return err
			}
			if path == "$.output" {
				if item.Status != "" && item.Status != "in_progress" && item.Status != "completed" && item.Status != "incomplete" {
					return upstreamResponseError(ProtocolResponses, itemPath+".status", "invalid function_call status %q", item.Status)
				}
			} else {
				if item.ID != "" && item.ID != item.CallID {
					return unsupported(ProtocolResponses, itemPath+".id", "Gemini cannot preserve a function-call item id separate from call_id")
				}
				if item.Status != "" && item.Status != "completed" {
					return unsupported(ProtocolResponses, itemPath+".status", "Gemini history cannot preserve function_call status %q", item.Status)
				}
			}
		case "function_call_output":
			if err := validateResponsesToolOutput(ProtocolResponses, item.Output, itemPath+".output"); err != nil {
				return err
			}
			if path == "$.output" {
				return unsupported(ProtocolResponses, itemPath+".type", "Gemini model responses cannot represent a function_call_output item")
			}
			if item.CallID == "" && item.Name == "" {
				return unsupported(ProtocolResponses, itemPath, "Gemini function responses require call_id or name correlation")
			}
			if item.ID != "" && item.ID != item.CallID {
				return unsupported(ProtocolResponses, itemPath+".id", "Gemini cannot preserve a function-output item id separate from call_id")
			}
			if item.Status != "" && item.Status != "completed" {
				return unsupported(ProtocolResponses, itemPath+".status", "Gemini history cannot preserve function output status %q", item.Status)
			}
			if err := validateResponsesFunctionItemFields(item, itemPath); err != nil {
				return err
			}
		case "reasoning":
		default:
			return unsupported(ProtocolResponses, itemPath+".type", "item type %q is not supported by this cross-protocol route", item.Type)
		}
	}
	return nil
}

func validateResponsesItemsForGemini(items []responsesItem, path string, codingAgentCompatible bool) error {
	if !codingAgentCompatible {
		return validateResponsesItems(items, path)
	}
	normalized := append([]responsesItem(nil), items...)
	for index := range normalized {
		if normalized[index].Type == "reasoning" {
			normalized[index].EncryptedContent = nil
		}
	}
	return validateResponsesItems(normalized, path)
}

func validateResponsesMessageContent(raw json.RawMessage, path string, input bool, role string) error {
	if len(raw) == 0 || string(raw) == "null" {
		if input {
			return invalid(ProtocolResponses, path, "message content is required")
		}
		return upstreamResponseError(ProtocolResponses, path, "output message content is required")
	}
	if raw[0] == '"' {
		if !input {
			return upstreamResponseError(ProtocolResponses, path, "output message content must be an array")
		}
		return nil
	}
	var parts []responsesContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return invalid(ProtocolResponses, path, "message content must be a string or content-part array")
	}
	for index, part := range parts {
		allowed := part.Type == "output_text" || part.Type == "refusal"
		if input && role == "assistant" {
			allowed = allowed || part.Type == "input_text" || part.Type == "input_image" || part.Type == "input_file"
		} else if input {
			allowed = part.Type == "input_text" || part.Type == "input_image" || part.Type == "input_file"
		}
		if !allowed {
			return unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].type", path, index), "content part %q is not valid in this message context", part.Type)
		}
	}
	return nil
}

func validateResponsesFunctionItemFields(item responsesItem, path string) error {
	if item.Async != nil && *item.Async {
		return unsupported(ProtocolResponses, path+".async", "asynchronous Responses calls have no Gemini function-call equivalent")
	}
	if jsonValuePresent(item.Caller) {
		return unsupported(ProtocolResponses, path+".caller", "Responses caller provenance has no Gemini function-call equivalent")
	}
	if item.Namespace != "" {
		return unsupported(ProtocolResponses, path+".namespace", "Responses tool namespaces have no Gemini function-call equivalent")
	}
	if item.CreatedBy != "" {
		return unsupported(ProtocolResponses, path+".created_by", "Responses item provenance has no Gemini function-call equivalent")
	}
	if item.Execution != "" || nonNullJSON(item.Action) || nonNullJSON(item.Tools) {
		return unsupported(ProtocolResponses, path, "hosted-tool metadata has no Gemini function-call equivalent")
	}
	if item.Status != "" && item.Status != "in_progress" && item.Status != "completed" && item.Status != "incomplete" {
		return invalid(ProtocolResponses, path+".status", "invalid function item status %q", item.Status)
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
	if source.Status == "incomplete" && source.IncompleteDetails != nil {
		switch source.IncompleteDetails.Reason {
		case "", "max_output_tokens", "content_filter":
		case "max_messages", "steered":
			return unsupported(ProtocolResponses, "$.incomplete_details.reason", "Gemini has no exact finish reason for Responses reason %q", source.IncompleteDetails.Reason)
		default:
			return upstreamResponseError(ProtocolResponses, "$.incomplete_details.reason", "invalid incomplete reason %q", source.IncompleteDetails.Reason)
		}
	}
	for index, item := range source.Output {
		path := fmt.Sprintf("$.output[%d]", index)
		switch item.Type {
		case "function_call":
			if !nonNullJSON(item.Arguments) {
				return upstreamResponseError(ProtocolResponses, path+".arguments", "function_call arguments are required")
			}
		case "function_call_output":
			if !nonNullJSON(item.Output) {
				return upstreamResponseError(ProtocolResponses, path+".output", "function_call_output output is required")
			}
		}
	}
	return validateResponsesItems(source.Output, "$.output")
}

func validateResponsesTerminalForGemini(source responsesResponse, codingAgentCompatible bool) error {
	if !codingAgentCompatible {
		return validateResponsesTerminal(source)
	}
	normalized := source
	normalized.Output = append([]responsesItem(nil), source.Output...)
	for index := range normalized.Output {
		if normalized.Output[index].Type == "reasoning" {
			normalized.Output[index].EncryptedContent = nil
		}
	}
	return validateResponsesTerminal(normalized)
}
