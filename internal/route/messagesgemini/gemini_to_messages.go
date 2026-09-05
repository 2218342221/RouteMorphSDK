package messagesgemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (c *geminiToMessagesConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolGenerateContent, input, "contents", "systemInstruction", "tools", "toolConfig", "generationConfig", "safetySettings", "cachedContent", "model", "serviceTier", "store"); err != nil {
		return conversionResult{}, err
	}
	if err := validateGeminiNestedFields(input); err != nil {
		return conversionResult{}, err
	}
	var source geminiRequest
	if err := decodeJSON(ProtocolGenerateContent, input, &source); err != nil {
		return conversionResult{}, err
	}
	if err := validateGeminiPortableRequest(&source); err != nil {
		return conversionResult{}, err
	}
	if source.CachedContent != "" {
		return conversionResult{}, unsupported(ProtocolGenerateContent, "$.cachedContent", "cached content state requires a native generateContent provider")
	}
	if source.Model != "" {
		return conversionResult{}, unsupported(ProtocolGenerateContent, "$.model", "body model selection cannot be preserved when routing to Messages")
	}
	if source.ServiceTier != "" {
		return conversionResult{}, unsupported(ProtocolGenerateContent, "$.serviceTier", "Gemini service-tier semantics are provider specific")
	}
	if source.Store != nil {
		return conversionResult{}, unsupported(ProtocolGenerateContent, "$.store", "Gemini logging policy has no exact Messages equivalent")
	}
	if jsonValuePresent(source.SafetySettings) {
		return conversionResult{}, unsupported(ProtocolGenerateContent, "$.safetySettings", "provider safety policy is not portable to Messages")
	}

	maxTokens := 4096
	var diagnostics []Diagnostic
	target := messagesRequest{Model: options.Exchange.UpstreamModel, MaxTokens: maxTokens, Stream: resolveExchangeStream(false, options.Exchange)}
	if source.GenerationConfig != nil {
		config := source.GenerationConfig
		if config.MaxOutputTokens != nil {
			target.MaxTokens = *config.MaxOutputTokens
		} else {
			diagnostics = appendDiagnostic(diagnostics, "warning", "default_max_tokens", "$.max_tokens", "Messages requires max_tokens; RouteMorph used 4096")
		}
		if target.MaxTokens <= 0 {
			return conversionResult{}, invalid(ProtocolGenerateContent, "$.generationConfig.maxOutputTokens", "must be greater than zero")
		}
		target.Temperature, target.TopP, target.TopK = config.Temperature, config.TopP, config.TopK
		target.StopSequences = append([]string(nil), config.StopSequences...)
		if config.ThinkingConfig != nil {
			return conversionResult{}, unsupported(ProtocolGenerateContent, "$.generationConfig.thinkingConfig", "Gemini thinkingConfig is not semantically equivalent to Messages thinking")
		}
		if jsonValuePresent(config.ResponseJSONSchema) {
			converted, err := normalizeGeminiJSONSchema(ProtocolGenerateContent, "$.generationConfig.responseJsonSchema", config.ResponseJSONSchema)
			if err != nil {
				return conversionResult{}, err
			}
			target.OutputConfig = &messagesOutputConfig{Format: &struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			}{Type: "json_schema", Schema: converted}}
		} else if jsonValuePresent(config.ResponseSchema) {
			converted, err := messagesGeminiSchemaToJSONSchema(config.ResponseSchema, "$.generationConfig.responseSchema")
			if err != nil {
				return conversionResult{}, err
			}
			target.OutputConfig = &messagesOutputConfig{Format: &struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			}{Type: "json_schema", Schema: converted}}
		} else if config.ResponseMIMEType == "application/json" {
			return conversionResult{}, unsupported(ProtocolGenerateContent, "$.generationConfig.responseMimeType", "Messages output_config cannot express schema-less JSON mode")
		}
	} else {
		diagnostics = appendDiagnostic(diagnostics, "warning", "default_max_tokens", "$.max_tokens", "Messages requires max_tokens; RouteMorph used 4096")
	}
	if target.Model == "" {
		return conversionResult{}, invalid(ProtocolMessages, "$.model", "upstream model is required for generateContent to Messages conversion")
	}

	if source.SystemInstruction != nil {
		blocks, blockDiagnostics, _, err := geminiPartsToMessages(source.SystemInstruction.Parts, "system", "$.systemInstruction.parts", nil)
		if err != nil {
			return conversionResult{}, err
		}
		for index, block := range blocks {
			if block.Type != "text" {
				return conversionResult{}, unsupported(ProtocolGenerateContent, fmt.Sprintf("$.systemInstruction.parts[%d]", index), "Messages system content only has a portable text mapping")
			}
		}
		diagnostics = append(diagnostics, blockDiagnostics...)
		target.System = mustJSON(blocks)
	}

	if len(source.Tools) > 0 {
		for toolIndex, tool := range source.Tools {
			for functionIndex, function := range tool.FunctionDeclarations {
				path := fmt.Sprintf("$.tools[%d].functionDeclarations[%d]", toolIndex, functionIndex)
				schema := function.ParametersJSONSchema
				schemaPath := path + ".parametersJsonSchema"
				var err error
				if jsonValuePresent(schema) {
					schema, err = normalizeGeminiJSONSchema(ProtocolGenerateContent, schemaPath, schema)
				} else {
					schemaPath = path + ".parameters"
					schema, err = messagesGeminiSchemaToJSONSchema(function.Parameters, schemaPath)
				}
				if err != nil {
					return conversionResult{}, err
				}
				schema, err = normalizeMessagesInputSchema(schema, schemaPath)
				if err != nil {
					return conversionResult{}, err
				}
				target.Tools = append(target.Tools, messagesTool{Name: function.Name, Description: function.Description, InputSchema: schema})
			}
		}
	}
	if source.ToolConfig != nil {
		config := source.ToolConfig.FunctionCallingConfig
		choice := toolChoice{}
		switch config.Mode {
		case "", "AUTO":
			if len(config.AllowedFunctionNames) > 0 {
				return conversionResult{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "AUTO restricted to named functions has no Messages equivalent")
			}
			choice.Mode = toolChoiceAuto
		case "NONE":
			if len(config.AllowedFunctionNames) > 0 {
				return conversionResult{}, invalid(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "NONE cannot restrict allowed functions")
			}
			choice.Mode = toolChoiceNone
		case "ANY":
			switch len(config.AllowedFunctionNames) {
			case 0:
				choice.Mode = toolChoiceRequired
			case 1:
				choice = toolChoice{Mode: toolChoiceNamed, Name: config.AllowedFunctionNames[0]}
			default:
				return conversionResult{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "Messages cannot restrict tool choice to multiple named functions")
			}
		case "VALIDATED":
			return conversionResult{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.mode", "VALIDATED permits text or schema-valid calls and has no Messages equivalent")
		default:
			return conversionResult{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.mode", "mode %q is not portable", config.Mode)
		}
		target.ToolChoice = encodeMessagesToolChoice(choice, nil)
	}

	tracker := newGeminiCallTracker()
	for contentIndex, content := range source.Contents {
		path := fmt.Sprintf("$.contents[%d]", contentIndex)
		role := content.Role
		if role == "" {
			role = "user"
		}
		if role != "user" && role != "model" {
			return conversionResult{}, unsupported(ProtocolGenerateContent, path+".role", "role %q is not portable", role)
		}
		messageRole := "user"
		if role == "model" {
			messageRole = "assistant"
		}
		blocks, blockDiagnostics, _, err := geminiPartsToMessages(content.Parts, messageRole, path+".parts", tracker)
		if err != nil {
			return conversionResult{}, err
		}
		diagnostics = append(diagnostics, blockDiagnostics...)
		appendMessagesTurn(&target.Messages, messagesMessage{Role: messageRole, Content: mustJSON(blocks)})
	}
	body, err := marshal(ProtocolMessages, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *geminiToMessagesConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source messagesResponse
	if err := decodeJSON(ProtocolMessages, input, &source); err != nil {
		return conversionResult{}, err
	}
	if err := validateMessagesResponse(source); err != nil {
		return conversionResult{}, err
	}
	responseDiagnostics, err := messagesResponseExtensionDiagnostics(source, options.LossPolicy)
	if err != nil {
		return conversionResult{}, err
	}
	blocks, err := decodeMessagesBlocks(source.Content, "$.content")
	if err != nil {
		return conversionResult{}, err
	}
	parts, diagnostics, err := messagesBlocksToGemini(blocks, "assistant", "$.content", make(map[string]string), nil)
	if err != nil {
		return conversionResult{}, err
	}
	diagnostics = append(responseDiagnostics, diagnostics...)
	finish, err := parseMessagesFinish(source.StopReason)
	if err != nil {
		return conversionResult{}, err
	}
	if source.StopReason == "stop_sequence" {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolMessages, "$.stop_sequence", "Gemini responses cannot preserve the matched stop sequence")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "stop_sequence_not_representable", "$.stop_sequence", "the matched stop sequence was omitted from the Gemini response")
	}
	if source.Usage.CacheCreationInputTokens > 0 {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolMessages, "$.usage.cache_creation_input_tokens", "Gemini usage has no cache-creation token field")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "cache_creation_usage_not_representable", "$.usage.cache_creation_input_tokens", "cache-creation tokens were omitted from Gemini usage")
	}
	if jsonValuePresent(source.Usage.ServerToolUse) {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolMessages, "$.usage.server_tool_use", "Gemini usage has no server-tool usage field")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "server_tool_usage_not_representable", "$.usage.server_tool_use", "server-tool usage was omitted from Gemini usage")
	}
	model := source.Model
	if options.Exchange.ClientModel != "" {
		model = options.Exchange.ClientModel
	}
	var target geminiResponse
	target.ResponseID, target.ModelVersion = source.ID, model
	target.Candidates = []geminiCandidate{{Content: geminiContent{Role: "model", Parts: parts}, FinishReason: geminiStop(finish)}}
	// Gemini's prompt count is inclusive, while Anthropic splits cache reads and
	// cache creation from input_tokens. Recombine all three for Gemini billing.
	target.UsageMetadata.PromptTokenCount = source.Usage.InputTokens + source.Usage.CacheReadInputTokens + source.Usage.CacheCreationInputTokens
	target.UsageMetadata.CandidatesTokenCount = source.Usage.OutputTokens
	if source.Usage.OutputTokensDetails != nil {
		target.UsageMetadata.ThoughtsTokenCount = source.Usage.OutputTokensDetails.ThinkingTokens
		target.UsageMetadata.CandidatesTokenCount -= source.Usage.OutputTokensDetails.ThinkingTokens
	}
	target.UsageMetadata.TotalTokenCount = target.UsageMetadata.PromptTokenCount + target.UsageMetadata.CandidatesTokenCount + target.UsageMetadata.ThoughtsTokenCount
	target.UsageMetadata.CachedContentTokenCount = source.Usage.CacheReadInputTokens
	body, err := marshal(ProtocolGenerateContent, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func messagesResponseExtensionDiagnostics(source messagesResponse, policy lossPolicy) ([]Diagnostic, error) {
	fields := []struct {
		path    string
		code    string
		present bool
		message string
	}{
		{"$.stop_details", "messages_stop_details_not_representable", jsonValuePresent(source.StopDetails), "Messages stop details were omitted"},
		{"$.container", "messages_response_container_not_representable", jsonValuePresent(source.Container), "Messages response container state was omitted"},
		{"$.usage.cache_creation", "messages_cache_creation_breakdown_not_representable", source.Usage.CacheCreation != nil, "Messages cache-creation token breakdown was omitted"},
		{"$.usage.inference_geo", "messages_inference_geo_not_representable", source.Usage.InferenceGeo != "", "Messages inference geography was omitted"},
		{"$.usage.service_tier", "messages_usage_service_tier_not_representable", source.Usage.ServiceTier != "", "Messages usage service tier was omitted"},
	}
	var diagnostics []Diagnostic
	for _, field := range fields {
		if !field.present {
			continue
		}
		if policy == rejectSemanticLoss {
			return nil, unsupported(ProtocolMessages, field.path, "field has no Gemini response equivalent")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", field.code, field.path, field.message)
	}
	return diagnostics, nil
}

func (c *geminiToMessagesConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return c.buffered(c.spec, options, c.ToClientResponse), nil
}

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

func messagesBlocksToGemini(blocks []messagesBlock, role, path string, callNames map[string]string, consumedCallIDs map[string]bool) ([]geminiPart, []Diagnostic, error) {
	parts := make([]geminiPart, 0, len(blocks))
	var diagnostics []Diagnostic
	for index, block := range blocks {
		blockPath := fmt.Sprintf("%s[%d]", path, index)
		if err := rejectMessagesBlockMetadata(block, blockPath); err != nil {
			return nil, diagnostics, err
		}
		switch block.Type {
		case "text":
			parts = append(parts, geminiPart{Text: block.Text})
		case "image", "document":
			if role != "user" {
				return nil, diagnostics, unsupported(ProtocolMessages, blockPath, "Messages multimodal content is only portable in user messages")
			}
			if block.Source == nil {
				return nil, diagnostics, invalid(ProtocolMessages, blockPath+".source", "source is required")
			}
			switch block.Source.Type {
			case "base64":
				if block.Source.URL != "" || block.Source.FileID != "" || jsonValuePresent(block.Source.Content) {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".source", "base64 source cannot contain url, file_id, or content")
				}
				if block.Source.MediaType == "" || block.Source.Data == "" {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".source", "media_type and data are required")
				}
				if !validBase64(block.Source.Data) {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".source.data", "must be valid base64")
				}
				if block.Type == "image" && !validMessagesImageMediaType(block.Source.MediaType) {
					return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".source.media_type", "Messages images require JPEG, PNG, GIF, or WebP")
				}
				if block.Type == "document" && block.Source.MediaType != "application/pdf" {
					return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".source.media_type", "only PDF documents are portable to Gemini")
				}
				parts = append(parts, geminiPart{InlineData: &geminiBlob{MIMEType: block.Source.MediaType, Data: block.Source.Data}})
			case "url":
				if block.Source.Data != "" || block.Source.MediaType != "" || block.Source.FileID != "" || jsonValuePresent(block.Source.Content) {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".source", "URL source cannot contain data, media_type, file_id, or content")
				}
				if block.Source.URL == "" {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".source.url", "URL is required")
				}
				if !geminiFileURI(block.Source.URL) {
					return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".source.url", "Gemini fileData requires a Gemini Files or Google Cloud Storage URI")
				}
				mimeType := "application/pdf"
				if block.Type == "image" {
					mimeType = mimeTypeFromURL(block.Source.URL)
					if !validMessagesImageMediaType(mimeType) {
						return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".source.url", "Gemini image fileData requires a URL whose JPEG, PNG, GIF, or WebP MIME type can be determined")
					}
				} else if inferred := mimeTypeFromURL(block.Source.URL); inferred != "" && inferred != mimeType {
					return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".source.url", "Messages URL documents must identify PDF content")
				}
				parts = append(parts, geminiPart{FileData: &geminiFileData{MIMEType: mimeType, FileURI: block.Source.URL}})
			case "file":
				if block.Source.Data != "" || block.Source.URL != "" || block.Source.MediaType != "" || jsonValuePresent(block.Source.Content) {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".source", "file source cannot contain data, url, media_type, or content")
				}
				if block.Source.FileID == "" {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".source.file_id", "file_id is required")
				}
				return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".source.file_id", "Messages file IDs are provider scoped and cannot be sent to Gemini")
			default:
				return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".source.type", "source type %q is not portable", block.Source.Type)
			}
		case "tool_use":
			if role != "assistant" {
				return nil, diagnostics, invalid(ProtocolMessages, blockPath, "tool_use blocks require the assistant role")
			}
			if block.ID == "" || block.Name == "" {
				return nil, diagnostics, invalid(ProtocolMessages, blockPath, "tool_use requires id and name")
			}
			if callNames != nil {
				if _, exists := callNames[block.ID]; exists {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".id", "duplicate tool_use id %q", block.ID)
				}
				callNames[block.ID] = block.Name
			}
			arguments, err := normalizeGeminiToolArguments(ProtocolMessages, blockPath+".input", block.Input)
			if err != nil {
				return nil, diagnostics, err
			}
			parts = append(parts, geminiPart{FunctionCall: &geminiFunctionCall{ID: block.ID, Name: block.Name, Args: arguments}})
			diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_thought_signature_unavailable", blockPath, "the source protocol cannot provide a provider-issued Gemini thoughtSignature; Gemini 3 may reject replayed function-call history")
		case "tool_result":
			if role != "user" {
				return nil, diagnostics, invalid(ProtocolMessages, blockPath, "tool_result blocks require the user role")
			}
			if block.ToolUseID == "" {
				return nil, diagnostics, invalid(ProtocolMessages, blockPath+".tool_use_id", "tool_use_id is required")
			}
			if block.IsError {
				return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".is_error", "Gemini functionResponse cannot preserve the tool error flag")
			}
			name := ""
			if callNames != nil {
				name = callNames[block.ToolUseID]
			}
			if name == "" {
				return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".tool_use_id", "Gemini functionResponse requires the corresponding function name")
			}
			if consumedCallIDs != nil {
				if consumedCallIDs[block.ToolUseID] {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".tool_use_id", "tool call %q already has an output", block.ToolUseID)
				}
				consumedCallIDs[block.ToolUseID] = true
			}
			response, err := messagesToolResultToGemini(block.Content, blockPath+".content")
			if err != nil {
				return nil, diagnostics, err
			}
			parts = append(parts, geminiPart{FunctionResponse: &geminiFunctionResponse{ID: block.ToolUseID, Name: name, Response: response}})
		case "thinking", "redacted_thinking":
			return nil, diagnostics, unsupported(ProtocolMessages, blockPath, "Anthropic signed or redacted thinking cannot be converted into a Gemini thought signature")
		default:
			return nil, diagnostics, unsupported(ProtocolMessages, blockPath+".type", "content block %q requires a native Messages provider", block.Type)
		}
	}
	return parts, diagnostics, nil
}

func messagesToolResultToGemini(raw json.RawMessage, path string) (json.RawMessage, error) {
	blocks, err := decodeMessagesBlocks(raw, path)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	for index, block := range blocks {
		if err := rejectMessagesBlockMetadata(block, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return nil, err
		}
		if block.Type != "text" {
			return nil, unsupported(ProtocolMessages, fmt.Sprintf("%s[%d]", path, index), "multimodal tool results require Gemini functionResponse.parts semantics")
		}
		text.WriteString(block.Text)
	}
	value := text.String()
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) == nil {
		if object, ok := decoded.(map[string]any); ok {
			return mustJSON(object), nil
		}
	}
	return mustJSON(map[string]any{"output": value}), nil
}

type geminiCallTracker struct {
	names     map[string]string
	pending   map[string][]string
	consumed  map[string]bool
	generated int
}

func newGeminiCallTracker() *geminiCallTracker {
	return &geminiCallTracker{names: make(map[string]string), pending: make(map[string][]string), consumed: make(map[string]bool)}
}

func (t *geminiCallTracker) add(id, name string) (string, bool, error) {
	generated := false
	if id == "" {
		t.generated++
		id = fmt.Sprintf("call_rm_%d", t.generated)
		generated = true
	}
	if _, exists := t.names[id]; exists {
		return "", generated, invalid(ProtocolGenerateContent, "$.contents.parts.functionCall.id", "duplicate function call id %q", id)
	}
	t.names[id] = name
	t.pending[name] = append(t.pending[name], id)
	return id, generated, nil
}

func (t *geminiCallTracker) resolve(id, name string) (string, error) {
	if id != "" {
		declaredName, exists := t.names[id]
		if !exists {
			return "", unsupported(ProtocolGenerateContent, "$.contents.parts.functionResponse.id", "function response id %q has no preceding function call", id)
		}
		if declaredName != name {
			return "", invalid(ProtocolGenerateContent, "$.contents.parts.functionResponse.name", "function response name %q does not match call %q", name, declaredName)
		}
		if t.consumed[id] {
			return "", invalid(ProtocolGenerateContent, "$.contents.parts.functionResponse.id", "duplicate function response for %q", id)
		}
		t.consumed[id] = true
		return id, nil
	}
	for _, pendingID := range t.pending[name] {
		if !t.consumed[pendingID] {
			t.consumed[pendingID] = true
			return pendingID, nil
		}
	}
	return "", unsupported(ProtocolGenerateContent, "$.contents.parts.functionResponse.name", "function response %q has no unambiguous preceding function call", name)
}

func geminiPartsToMessages(parts []geminiPart, role, path string, tracker *geminiCallTracker) ([]messagesBlock, []Diagnostic, bool, error) {
	blocks := make([]messagesBlock, 0, len(parts))
	var diagnostics []Diagnostic
	hasToolCall := false
	for index, part := range parts {
		partPath := fmt.Sprintf("%s[%d]", path, index)
		if err := validateGeminiPart(part, partPath); err != nil {
			return nil, diagnostics, hasToolCall, err
		}
		if part.Thought {
			return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath, "Gemini thoughts and signatures are not semantically equivalent to Anthropic thinking blocks")
		}
		if part.ThoughtSignature != "" {
			return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".thoughtSignature", "provider-issued Gemini thought signatures cannot be represented by Messages and must not be dropped")
		}
		switch {
		case part.FunctionCall != nil:
			if role != "assistant" {
				return nil, diagnostics, hasToolCall, invalid(ProtocolGenerateContent, partPath, "functionCall parts require the model role")
			}
			if part.FunctionCall.Name == "" {
				return nil, diagnostics, hasToolCall, invalid(ProtocolGenerateContent, partPath+".functionCall.name", "name is required")
			}
			arguments, err := normalizeGeminiToolArguments(ProtocolGenerateContent, partPath+".functionCall.args", part.FunctionCall.Args)
			if err != nil {
				return nil, diagnostics, hasToolCall, err
			}
			id := part.FunctionCall.ID
			generated := false
			if tracker != nil {
				id, generated, err = tracker.add(id, part.FunctionCall.Name)
				if err != nil {
					return nil, diagnostics, hasToolCall, err
				}
			} else if id == "" {
				id = fmt.Sprintf("call_rm_%d", index+1)
				generated = true
			}
			if generated {
				diagnostics = appendDiagnostic(diagnostics, "warning", "function_call_id_generated", partPath+".functionCall.id", "Messages requires a tool_use id; RouteMorph generated one")
			}
			blocks = append(blocks, messagesBlock{Type: "tool_use", ID: id, Name: part.FunctionCall.Name, Input: arguments})
			hasToolCall = true
		case part.FunctionResponse != nil:
			if role != "user" {
				return nil, diagnostics, hasToolCall, invalid(ProtocolGenerateContent, partPath, "functionResponse parts require the user role")
			}
			if tracker == nil {
				return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath, "functionResponse is not valid in an assistant response")
			}
			if part.FunctionResponse.Name == "" {
				return nil, diagnostics, hasToolCall, invalid(ProtocolGenerateContent, partPath+".functionResponse.name", "name is required")
			}
			callID, err := tracker.resolve(part.FunctionResponse.ID, part.FunctionResponse.Name)
			if err != nil {
				return nil, diagnostics, hasToolCall, err
			}
			content, err := geminiFunctionResponseToMessages(part.FunctionResponse.Response, partPath+".functionResponse.response")
			if err != nil {
				return nil, diagnostics, hasToolCall, err
			}
			blocks = append(blocks, messagesBlock{Type: "tool_result", ToolUseID: callID, Content: content})
		case part.InlineData != nil:
			if role == "assistant" {
				return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".inlineData", "multimodal Gemini model output has no valid Messages assistant-content mapping")
			}
			if strings.HasPrefix(part.InlineData.MIMEType, "audio/") {
				return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".inlineData", "Messages has no portable audio content block")
			}
			blockType := "document"
			if strings.HasPrefix(part.InlineData.MIMEType, "image/") {
				if !validMessagesImageMediaType(part.InlineData.MIMEType) {
					return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".inlineData.mimeType", "Messages supports only JPEG, PNG, GIF, or WebP images")
				}
				blockType = "image"
			} else if part.InlineData.MIMEType != "application/pdf" {
				return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".inlineData.mimeType", "Messages documents support only application/pdf")
			}
			block := messagesBlock{Type: blockType}
			block.Source = &struct {
				Type      string          `json:"type"`
				MediaType string          `json:"media_type,omitempty"`
				Data      string          `json:"data,omitempty"`
				URL       string          `json:"url,omitempty"`
				FileID    string          `json:"file_id,omitempty"`
				Content   json.RawMessage `json:"content,omitempty"`
			}{Type: "base64", MediaType: part.InlineData.MIMEType, Data: part.InlineData.Data}
			blocks = append(blocks, block)
		case part.FileData != nil:
			if role == "assistant" {
				return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".fileData", "multimodal Gemini model output has no valid Messages assistant-content mapping")
			}
			if strings.HasPrefix(part.FileData.MIMEType, "audio/") {
				return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".fileData", "Messages has no portable audio content block")
			}
			if !portableGeminiFileURI(part.FileData.FileURI) {
				return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".fileData.fileUri", "provider-scoped Gemini file URIs cannot be represented as Messages URLs")
			}
			blockType := "document"
			if strings.HasPrefix(part.FileData.MIMEType, "image/") {
				if !validMessagesImageMediaType(part.FileData.MIMEType) {
					return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".fileData.mimeType", "Messages supports only JPEG, PNG, GIF, or WebP images")
				}
				blockType = "image"
			} else if part.FileData.MIMEType != "application/pdf" {
				return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath+".fileData.mimeType", "Messages documents support only application/pdf")
			}
			block := messagesBlock{Type: blockType}
			block.Source = &struct {
				Type      string          `json:"type"`
				MediaType string          `json:"media_type,omitempty"`
				Data      string          `json:"data,omitempty"`
				URL       string          `json:"url,omitempty"`
				FileID    string          `json:"file_id,omitempty"`
				Content   json.RawMessage `json:"content,omitempty"`
			}{Type: "url", URL: part.FileData.FileURI}
			blocks = append(blocks, block)
		case part.Text != "":
			blocks = append(blocks, messagesBlock{Type: "text", Text: part.Text})
		default:
			return nil, diagnostics, hasToolCall, unsupported(ProtocolGenerateContent, partPath, "unknown or empty Gemini part")
		}
	}
	return blocks, diagnostics, hasToolCall, nil
}

func geminiFunctionResponseToMessages(raw json.RawMessage, path string) (json.RawMessage, error) {
	if !jsonValuePresent(raw) {
		return nil, invalid(ProtocolGenerateContent, path, "response is required")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, invalid(ProtocolGenerateContent, path, "response must be valid JSON: %v", err)
	}
	if object, ok := value.(map[string]any); ok && len(object) == 1 {
		if unwrapped, exists := object["output"]; exists {
			value = unwrapped
		}
	}
	if text, ok := value.(string); ok {
		return mustJSON(text), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, invalid(ProtocolGenerateContent, path, "cannot encode function response: %v", err)
	}
	return mustJSON(string(encoded)), nil
}

func messagesGeminiSchemaToJSONSchema(raw json.RawMessage, path string) (json.RawMessage, error) {
	if !jsonValuePresent(raw) {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, invalid(ProtocolGenerateContent, path, "schema must be valid JSON: %v", err)
	}
	converted, err := convertMessagesGeminiSchemaNode(value, path, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}

func convertMessagesGeminiSchemaNode(value any, path string, depth int) (any, error) {
	if depth >= 64 {
		return nil, unsupported(ProtocolGenerateContent, path, "JSON schema exceeds the supported nesting depth")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, invalid(ProtocolGenerateContent, path, "schema must be an object")
	}
	converted := make(map[string]any, len(object))
	for key, child := range object {
		converted[key] = child
	}
	if rawType, exists := converted["type"]; exists {
		switch typed := rawType.(type) {
		case string:
			converted["type"] = strings.ToLower(typed)
		case []any:
			values := make([]any, len(typed))
			for index, item := range typed {
				text, ok := item.(string)
				if !ok {
					return nil, invalid(ProtocolGenerateContent, path+".type", "type array must contain strings")
				}
				values[index] = strings.ToLower(text)
			}
			converted["type"] = values
		default:
			return nil, invalid(ProtocolGenerateContent, path+".type", "type must be a string or string array")
		}
	}
	if properties, exists := converted["properties"]; exists {
		propertyMap, ok := properties.(map[string]any)
		if !ok {
			return nil, invalid(ProtocolGenerateContent, path+".properties", "must be an object")
		}
		cleaned := make(map[string]any, len(propertyMap))
		for name, property := range propertyMap {
			child, err := convertMessagesGeminiSchemaNode(property, path+".properties."+name, depth+1)
			if err != nil {
				return nil, err
			}
			cleaned[name] = child
		}
		converted["properties"] = cleaned
	}
	if items, exists := converted["items"]; exists {
		child, err := convertMessagesGeminiSchemaNode(items, path+".items", depth+1)
		if err != nil {
			return nil, err
		}
		converted["items"] = child
	}
	if anyOf, exists := converted["anyOf"]; exists {
		values, ok := anyOf.([]any)
		if !ok {
			return nil, invalid(ProtocolGenerateContent, path+".anyOf", "must be an array")
		}
		cleaned := make([]any, 0, len(values)+1)
		for index, item := range values {
			child, err := convertMessagesGeminiSchemaNode(item, fmt.Sprintf("%s.anyOf[%d]", path, index), depth+1)
			if err != nil {
				return nil, err
			}
			cleaned = append(cleaned, child)
		}
		converted["anyOf"] = cleaned
	}
	if nullable, exists := converted["nullable"]; exists {
		enabled, ok := nullable.(bool)
		if !ok {
			return nil, invalid(ProtocolGenerateContent, path+".nullable", "must be a boolean")
		}
		delete(converted, "nullable")
		if enabled {
			switch typed := converted["type"].(type) {
			case string:
				converted["type"] = []any{typed, "null"}
			case []any:
				seenNull := false
				for _, item := range typed {
					seenNull = seenNull || item == "null"
				}
				if !seenNull {
					converted["type"] = append(typed, "null")
				}
			default:
				values, _ := converted["anyOf"].([]any)
				converted["anyOf"] = append(values, map[string]any{"type": "null"})
			}
		}
	}
	return converted, nil
}
