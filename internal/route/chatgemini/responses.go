package chatgemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (c *chatToGeminiConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source geminiResponse
	if err := decodeJSON(ProtocolGenerateContent, input, &source); err != nil {
		return conversionResult{}, err
	}
	diagnostics, err := validateGeminiResponseEnvelope(&source, options.LossPolicy)
	if err != nil {
		return conversionResult{}, err
	}
	candidate := source.Candidates[0]
	message := chatMessage{Role: "assistant", Content: json.RawMessage(`null`)}
	var text strings.Builder
	generatedCallID := 0
	for index, part := range candidate.Content.Parts {
		path := fmt.Sprintf("$.candidates[0].content.parts[%d]", index)
		if err := validateGeminiPart(part, path); err != nil {
			return conversionResult{}, err
		}
		switch {
		case part.FunctionCall != nil:
			if err := checkGeminiSignature(part.ThoughtSignature, path+".thoughtSignature", options.LossPolicy, &diagnostics); err != nil {
				return conversionResult{}, err
			}
			arguments, err := normalizeGeminiToolArguments(ProtocolGenerateContent, path+".functionCall.args", part.FunctionCall.Args)
			if err != nil {
				return conversionResult{}, err
			}
			callID := part.FunctionCall.ID
			if callID == "" {
				generatedCallID++
				callID = fmt.Sprintf("rm_call_%d", generatedCallID)
				diagnostics = appendDiagnostic(diagnostics, "warning", "generated_function_call_id", path+".functionCall.id", "Gemini omitted the optional function call id; RouteMorph generated a response-local id")
			}
			var call chatToolCall
			call.ID, call.Type, call.Function.Name = callID, "function", part.FunctionCall.Name
			call.Function.Arguments = json.RawMessage(mustJSONString(string(arguments)))
			message.ToolCalls = append(message.ToolCalls, call)
		case part.FunctionResponse != nil:
			return conversionResult{}, unsupported(ProtocolGenerateContent, path, "functionResponse is not a valid model output part")
		case part.Thought:
			if err := checkGeminiSignature(part.ThoughtSignature, path+".thoughtSignature", options.LossPolicy, &diagnostics); err != nil {
				return conversionResult{}, err
			}
			message.ReasoningContent += part.Text
		default:
			if err := checkGeminiSignature(part.ThoughtSignature, path+".thoughtSignature", options.LossPolicy, &diagnostics); err != nil {
				return conversionResult{}, err
			}
			if part.InlineData != nil || part.FileData != nil {
				return conversionResult{}, unsupported(ProtocolGenerateContent, path, "multimodal Gemini model output has no valid Chat assistant-content mapping")
			}
			if part.Text == "" {
				return conversionResult{}, unsupported(ProtocolGenerateContent, path, "Gemini model output part is not representable by Chat")
			}
			text.WriteString(part.Text)
		}
	}
	if text.Len() > 0 {
		message.Content = json.RawMessage(mustJSONString(text.String()))
	}
	finish, err := parseGeminiFinish(candidate.FinishReason)
	if err != nil {
		return conversionResult{}, err
	}
	if finish == finishStop && len(message.ToolCalls) > 0 {
		finish = finishToolCalls
	}
	var target chatResponse
	target.ID, target.Object, target.Model = source.ResponseID, "chat.completion", source.ModelVersion
	if options.Exchange.ClientModel != "" {
		target.Model = options.Exchange.ClientModel
	}
	target.Choices = append(target.Choices, struct {
		Index        int             `json:"index"`
		Message      chatMessage     `json:"message"`
		FinishReason string          `json:"finish_reason"`
		Logprobs     json.RawMessage `json:"logprobs,omitempty"`
	}{Index: int(candidate.Index), Message: message, FinishReason: string(finish)})
	target.Usage.PromptTokens = source.UsageMetadata.PromptTokenCount + source.UsageMetadata.ToolUsePromptTokenCount
	target.Usage.CompletionTokens = source.UsageMetadata.CandidatesTokenCount + source.UsageMetadata.ThoughtsTokenCount
	target.Usage.TotalTokens = source.UsageMetadata.TotalTokenCount
	target.Usage.PromptDetails.CachedTokens = source.UsageMetadata.CachedContentTokenCount
	target.Usage.CompletionDetails.ReasoningTokens = source.UsageMetadata.ThoughtsTokenCount
	body, err := marshal(ProtocolChat, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *geminiToChatConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source chatResponse
	if err := decodeJSON(ProtocolChat, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Error != nil {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.error", "%s", source.Error.Message)
	}
	if len(source.Choices) != 1 {
		return conversionResult{}, unsupported(ProtocolChat, "$.choices", "Gemini conversion requires exactly one Chat choice")
	}
	choice := source.Choices[0]
	var diagnostics []Diagnostic
	if jsonValuePresent(choice.Logprobs) {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolChat, "$.choices[0].logprobs", "Gemini logprobs require token ids that Chat does not provide")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "chat_logprobs_not_representable", "$.choices[0].logprobs", "Chat log probabilities were omitted because Gemini requires token ids")
	}
	if choice.Message.Role != "" && choice.Message.Role != "assistant" {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.choices[0].message.role", "expected assistant role, got %q", choice.Message.Role)
	}
	parts, err := chatContentToGeminiParts(choice.Message.Content, "$.choices[0].message.content")
	if err != nil {
		return conversionResult{}, err
	}
	if choice.Message.Refusal != "" {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolChat, "$.choices[0].message.refusal", "Gemini cannot preserve a structured refusal")
		}
		parts = append(parts, geminiPart{Text: choice.Message.Refusal})
		diagnostics = appendDiagnostic(diagnostics, "warning", "chat_refusal_approximated", "$.choices[0].message.refusal", "structured Chat refusal was emitted as Gemini text")
	}
	signatureAttached := false
	if choice.Message.ReasoningContent != "" {
		parts = append([]geminiPart{{Text: choice.Message.ReasoningContent, Thought: true, ThoughtSignature: geminiThoughtSignatureBypass}}, parts...)
		signatureAttached = true
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_thought_signature_bypass_added", "$.choices[0].message.reasoning_content", "a Gemini-compatible thought signature bypass was attached to converted reasoning")
	}
	for index, call := range choice.Message.ToolCalls {
		path := fmt.Sprintf("$.choices[0].message.tool_calls[%d]", index)
		if call.Type != "" && call.Type != "function" {
			return conversionResult{}, unsupported(ProtocolChat, path+".type", "tool call type %q is unsupported", call.Type)
		}
		if call.ID == "" || call.Function.Name == "" {
			return conversionResult{}, upstreamResponseError(ProtocolChat, path, "tool call id and function name are required")
		}
		arguments, err := normalizeOpenAIToolArguments(ProtocolChat, path+".function.arguments", call.Function.Arguments)
		if err != nil {
			return conversionResult{}, err
		}
		part := geminiPart{FunctionCall: &geminiFunctionCall{ID: call.ID, Name: call.Function.Name, Args: arguments}}
		if !signatureAttached {
			part.ThoughtSignature = geminiThoughtSignatureBypass
			signatureAttached = true
			diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_thought_signature_bypass_added", path, "a Gemini-compatible thought signature bypass was attached to the converted function call")
		}
		parts = append(parts, part)
	}
	finish, err := parseChatFinish(choice.FinishReason)
	if err != nil {
		return conversionResult{}, err
	}
	var target geminiResponse
	target.ResponseID, target.ModelVersion = source.ID, source.Model
	if options.Exchange.ClientModel != "" {
		target.ModelVersion = options.Exchange.ClientModel
	}
	target.Candidates = append(target.Candidates, geminiCandidate{
		Index: int64(choice.Index), Content: geminiContent{Role: "model", Parts: parts}, FinishReason: geminiStop(finish),
	})
	target.UsageMetadata.PromptTokenCount = source.Usage.PromptTokens
	target.UsageMetadata.CandidatesTokenCount = source.Usage.CompletionTokens - source.Usage.CompletionDetails.ReasoningTokens
	if target.UsageMetadata.CandidatesTokenCount < 0 {
		target.UsageMetadata.CandidatesTokenCount = 0
	}
	target.UsageMetadata.ThoughtsTokenCount = source.Usage.CompletionDetails.ReasoningTokens
	target.UsageMetadata.TotalTokenCount = source.Usage.TotalTokens
	target.UsageMetadata.CachedContentTokenCount = source.Usage.PromptDetails.CachedTokens
	body, err := marshal(ProtocolGenerateContent, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *chatToGeminiConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return c.buffered(c.spec, options, c.ToClientResponse), nil
}

func (c *geminiToChatConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return c.buffered(c.spec, options, c.ToClientResponse), nil
}

func emptyGeminiGenerationConfig(config *geminiGenerationConfig) bool {
	return config == nil || (config.MaxOutputTokens == nil && config.Temperature == nil && config.TopP == nil &&
		config.CandidateCount == nil && len(config.StopSequences) == 0 && config.ResponseMIMEType == "" &&
		!jsonValuePresent(config.ResponseSchema) && !jsonValuePresent(config.ResponseJSONSchema) &&
		config.PresencePenalty == nil && config.FrequencyPenalty == nil && config.ResponseLogprobs == nil &&
		config.Logprobs == nil && config.ThinkingConfig == nil)
}

func geminiToolConfigForChoice(choice toolChoice) *geminiToolConfig {
	config := &geminiToolConfig{}
	switch choice.Mode {
	case toolChoiceAuto:
		config.FunctionCallingConfig.Mode = "AUTO"
	case toolChoiceNone:
		config.FunctionCallingConfig.Mode = "NONE"
	case toolChoiceRequired:
		config.FunctionCallingConfig.Mode = "ANY"
	case toolChoiceNamed:
		config.FunctionCallingConfig.Mode = "ANY"
		config.FunctionCallingConfig.AllowedFunctionNames = []string{choice.Name}
	}
	return config
}

func chatContentToGeminiParts(raw json.RawMessage, path string) ([]geminiPart, error) {
	parts, err := decodeChatContent(raw, path)
	if err != nil {
		return nil, err
	}
	return encodeGeminiParts(parts)
}

func chatToolResultToGemini(raw json.RawMessage, path string) (json.RawMessage, bool, error) {
	parts, err := decodeChatContent(raw, path)
	if err != nil {
		return nil, false, err
	}
	for index, part := range parts {
		if part.Kind != partText {
			return nil, false, unsupported(ProtocolChat, fmt.Sprintf("%s[%d]", path, index), "Gemini functionResponse cannot preserve multimodal Chat tool content")
		}
	}
	text := joinText(parts)
	var value any
	if json.Unmarshal([]byte(text), &value) == nil {
		if _, ok := value.(map[string]any); ok {
			return json.RawMessage(text), false, nil
		}
		return mustJSON(map[string]any{"result": value}), true, nil
	}
	return mustJSON(map[string]any{"result": text}), true, nil
}

func geminiConfigToChat(config *geminiGenerationConfig, target *chatRequest, policy lossPolicy, diagnostics *[]Diagnostic) error {
	if config == nil {
		return nil
	}
	for _, field := range []struct {
		path string
		set  bool
	}{
		{"$.generationConfig.topK", config.TopK != nil},
		{"$.generationConfig.enableEnhancedCivicAnswers", config.EnableEnhancedCivicAnswers != nil},
		{"$.generationConfig.mediaResolution", jsonValuePresent(config.MediaResolution)},
		{"$.generationConfig.seed", config.Seed != nil},
		{"$.generationConfig.responseModalities", len(config.ResponseModalities) > 0},
		{"$.generationConfig.speechConfig", jsonValuePresent(config.SpeechConfig)},
		{"$.generationConfig.imageConfig", jsonValuePresent(config.ImageConfig)},
	} {
		if field.set {
			return unsupported(ProtocolGenerateContent, field.path, "generation setting has no Chat equivalent")
		}
	}
	if config.CandidateCount != nil && *config.CandidateCount != 1 {
		return unsupported(ProtocolGenerateContent, "$.generationConfig.candidateCount", "Chat cross-protocol conversion supports exactly one choice")
	}
	if config.Logprobs != nil && (config.ResponseLogprobs == nil || !*config.ResponseLogprobs) {
		return invalid(ProtocolGenerateContent, "$.generationConfig.logprobs", "logprobs requires responseLogprobs=true")
	}
	if (config.ResponseLogprobs != nil && *config.ResponseLogprobs) || config.Logprobs != nil {
		if policy == rejectSemanticLoss {
			return unsupported(ProtocolGenerateContent, "$.generationConfig.responseLogprobs", "Chat does not provide the token ids required by Gemini logprobs")
		}
		*diagnostics = appendDiagnostic(*diagnostics, "warning", "chat_logprobs_partially_representable", "$.generationConfig.responseLogprobs", "Chat log probabilities will be omitted from the Gemini response because token ids are unavailable")
	}
	if jsonValuePresent(config.ResponseSchema) && jsonValuePresent(config.ResponseJSONSchema) {
		return invalid(ProtocolGenerateContent, "$.generationConfig", "responseSchema and responseJsonSchema are mutually exclusive")
	}
	if config.ResponseMIMEType != "" && config.ResponseMIMEType != "text/plain" && config.ResponseMIMEType != "application/json" {
		return unsupported(ProtocolGenerateContent, "$.generationConfig.responseMimeType", "MIME type %q has no Chat equivalent", config.ResponseMIMEType)
	}
	target.MaxCompletion, target.Temperature, target.TopP = config.MaxOutputTokens, config.Temperature, config.TopP
	target.FrequencyPenalty, target.PresencePenalty = config.FrequencyPenalty, config.PresencePenalty
	target.Logprobs, target.TopLogprobs = config.ResponseLogprobs, config.Logprobs
	if config.CandidateCount != nil {
		target.N = config.CandidateCount
	}
	if len(config.StopSequences) == 1 {
		target.Stop = mustJSON(config.StopSequences[0])
	} else if len(config.StopSequences) > 1 {
		target.Stop = mustJSON(config.StopSequences)
	}
	if config.ThinkingConfig != nil {
		if config.ThinkingConfig.ThinkingBudget != nil {
			return unsupported(ProtocolGenerateContent, "$.generationConfig.thinkingConfig.thinkingBudget", "Chat reasoning_effort cannot preserve an exact token budget")
		}
		if config.ThinkingConfig.IncludeThoughts {
			return unsupported(ProtocolGenerateContent, "$.generationConfig.thinkingConfig.includeThoughts", "Chat has no equivalent request control for returning thoughts")
		}
		target.ReasoningEffort = strings.ToLower(config.ThinkingConfig.ThinkingLevel)
	}
	schema := config.ResponseJSONSchema
	if jsonValuePresent(schema) {
		target.ResponseFormat = mustJSON(map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "response", "schema": schema}})
	} else if jsonValuePresent(config.ResponseSchema) {
		converted, err := chatGeminiSchemaToJSONSchema(config.ResponseSchema, "$.generationConfig.responseSchema")
		if err != nil {
			return err
		}
		target.ResponseFormat = mustJSON(map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "response", "schema": converted}})
	} else if config.ResponseMIMEType == "application/json" {
		target.ResponseFormat = mustJSON(map[string]any{"type": "json_object"})
	}
	return nil
}

func geminiToolChoiceToChat(config *geminiToolConfig, declared map[string]struct{}) (toolChoice, error) {
	if config == nil {
		return toolChoice{}, nil
	}
	if jsonValuePresent(config.RetrievalConfig) || config.IncludeServerSideToolInvocations != nil {
		return toolChoice{}, unsupported(ProtocolGenerateContent, "$.toolConfig", "retrieval and server-side invocation settings have no Chat equivalent")
	}
	mode := config.FunctionCallingConfig.Mode
	names := config.FunctionCallingConfig.AllowedFunctionNames
	for index, name := range names {
		if _, ok := declared[name]; !ok {
			return toolChoice{}, invalid(ProtocolGenerateContent, fmt.Sprintf("$.toolConfig.functionCallingConfig.allowedFunctionNames[%d]", index), "function %q is not declared", name)
		}
	}
	switch mode {
	case "", "AUTO":
		if len(names) > 0 {
			return toolChoice{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "Chat cannot restrict AUTO to a subset of functions")
		}
		return toolChoice{Mode: toolChoiceAuto}, nil
	case "NONE":
		if len(names) > 0 {
			return toolChoice{}, invalid(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "NONE cannot include allowed function names")
		}
		return toolChoice{Mode: toolChoiceNone}, nil
	case "ANY":
		if len(names) == 1 {
			return toolChoice{Mode: toolChoiceNamed, Name: names[0]}, nil
		}
		if len(names) > 1 {
			allowed := make(map[string]struct{}, len(names))
			for _, name := range names {
				allowed[name] = struct{}{}
			}
			if len(allowed) != len(declared) {
				return toolChoice{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "Chat cannot express a required subset of multiple functions")
			}
			for name := range declared {
				if _, ok := allowed[name]; !ok {
					return toolChoice{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "Chat cannot express a required subset of multiple functions")
				}
			}
		}
		return toolChoice{Mode: toolChoiceRequired}, nil
	case "VALIDATED":
		return toolChoice{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.mode", "VALIDATED has no exact Chat tool_choice equivalent")
	default:
		return toolChoice{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.mode", "mode %q has no Chat equivalent", mode)
	}
}

func geminiPartsToChat(source []geminiPart, path, role string, policy lossPolicy, generatedCallID *int, diagnostics *[]Diagnostic) ([]portablePart, []portableToolCall, error) {
	parts := make([]portablePart, 0, len(source))
	calls := make([]portableToolCall, 0)
	for index, part := range source {
		partPath := fmt.Sprintf("%s[%d]", path, index)
		if err := validateGeminiPart(part, partPath); err != nil {
			return nil, nil, err
		}
		switch {
		case part.FunctionCall != nil:
			if role != "model" {
				return nil, nil, invalid(ProtocolGenerateContent, partPath+".functionCall", "functionCall is only valid in model content")
			}
			if strings.TrimSpace(part.FunctionCall.Name) == "" {
				return nil, nil, invalid(ProtocolGenerateContent, partPath+".functionCall.name", "name is required")
			}
			if err := checkGeminiSignature(part.ThoughtSignature, partPath+".thoughtSignature", policy, diagnostics); err != nil {
				return nil, nil, err
			}
			arguments, err := normalizeGeminiToolArguments(ProtocolGenerateContent, partPath+".functionCall.args", part.FunctionCall.Args)
			if err != nil {
				return nil, nil, err
			}
			callID := part.FunctionCall.ID
			if callID == "" {
				if generatedCallID == nil {
					return nil, nil, unsupported(ProtocolGenerateContent, partPath+".functionCall.id", "Chat requires a function call id")
				}
				(*generatedCallID)++
				callID = fmt.Sprintf("rm_call_%d", *generatedCallID)
				if diagnostics != nil {
					*diagnostics = appendDiagnostic(*diagnostics, "warning", "generated_function_call_id", partPath+".functionCall.id", "Gemini omitted the optional function call id; RouteMorph generated a request-local id")
				}
			}
			calls = append(calls, portableToolCall{ID: callID, Name: part.FunctionCall.Name, Arguments: arguments})
		case part.FunctionResponse != nil:
			return nil, nil, invalid(ProtocolGenerateContent, partPath+".functionResponse", "functionResponse must be converted as a standalone tool message")
		case part.Thought:
			if role != "model" {
				return nil, nil, invalid(ProtocolGenerateContent, partPath+".thought", "thought is only valid in model content")
			}
			if err := checkGeminiSignature(part.ThoughtSignature, partPath+".thoughtSignature", policy, diagnostics); err != nil {
				return nil, nil, err
			}
			parts = append(parts, portablePart{Kind: partReasoning, Text: part.Text})
		default:
			if err := checkGeminiSignature(part.ThoughtSignature, partPath+".thoughtSignature", policy, diagnostics); err != nil {
				return nil, nil, err
			}
			converted, err := geminiMediaOrTextToChatPart(part, partPath)
			if err != nil {
				return nil, nil, err
			}
			parts = append(parts, converted)
		}
	}
	return parts, calls, nil
}

func geminiMediaOrTextToChatPart(part geminiPart, path string) (portablePart, error) {
	switch {
	case part.Text != "":
		return portablePart{Kind: partText, Text: part.Text}, nil
	case part.InlineData != nil:
		mime := strings.ToLower(part.InlineData.MIMEType)
		if strings.HasPrefix(mime, "image/") {
			return portablePart{Kind: partImage, Media: &portableMedia{MIMEType: part.InlineData.MIMEType, Data: part.InlineData.Data}}, nil
		}
		if mime == "audio/wav" || mime == "audio/mpeg" || mime == "audio/mp3" {
			return portablePart{Kind: partAudio, Media: &portableMedia{MIMEType: part.InlineData.MIMEType, Data: part.InlineData.Data}}, nil
		}
		return portablePart{}, unsupported(ProtocolGenerateContent, path+".inlineData.mimeType", "Chat cannot represent inline media type %q", part.InlineData.MIMEType)
	case part.FileData != nil:
		if !strings.HasPrefix(strings.ToLower(part.FileData.MIMEType), "image/") {
			return portablePart{}, unsupported(ProtocolGenerateContent, path+".fileData", "Chat can only preserve Gemini fileData as an image URL")
		}
		return portablePart{Kind: partImage, Media: &portableMedia{URL: part.FileData.FileURI}}, nil
	default:
		return portablePart{}, unsupported(ProtocolGenerateContent, path, "part has no Chat equivalent")
	}
}

func removeFirstString(values []string, target string) []string {
	for index, value := range values {
		if value == target {
			return append(values[:index], values[index+1:]...)
		}
	}
	return values
}

func chatGeminiSchemaToJSONSchema(raw json.RawMessage, path string) (json.RawMessage, error) {
	if !jsonValuePresent(raw) {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, invalid(ProtocolGenerateContent, path, "invalid schema: %v", err)
	}
	converted, err := chatGeminiSchemaValueToJSON(value, path, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}

func chatGeminiSchemaValueToJSON(value any, path string, depth int) (any, error) {
	if depth >= 64 {
		return nil, unsupported(ProtocolGenerateContent, path, "schema exceeds supported nesting depth")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, invalid(ProtocolGenerateContent, path, "schema must be a JSON object")
	}
	result := make(map[string]any, len(object))
	for key, child := range object {
		switch key {
		case "properties":
			properties, ok := child.(map[string]any)
			if !ok {
				return nil, invalid(ProtocolGenerateContent, path+".properties", "must be an object")
			}
			converted := make(map[string]any, len(properties))
			for name, property := range properties {
				item, err := chatGeminiSchemaValueToJSON(property, path+".properties."+name, depth+1)
				if err != nil {
					return nil, err
				}
				converted[name] = item
			}
			result[key] = converted
		case "items":
			item, err := chatGeminiSchemaValueToJSON(child, path+".items", depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = item
		case "anyOf":
			items, ok := child.([]any)
			if !ok {
				return nil, invalid(ProtocolGenerateContent, path+".anyOf", "must be an array")
			}
			converted := make([]any, 0, len(items))
			for index, item := range items {
				child, err := chatGeminiSchemaValueToJSON(item, fmt.Sprintf("%s.anyOf[%d]", path, index), depth+1)
				if err != nil {
					return nil, err
				}
				converted = append(converted, child)
			}
			result[key] = converted
		case "type":
			switch typed := child.(type) {
			case string:
				result[key] = strings.ToLower(typed)
			case []any:
				values := make([]any, 0, len(typed))
				for _, item := range typed {
					text, ok := item.(string)
					if !ok {
						return nil, invalid(ProtocolGenerateContent, path+".type", "type array must contain strings")
					}
					values = append(values, strings.ToLower(text))
				}
				result[key] = values
			default:
				return nil, invalid(ProtocolGenerateContent, path+".type", "must be a string or string array")
			}
		case "nullable":
			// Applied after all ordinary keys are copied.
		default:
			result[key] = child
		}
	}
	if nullableValue, present := object["nullable"]; present {
		nullable, ok := nullableValue.(bool)
		if !ok {
			return nil, invalid(ProtocolGenerateContent, path+".nullable", "must be a boolean")
		}
		if !nullable {
			return result, nil
		}
		switch typed := result["type"].(type) {
		case string:
			result["type"] = []any{typed, "null"}
		case []any:
			found := false
			for _, item := range typed {
				found = found || item == "null"
			}
			if !found {
				result["type"] = append(typed, "null")
			}
		case nil:
			return nil, unsupported(ProtocolGenerateContent, path+".nullable", "nullable schema without a type has no exact JSON Schema translation")
		}
	}
	return result, nil
}
