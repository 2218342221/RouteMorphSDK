package chatresponses

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
		breakpointPath := fmt.Sprintf("%s[%d].prompt_cache_breakpoint", path, i)
		if err := validatePromptCacheBreakpoint(ProtocolResponses, breakpointPath, part.PromptCacheBreakpoint); err != nil {
			return nil, err
		}
		if !input && jsonValuePresent(part.PromptCacheBreakpoint) {
			return nil, unsupported(ProtocolResponses, breakpointPath, "output prompt cache breakpoints have no Chat response equivalent")
		}
		if len(part.Annotations) > 0 && string(part.Annotations) != "null" && string(part.Annotations) != "[]" {
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].annotations", path, i), "output annotations cannot be represented cross-protocol")
		}
		if len(part.Logprobs) > 0 && string(part.Logprobs) != "null" && string(part.Logprobs) != "[]" {
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].logprobs", path, i), "output log probabilities cannot be represented cross-protocol")
		}
		converted := portablePart{PromptCacheBreakpoint: append(json.RawMessage(nil), part.PromptCacheBreakpoint...)}
		switch part.Type {
		case "input_text", "output_text", "summary_text", "reasoning_text", "text":
			converted.Kind, converted.Text = partText, part.Text
			parts = append(parts, converted)
		case "refusal":
			converted.Kind, converted.Text = partRefusal, part.Refusal
			parts = append(parts, converted)
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
			converted.Kind, converted.Media = partImage, media
			parts = append(parts, converted)
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
			converted.Kind, converted.Media = partFile, media
			parts = append(parts, converted)
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
	for index, part := range parts {
		if err := validatePromptCacheBreakpoint(ProtocolChat, fmt.Sprintf("$.messages.content[%d].prompt_cache_breakpoint", index), part.PromptCacheBreakpoint); err != nil {
			return nil, err
		}
		switch part.Kind {
		case partText:
			typeName := "input_text"
			annotations := json.RawMessage(nil)
			if !input {
				typeName = "output_text"
				annotations = json.RawMessage(`[]`)
			}
			converted = append(converted, responsesContentPart{Type: typeName, Text: part.Text, Annotations: annotations, PromptCacheBreakpoint: append(json.RawMessage(nil), part.PromptCacheBreakpoint...)})
		case partRefusal:
			converted = append(converted, responsesContentPart{Type: "refusal", Refusal: part.Text})
		case partImage:
			if part.Media == nil {
				return nil, invalid(ProtocolResponses, "$.input", "nil image")
			}
			if (part.Media.FileID == "") == (part.Media.URL == "" && part.Media.Data == "") || (part.Media.URL != "" && part.Media.Data != "") {
				return nil, invalid(ProtocolResponses, "$.input", "input_image requires exactly one source")
			}
			converted = append(converted, responsesContentPart{Type: "input_image", ImageURL: dataURL(part.Media), FileID: part.Media.FileID, Detail: part.Media.Detail, PromptCacheBreakpoint: append(json.RawMessage(nil), part.PromptCacheBreakpoint...)})
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
			converted = append(converted, responsesContentPart{Type: "input_file", FileID: part.Media.FileID, FileURL: part.Media.URL, FileData: openAIFileData(part.Media), Filename: part.Media.Filename, PromptCacheBreakpoint: append(json.RawMessage(nil), part.PromptCacheBreakpoint...)})
		case partAudio:
			return nil, unsupported(ProtocolResponses, fmt.Sprintf("$.input.content[%d]", index), "Responses Create message content has no input_audio part")
		default:
			return nil, unsupported(ProtocolResponses, "$.input", "part %q cannot be encoded as message content", part.Kind)
		}
	}
	return converted, nil
}

func validateResponsesTool(tool responsesTool, path string) error {
	if err := validateResponsesToolPortableFields(tool, path); err != nil {
		return err
	}
	switch tool.Type {
	case "function":
		if tool.Name == "" {
			return invalid(ProtocolResponses, path+".name", "function name is required")
		}
		if nonNullJSON(tool.Format) {
			return invalid(ProtocolResponses, path+".format", "format is only valid for custom tools")
		}
		if tool.Execution != "" || tool.ExternalWebAccess != nil || nonNullJSON(tool.Filters) || nonNullJSON(tool.UserLocation) || tool.SearchContextSize != "" {
			return invalid(ProtocolResponses, path, "function tool contains fields for another tool type")
		}
	case "custom":
		if tool.Name == "" {
			return invalid(ProtocolResponses, path+".name", "custom tool name is required")
		}
		if nonNullJSON(tool.Parameters) || tool.Strict != nil {
			return invalid(ProtocolResponses, path, "custom tools cannot contain function parameters or strict")
		}
		if tool.Execution != "" || tool.ExternalWebAccess != nil || nonNullJSON(tool.Filters) || nonNullJSON(tool.UserLocation) || tool.SearchContextSize != "" {
			return invalid(ProtocolResponses, path, "custom tool contains fields for another tool type")
		}
	case "web_search", "web_search_2025_08_26":
		if tool.Name != "" || nonNullJSON(tool.Parameters) || tool.Strict != nil || nonNullJSON(tool.Format) {
			return invalid(ProtocolResponses, path, "web-search tool contains fields for another tool type")
		}
		if tool.Execution != "" {
			return invalid(ProtocolResponses, path+".execution", "is not valid for web_search")
		}
	case "tool_search":
		return unsupported(ProtocolResponses, path+".type", "tool_search has no Chat equivalent")
	default:
		return unsupported(ProtocolResponses, path+".type", "tool type %q requires a native Responses provider", tool.Type)
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
			if err := validateResponsesItemProvenance(item, itemPath); err != nil {
				return err
			}
			if !nonNullJSON(item.Arguments) {
				return invalid(ProtocolResponses, itemPath+".arguments", "function_call arguments are required")
			}
			if item.CallID == "" || item.Name == "" {
				return invalid(ProtocolResponses, itemPath, "function_call requires call_id and name")
			}
			if path == "$.output" {
				if item.Status != "" && item.Status != "in_progress" && item.Status != "completed" && item.Status != "incomplete" {
					return upstreamResponseError(ProtocolResponses, itemPath+".status", "invalid function_call status %q", item.Status)
				}
			} else if err := validateResponsesHistoryCallStatus(item.Status, itemPath+".status"); err != nil {
				return err
			}
		case "function_call_output":
			if err := validateResponsesItemProvenance(item, itemPath); err != nil {
				return err
			}
			if err := validateResponsesToolOutput(ProtocolResponses, item.Output, itemPath+".output"); err != nil {
				return err
			}
			if path == "$.output" {
				return unsupported(ProtocolResponses, itemPath+".type", "Chat responses cannot represent a function_call_output item")
			}
			if item.CallID == "" && item.Name == "" {
				return unsupported(ProtocolResponses, itemPath, "Chat tool results require call_id or name correlation")
			}
			if item.ID != "" && item.ID != item.CallID {
				return unsupported(ProtocolResponses, itemPath+".id", "Chat cannot preserve a function-output item id separate from call_id")
			}
			if err := validateResponsesHistoryCallStatus(item.Status, itemPath+".status"); err != nil {
				return err
			}
		case "custom_tool_call":
			if err := validateResponsesItemProvenance(item, itemPath); err != nil {
				return err
			}
			if item.CallID == "" || item.Name == "" {
				return invalid(ProtocolResponses, itemPath, "custom_tool_call requires call_id and name")
			}
			if _, err := customInput(item.Input, ProtocolResponses, itemPath+".input"); err != nil {
				return err
			}
			if item.Status != "" {
				if path == "$.output" {
					return upstreamResponseError(ProtocolResponses, itemPath+".status", "custom_tool_call has no status field")
				}
				return invalid(ProtocolResponses, itemPath+".status", "custom_tool_call has no status field")
			}
		case "custom_tool_call_output":
			if err := validateResponsesItemProvenance(item, itemPath); err != nil {
				return err
			}
			if err := validateResponsesToolOutput(ProtocolResponses, item.Output, itemPath+".output"); err != nil {
				return err
			}
			if path == "$.output" {
				return unsupported(ProtocolResponses, itemPath+".type", "Chat responses cannot represent a custom_tool_call_output item")
			}
			if item.CallID == "" {
				return invalid(ProtocolResponses, itemPath+".call_id", "call_id is required")
			}
			if item.ID != "" && item.ID != item.CallID {
				return unsupported(ProtocolResponses, itemPath+".id", "Chat cannot preserve a custom-output item id separate from call_id")
			}
			if item.Status != "" {
				return invalid(ProtocolResponses, itemPath+".status", "custom_tool_call_output has no status field")
			}
		case "web_search_call":
			if path != "$.output" {
				return unsupported(ProtocolResponses, itemPath+".type", "web_search_call history cannot be represented by Chat")
			}
			if item.ID == "" {
				return upstreamResponseError(ProtocolResponses, itemPath+".id", "web_search_call id is required")
			}
			if item.Status != "in_progress" && item.Status != "searching" && item.Status != "completed" && item.Status != "failed" {
				return upstreamResponseError(ProtocolResponses, itemPath+".status", "invalid web_search_call status %q", item.Status)
			}
			if err := validateResponsesWebSearchAction(item.Action, itemPath+".action"); err != nil {
				return err
			}
		case "reasoning":
			if path == "$.output" {
				if err := validateResponsesReasoningItem(item, itemPath); err != nil {
					return err
				}
			}
		default:
			return unsupported(ProtocolResponses, itemPath+".type", "item type %q is not supported by this cross-protocol route", item.Type)
		}
	}
	return nil
}

func validateResponsesWebSearchAction(raw json.RawMessage, path string) error {
	var action map[string]json.RawMessage
	if !nonNullJSON(raw) || json.Unmarshal(raw, &action) != nil || action == nil {
		return upstreamResponseError(ProtocolResponses, path, "web_search_call action object is required")
	}
	var actionType string
	if err := json.Unmarshal(action["type"], &actionType); err != nil || actionType == "" {
		return upstreamResponseError(ProtocolResponses, path+".type", "web search action type is required")
	}
	allowed := map[string]bool{"type": true}
	switch actionType {
	case "search":
		allowed["query"], allowed["queries"], allowed["sources"] = true, true, true
		if rawQuery, ok := action["query"]; ok {
			var query string
			if json.Unmarshal(rawQuery, &query) != nil {
				return upstreamResponseError(ProtocolResponses, path+".query", "must be a string")
			}
		}
		if rawQueries, ok := action["queries"]; ok {
			var queries []string
			if json.Unmarshal(rawQueries, &queries) != nil {
				return upstreamResponseError(ProtocolResponses, path+".queries", "must be a string array")
			}
		}
		if rawSources, ok := action["sources"]; ok {
			var sources []json.RawMessage
			if json.Unmarshal(rawSources, &sources) != nil {
				return upstreamResponseError(ProtocolResponses, path+".sources", "must be an array")
			}
			for index, source := range sources {
				sourcePath := fmt.Sprintf("%s.sources[%d]", path, index)
				var fields map[string]json.RawMessage
				if json.Unmarshal(source, &fields) != nil || fields == nil {
					return upstreamResponseError(ProtocolResponses, sourcePath, "must be an object")
				}
				for name := range fields {
					if name != "type" && name != "url" {
						return upstreamResponseError(ProtocolResponses, sourcePath+"."+name, "field is not valid for a web search source")
					}
				}
				var sourceType, sourceURL string
				if json.Unmarshal(fields["type"], &sourceType) != nil || sourceType != "url" {
					return upstreamResponseError(ProtocolResponses, sourcePath+".type", "must be url")
				}
				if json.Unmarshal(fields["url"], &sourceURL) != nil || !validHTTPURL(sourceURL) {
					return upstreamResponseError(ProtocolResponses, sourcePath+".url", "must be an absolute HTTP(S) URL")
				}
			}
		}
	case "open_page":
		allowed["url"] = true
		if rawURL, ok := action["url"]; ok && nonNullJSON(rawURL) {
			var value string
			if json.Unmarshal(rawURL, &value) != nil || !validHTTPURL(value) {
				return upstreamResponseError(ProtocolResponses, path+".url", "must be an absolute HTTP(S) URL or null")
			}
		}
	case "find_in_page":
		allowed["url"], allowed["pattern"] = true, true
		var value, pattern string
		if json.Unmarshal(action["url"], &value) != nil || !validHTTPURL(value) {
			return upstreamResponseError(ProtocolResponses, path+".url", "must be an absolute HTTP(S) URL")
		}
		if json.Unmarshal(action["pattern"], &pattern) != nil || pattern == "" {
			return upstreamResponseError(ProtocolResponses, path+".pattern", "non-empty pattern is required")
		}
	default:
		return upstreamResponseError(ProtocolResponses, path+".type", "unsupported web search action %q", actionType)
	}
	for name := range action {
		if !allowed[name] {
			return upstreamResponseError(ProtocolResponses, path+"."+name, "field is not valid for %s action", actionType)
		}
	}
	return nil
}

func validateResponsesHistoryCallStatus(status, path string) error {
	switch status {
	case "", "completed":
		return nil
	case "in_progress", "incomplete":
		return unsupported(ProtocolResponses, path, "Chat history cannot preserve Responses call status %q", status)
	default:
		return invalid(ProtocolResponses, path, "invalid call status %q", status)
	}
}

func validateResponsesReasoningItem(item responsesItem, path string) error {
	if item.ID == "" {
		return upstreamResponseError(ProtocolResponses, path+".id", "reasoning output item id is required")
	}
	if len(item.Summary) == 0 {
		return upstreamResponseError(ProtocolResponses, path+".summary", "reasoning output item summary is required")
	}
	if err := validateResponsesReasoningParts(item.Summary, path+".summary", "summary_text"); err != nil {
		return err
	}
	if len(item.Content) > 0 {
		if err := validateResponsesReasoningParts(item.Content, path+".content", "reasoning_text"); err != nil {
			return err
		}
	}
	if item.Status != "" && item.Status != "in_progress" && item.Status != "completed" && item.Status != "incomplete" {
		return upstreamResponseError(ProtocolResponses, path+".status", "invalid reasoning status %q", item.Status)
	}
	return nil
}

func validateResponsesReasoningParts(raw json.RawMessage, path, wantType string) error {
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil || parts == nil {
		return upstreamResponseError(ProtocolResponses, path, "must be an array")
	}
	for index, part := range parts {
		partPath := fmt.Sprintf("%s[%d]", path, index)
		for field := range part {
			if field != "type" && field != "text" {
				return upstreamResponseError(ProtocolResponses, partPath+"."+field, "field is not valid for %s", wantType)
			}
		}
		var partType, text string
		if err := json.Unmarshal(part["type"], &partType); err != nil || partType != wantType {
			return upstreamResponseError(ProtocolResponses, partPath+".type", "expected %q", wantType)
		}
		if rawText, ok := part["text"]; !ok || json.Unmarshal(rawText, &text) != nil {
			return upstreamResponseError(ProtocolResponses, partPath+".text", "text is required")
		}
	}
	return nil
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

func responsesPhaseDiagnostics(items []responsesItem, path string) []Diagnostic {
	var diagnostics []Diagnostic
	for index, item := range items {
		if item.Phase != "" {
			diagnostics = appendDiagnostic(diagnostics, "warning", "responses_output_phase_not_representable", fmt.Sprintf("%s[%d].phase", path, index), fmt.Sprintf("output phase %q is not represented by the target protocol", item.Phase))
		}
	}
	return diagnostics
}

func decodeResponsesTextOptions(raw json.RawMessage) (*portableTextFormat, json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	fields, err := rejectUnknownObjectFields(ProtocolResponses, "$.text", raw, "format", "verbosity")
	if err != nil {
		return nil, nil, err
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
	if jsonValuePresent(value.Verbosity) {
		verbosity := rawString(value.Verbosity)
		if verbosity != "low" && verbosity != "medium" && verbosity != "high" {
			return nil, nil, invalid(ProtocolResponses, "$.text.verbosity", "unsupported verbosity %q", verbosity)
		}
	}
	if !jsonValuePresent(fields["format"]) {
		return nil, value.Verbosity, nil
	}
	if value.Format.Type == "" {
		return nil, nil, invalid(ProtocolResponses, "$.text.format.type", "is required")
	}
	if value.Format.Type == "text" {
		if _, err := rejectUnknownObjectFields(ProtocolResponses, "$.text.format", fields["format"], "type"); err != nil {
			return nil, nil, err
		}
		return nil, value.Verbosity, nil
	}
	if value.Format.Type == "json_object" {
		if _, err := rejectUnknownObjectFields(ProtocolResponses, "$.text.format", fields["format"], "type"); err != nil {
			return nil, nil, err
		}
		return &portableTextFormat{Type: "json_object"}, value.Verbosity, nil
	}
	if value.Format.Type != "json_schema" {
		return nil, nil, unsupported(ProtocolResponses, "$.text.format.type", "format %q is not losslessly portable", value.Format.Type)
	}
	if value.Format.Name == "" || !jsonValuePresent(value.Format.Schema) {
		return nil, nil, invalid(ProtocolResponses, "$.text.format", "name and schema are required")
	}
	if _, err := rejectUnknownObjectFields(ProtocolResponses, "$.text.format", fields["format"], "type", "name", "description", "schema", "strict"); err != nil {
		return nil, nil, err
	}
	return &portableTextFormat{Type: "json_schema", Name: value.Format.Name, Description: value.Format.Description, Schema: value.Format.Schema, Strict: value.Format.Strict}, value.Verbosity, nil
}

type responsesResponse = responseswire.Response
type responsesError = responseswire.Error

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
			return unsupported(ProtocolResponses, "$.incomplete_details.reason", "Chat has no exact finish reason for Responses reason %q", source.IncompleteDetails.Reason)
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
		case "custom_tool_call_output":
			if !nonNullJSON(item.Output) {
				return upstreamResponseError(ProtocolResponses, path+".output", "custom_tool_call_output output is required")
			}
		}
	}
	return validateResponsesItems(source.Output, "$.output")
}
