package chatgemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// chatToGeminiConverter and geminiToChatConverter deliberately translate the
// two native payload families directly. Routing either direction through the
// Responses API loses stop sequences and makes tool-result correlation and
// signed Gemini thought handling needlessly ambiguous.
type chatToGeminiConverter struct {
	spec     routeSpec
	buffered BufferedFactory
}
type geminiToChatConverter struct {
	spec     routeSpec
	buffered BufferedFactory
}

func (c *chatToGeminiConverter) Specification() routeSpec { return c.spec }
func (c *geminiToChatConverter) Specification() routeSpec { return c.spec }

func (c *chatToGeminiConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolChat, input,
		"model", "messages", "tools", "tool_choice", "max_tokens", "max_completion_tokens",
		"temperature", "top_p", "stop", "stream", "parallel_tool_calls", "response_format",
		"reasoning_effort", "metadata", "n", "frequency_penalty", "presence_penalty",
		"logprobs", "top_logprobs", "verbosity", "user", "service_tier", "store",
		"prompt_cache_key", "prompt_cache_retention", "safety_identifier", "stream_options"); err != nil {
		return conversionResult{}, err
	}
	var source chatRequest
	if err := decodeJSON(ProtocolChat, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Model == "" {
		return conversionResult{}, invalid(ProtocolChat, "$.model", "model is required")
	}
	if len(source.Messages) == 0 {
		return conversionResult{}, invalid(ProtocolChat, "$.messages", "at least one message is required")
	}
	if source.MaxTokens != nil && source.MaxCompletion != nil {
		return conversionResult{}, invalid(ProtocolChat, "$.max_tokens", "max_tokens and max_completion_tokens are mutually exclusive")
	}
	if source.N != nil && *source.N != 1 {
		return conversionResult{}, unsupported(ProtocolChat, "$.n", "Gemini cross-protocol conversion supports exactly one candidate")
	}
	if source.ParallelToolCalls != nil {
		return conversionResult{}, unsupported(ProtocolChat, "$.parallel_tool_calls", "Gemini has no equivalent parallel tool-call control")
	}
	for path, present := range map[string]bool{
		"$.metadata":               len(source.Metadata) > 0,
		"$.verbosity":              rawJSONValuePresent(source.Verbosity),
		"$.user":                   rawJSONValuePresent(source.User),
		"$.service_tier":           rawJSONValuePresent(source.ServiceTier),
		"$.store":                  rawJSONValuePresent(source.Store),
		"$.prompt_cache_key":       rawJSONValuePresent(source.PromptCacheKey),
		"$.prompt_cache_retention": rawJSONValuePresent(source.PromptCacheRetention),
		"$.safety_identifier":      rawJSONValuePresent(source.SafetyIdentifier),
	} {
		if present {
			return conversionResult{}, unsupported(ProtocolChat, path, "field has no Gemini equivalent")
		}
	}
	if source.TopLogprobs != nil && (source.Logprobs == nil || !*source.Logprobs) {
		return conversionResult{}, invalid(ProtocolChat, "$.top_logprobs", "top_logprobs requires logprobs=true")
	}
	var diagnostics []Diagnostic
	if (source.Logprobs != nil && *source.Logprobs) || source.TopLogprobs != nil {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolChat, "$.logprobs", "Gemini token ids and average log probability cannot be represented by Chat without loss")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_logprobs_partially_representable", "$.logprobs", "Gemini token ids and average log probability will be omitted from the Chat response")
	}
	if err := validateChatStreamOptions(input); err != nil {
		return conversionResult{}, err
	}
	for index, message := range source.Messages {
		if rawJSONValuePresent(message.Audio) {
			return conversionResult{}, unsupported(ProtocolChat, fmt.Sprintf("$.messages[%d].audio", index), "Chat assistant audio cannot be represented by Gemini input")
		}
	}

	stop, err := decodeStop(ProtocolChat, source.Stop)
	if err != nil {
		return conversionResult{}, err
	}
	target := geminiRequest{}
	config := &geminiGenerationConfig{
		MaxOutputTokens:  source.MaxCompletion,
		Temperature:      source.Temperature,
		TopP:             source.TopP,
		StopSequences:    stop,
		PresencePenalty:  source.PresencePenalty,
		FrequencyPenalty: source.FrequencyPenalty,
		ResponseLogprobs: source.Logprobs,
		Logprobs:         source.TopLogprobs,
	}
	if config.MaxOutputTokens == nil {
		config.MaxOutputTokens = source.MaxTokens
	}
	if source.N != nil {
		config.CandidateCount = source.N
	}
	if source.ReasoningEffort != "" {
		level, err := normalizeGeminiThinkingLevel(ProtocolChat, "$.reasoning_effort", source.ReasoningEffort)
		if err != nil {
			return conversionResult{}, err
		}
		config.ThinkingConfig = &geminiThinkingConfig{ThinkingLevel: level}
	}
	var responseFormat struct {
		Type string `json:"type"`
	}
	if jsonValuePresent(source.ResponseFormat) {
		if err := json.Unmarshal(source.ResponseFormat, &responseFormat); err != nil {
			return conversionResult{}, invalid(ProtocolChat, "$.response_format", "invalid response format")
		}
	}
	if responseFormat.Type == "json_object" {
		config.ResponseMIMEType = "application/json"
	} else {
		format, err := decodeChatResponseFormat(source.ResponseFormat)
		if err != nil {
			return conversionResult{}, err
		}
		if format != nil {
			if format.Name != "" || format.Description != "" || format.Strict != nil {
				if options.LossPolicy == rejectSemanticLoss {
					return conversionResult{}, unsupported(ProtocolChat, "$.response_format.json_schema", "Gemini can preserve the schema but not its name, description, or strict flag")
				}
				diagnostics = appendDiagnostic(diagnostics, "warning", "json_schema_metadata_not_representable", "$.response_format.json_schema", "JSON schema name, description, and strict flag were omitted")
			}
			config.ResponseMIMEType = "application/json"
			schema, err := normalizeGeminiJSONSchema(ProtocolChat, "$.response_format.json_schema.schema", format.Schema)
			if err != nil {
				return conversionResult{}, err
			}
			config.ResponseJSONSchema = schema
		}
	}
	if !emptyGeminiGenerationConfig(config) {
		target.GenerationConfig = config
	}

	declaredTools := make(map[string]struct{}, len(source.Tools))
	if len(source.Tools) > 0 {
		tool := geminiTool{}
		for index, item := range source.Tools {
			path := fmt.Sprintf("$.tools[%d]", index)
			if item.Type != "function" {
				return conversionResult{}, unsupported(ProtocolChat, path+".type", "tool type %q requires a native Chat provider", item.Type)
			}
			if strings.TrimSpace(item.Function.Name) == "" {
				return conversionResult{}, invalid(ProtocolChat, path+".function.name", "name is required")
			}
			if _, duplicate := declaredTools[item.Function.Name]; duplicate {
				return conversionResult{}, invalid(ProtocolChat, path+".function.name", "duplicate function name %q", item.Function.Name)
			}
			declaredTools[item.Function.Name] = struct{}{}
			if item.Function.Strict != nil && *item.Function.Strict {
				return conversionResult{}, unsupported(ProtocolChat, path+".function.strict", "Gemini function declarations have no strict-mode equivalent")
			}
			parameters, err := normalizeFunctionParameters(ProtocolChat, path+".function.parameters", item.Function.Parameters)
			if err != nil {
				return conversionResult{}, err
			}
			parameters, err = normalizeGeminiSchema(parameters)
			if err != nil {
				return conversionResult{}, err
			}
			tool.FunctionDeclarations = append(tool.FunctionDeclarations, geminiFunctionDeclaration{
				Name: item.Function.Name, Description: item.Function.Description, Parameters: parameters,
			})
		}
		target.Tools = []geminiTool{tool}
	}
	choice, err := decodeChatToolChoice(source.ToolChoice)
	if err != nil {
		return conversionResult{}, err
	}
	if choice.Mode != "" {
		if len(declaredTools) == 0 && choice.Mode != toolChoiceNone {
			return conversionResult{}, invalid(ProtocolChat, "$.tool_choice", "tool choice requires at least one declared function")
		}
		if choice.Mode == toolChoiceNamed {
			if _, ok := declaredTools[choice.Name]; !ok {
				return conversionResult{}, invalid(ProtocolChat, "$.tool_choice.function.name", "function %q is not declared", choice.Name)
			}
		}
		target.ToolConfig = geminiToolConfigForChoice(choice)
	}

	callNames := make(map[string]string)
	sawOrdinaryContent := false
	for index, message := range source.Messages {
		path := fmt.Sprintf("$.messages[%d]", index)
		switch message.Role {
		case "system", "developer":
			if sawOrdinaryContent {
				return conversionResult{}, unsupported(ProtocolChat, path+".role", "interleaved system/developer messages cannot be moved to Gemini systemInstruction")
			}
			if message.Name != "" || message.ToolCallID != "" || len(message.ToolCalls) > 0 || message.Refusal != "" || message.ReasoningContent != "" {
				return conversionResult{}, unsupported(ProtocolChat, path, "system/developer message metadata has no Gemini equivalent")
			}
			parts, err := chatContentToGeminiParts(message.Content, path+".content")
			if err != nil {
				return conversionResult{}, err
			}
			if len(parts) == 0 {
				return conversionResult{}, invalid(ProtocolChat, path+".content", "system/developer content cannot be empty")
			}
			if target.SystemInstruction == nil {
				target.SystemInstruction = &geminiContent{}
			}
			target.SystemInstruction.Parts = append(target.SystemInstruction.Parts, parts...)
		case "tool":
			sawOrdinaryContent = true
			if message.ToolCallID == "" {
				return conversionResult{}, invalid(ProtocolChat, path+".tool_call_id", "tool_call_id is required")
			}
			name := callNames[message.ToolCallID]
			if message.Name != "" {
				if name != "" && name != message.Name {
					return conversionResult{}, invalid(ProtocolChat, path+".name", "tool result name %q does not match call %q", message.Name, name)
				}
				name = message.Name
			}
			if name == "" {
				return conversionResult{}, unsupported(ProtocolChat, path+".name", "Gemini functionResponse requires the corresponding function name")
			}
			response, wrapped, err := chatToolResultToGemini(message.Content, path+".content")
			if err != nil {
				return conversionResult{}, err
			}
			if wrapped {
				diagnostics = appendDiagnostic(diagnostics, "warning", "tool_result_wrapped", path+".content", "non-object Chat tool content was wrapped in a Gemini response object")
			}
			appendGeminiContent(&target, "user", geminiPart{FunctionResponse: &geminiFunctionResponse{ID: message.ToolCallID, Name: name, Response: response}})
		case "user", "assistant":
			sawOrdinaryContent = true
			if message.Name != "" {
				return conversionResult{}, unsupported(ProtocolChat, path+".name", "Gemini cannot preserve Chat message names")
			}
			if message.ToolCallID != "" {
				return conversionResult{}, invalid(ProtocolChat, path+".tool_call_id", "tool_call_id is only valid on tool messages")
			}
			if message.Refusal != "" {
				return conversionResult{}, unsupported(ProtocolChat, path+".refusal", "Gemini cannot preserve a structured refusal in request history")
			}
			if message.Role == "user" && (message.ReasoningContent != "" || len(message.ToolCalls) > 0) {
				return conversionResult{}, invalid(ProtocolChat, path, "reasoning_content and tool_calls are only valid on assistant messages")
			}
			var parts []geminiPart
			if message.ReasoningContent != "" {
				parts = append(parts, geminiPart{Text: message.ReasoningContent, Thought: true, ThoughtSignature: geminiThoughtSignatureBypass})
				diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_thought_signature_bypass_added", path+".reasoning_content", "a Gemini-compatible thought signature bypass was attached to converted reasoning")
			}
			content, err := chatContentToGeminiParts(message.Content, path+".content")
			if err != nil {
				return conversionResult{}, err
			}
			parts = append(parts, content...)
			signatureAttached := message.ReasoningContent != ""
			for callIndex, call := range message.ToolCalls {
				callPath := fmt.Sprintf("%s.tool_calls[%d]", path, callIndex)
				if call.Type != "" && call.Type != "function" {
					return conversionResult{}, unsupported(ProtocolChat, callPath+".type", "tool call type %q is unsupported", call.Type)
				}
				if call.ID == "" || call.Function.Name == "" {
					return conversionResult{}, invalid(ProtocolChat, callPath, "tool call id and function name are required")
				}
				if _, duplicate := callNames[call.ID]; duplicate {
					return conversionResult{}, invalid(ProtocolChat, callPath+".id", "duplicate tool call id %q", call.ID)
				}
				arguments, err := normalizeOpenAIToolArguments(ProtocolChat, callPath+".function.arguments", call.Function.Arguments)
				if err != nil {
					return conversionResult{}, err
				}
				part := geminiPart{FunctionCall: &geminiFunctionCall{ID: call.ID, Name: call.Function.Name, Args: arguments}}
				if !signatureAttached {
					part.ThoughtSignature = geminiThoughtSignatureBypass
					signatureAttached = true
					diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_thought_signature_bypass_added", callPath, "a Gemini-compatible thought signature bypass was attached to the converted function call")
				}
				parts = append(parts, part)
				callNames[call.ID] = call.Function.Name
			}
			if len(parts) == 0 {
				return conversionResult{}, invalid(ProtocolChat, path, "message must contain content, reasoning, or tool calls")
			}
			role := "user"
			if message.Role == "assistant" {
				role = "model"
			}
			appendGeminiContent(&target, role, parts...)
		default:
			return conversionResult{}, invalid(ProtocolChat, path+".role", "unsupported role %q", message.Role)
		}
	}
	if len(target.Contents) == 0 {
		return conversionResult{}, invalid(ProtocolChat, "$.messages", "Gemini requires at least one non-system content message")
	}
	body, err := marshal(ProtocolGenerateContent, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *geminiToChatConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolGenerateContent, input, "contents", "systemInstruction", "tools", "toolConfig", "generationConfig", "safetySettings", "cachedContent"); err != nil {
		return conversionResult{}, err
	}
	if err := validateGeminiNestedFields(input); err != nil {
		return conversionResult{}, err
	}
	var source geminiRequest
	if err := decodeJSON(ProtocolGenerateContent, input, &source); err != nil {
		return conversionResult{}, err
	}
	if len(source.Contents) == 0 {
		return conversionResult{}, invalid(ProtocolGenerateContent, "$.contents", "at least one content is required")
	}
	if source.CachedContent != "" {
		return conversionResult{}, unsupported(ProtocolGenerateContent, "$.cachedContent", "cached content state requires a native Gemini provider")
	}
	if jsonValuePresent(source.SafetySettings) && !bytes.Equal(bytes.TrimSpace(source.SafetySettings), []byte("[]")) {
		return conversionResult{}, unsupported(ProtocolGenerateContent, "$.safetySettings", "provider safety policy has no Chat equivalent")
	}
	target := chatRequest{Model: options.Exchange.UpstreamModel, Stream: resolveExchangeStream(false, options.Exchange)}
	if target.Model == "" {
		return conversionResult{}, invalid(ProtocolGenerateContent, "$.model", "upstream Chat model is required in exchange metadata")
	}
	var diagnostics []Diagnostic
	if err := geminiConfigToChat(source.GenerationConfig, &target, options.LossPolicy, &diagnostics); err != nil {
		return conversionResult{}, err
	}

	declaredTools := make(map[string]struct{})
	for toolIndex, item := range source.Tools {
		path := fmt.Sprintf("$.tools[%d]", toolIndex)
		if jsonValuePresent(item.CodeExecution) || jsonValuePresent(item.GoogleSearch) || jsonValuePresent(item.GoogleSearchRetrieval) || jsonValuePresent(item.URLContext) {
			return conversionResult{}, unsupported(ProtocolGenerateContent, path, "built-in Gemini tools require a native Gemini provider")
		}
		for functionIndex, function := range item.FunctionDeclarations {
			functionPath := fmt.Sprintf("%s.functionDeclarations[%d]", path, functionIndex)
			if strings.TrimSpace(function.Name) == "" {
				return conversionResult{}, invalid(ProtocolGenerateContent, functionPath+".name", "name is required")
			}
			if _, duplicate := declaredTools[function.Name]; duplicate {
				return conversionResult{}, invalid(ProtocolGenerateContent, functionPath+".name", "duplicate function name %q", function.Name)
			}
			declaredTools[function.Name] = struct{}{}
			parameters, err := chatGeminiSchemaToJSONSchema(function.Parameters, functionPath+".parameters")
			if err != nil {
				return conversionResult{}, err
			}
			parameters, err = normalizeFunctionParameters(ProtocolGenerateContent, functionPath+".parameters", parameters)
			if err != nil {
				return conversionResult{}, err
			}
			var converted chatTool
			converted.Type = "function"
			converted.Function.Name, converted.Function.Description, converted.Function.Parameters = function.Name, function.Description, parameters
			target.Tools = append(target.Tools, converted)
		}
	}
	choice, err := geminiToolChoiceToChat(source.ToolConfig, declaredTools)
	if err != nil {
		return conversionResult{}, err
	}
	if choice.Mode != "" {
		target.ToolChoice = encodeChatToolChoice(choice)
	}
	if source.SystemInstruction != nil {
		if source.SystemInstruction.Role != "" && source.SystemInstruction.Role != "user" && source.SystemInstruction.Role != "system" {
			return conversionResult{}, unsupported(ProtocolGenerateContent, "$.systemInstruction.role", "role %q cannot be represented by Chat", source.SystemInstruction.Role)
		}
		parts, _, err := geminiPartsToChat(source.SystemInstruction.Parts, "$.systemInstruction.parts", "system", options.LossPolicy, nil, &diagnostics)
		if err != nil {
			return conversionResult{}, err
		}
		content, err := encodeChatContent(parts)
		if err != nil {
			return conversionResult{}, err
		}
		if len(parts) == 0 {
			return conversionResult{}, invalid(ProtocolGenerateContent, "$.systemInstruction.parts", "at least one part is required")
		}
		target.Messages = append(target.Messages, chatMessage{Role: "system", Content: content})
	}

	callIDsByName := make(map[string][]string)
	callNamesByID := make(map[string]string)
	generatedCallID := 0
	for contentIndex, content := range source.Contents {
		path := fmt.Sprintf("$.contents[%d]", contentIndex)
		role := content.Role
		if role == "" {
			role = "user"
		}
		if role != "user" && role != "model" {
			return conversionResult{}, unsupported(ProtocolGenerateContent, path+".role", "role %q cannot be represented by Chat", content.Role)
		}
		if len(content.Parts) == 0 {
			return conversionResult{}, invalid(ProtocolGenerateContent, path+".parts", "at least one part is required")
		}
		var ordinary []geminiPart
		flushOrdinary := func() error {
			if len(ordinary) == 0 {
				return nil
			}
			parts, calls, err := geminiPartsToChat(ordinary, path+".parts", role, options.LossPolicy, &generatedCallID, &diagnostics)
			if err != nil {
				return err
			}
			messageRole := "user"
			if role == "model" {
				messageRole = "assistant"
			}
			message := chatMessage{Role: messageRole}
			var contentParts []portablePart
			for _, part := range parts {
				if part.Kind == partReasoning {
					message.ReasoningContent += part.Text
				} else {
					contentParts = append(contentParts, part)
				}
			}
			message.Content, err = encodeChatContent(contentParts)
			if err != nil {
				return err
			}
			for _, call := range calls {
				var converted chatToolCall
				converted.ID, converted.Type, converted.Function.Name = call.ID, "function", call.Name
				converted.Function.Arguments = json.RawMessage(mustJSONString(string(call.Arguments)))
				message.ToolCalls = append(message.ToolCalls, converted)
				if _, duplicate := callNamesByID[call.ID]; duplicate {
					return invalid(ProtocolGenerateContent, path+".parts.functionCall.id", "duplicate function call id %q", call.ID)
				}
				callNamesByID[call.ID] = call.Name
				callIDsByName[call.Name] = append(callIDsByName[call.Name], call.ID)
			}
			target.Messages = append(target.Messages, message)
			ordinary = nil
			return nil
		}
		for partIndex, part := range content.Parts {
			partPath := fmt.Sprintf("%s.parts[%d]", path, partIndex)
			if err := validateGeminiPart(part, partPath); err != nil {
				return conversionResult{}, err
			}
			if part.FunctionResponse == nil {
				ordinary = append(ordinary, part)
				continue
			}
			if role != "user" {
				return conversionResult{}, invalid(ProtocolGenerateContent, partPath+".functionResponse", "functionResponse is only valid in user content")
			}
			if err := flushOrdinary(); err != nil {
				return conversionResult{}, err
			}
			response := part.FunctionResponse
			if response.Name == "" {
				return conversionResult{}, invalid(ProtocolGenerateContent, partPath+".functionResponse.name", "name is required")
			}
			callID := response.ID
			if callID != "" {
				name := callNamesByID[callID]
				if name == "" {
					return conversionResult{}, unsupported(ProtocolGenerateContent, partPath+".functionResponse.id", "function response cannot be correlated with an earlier function call")
				}
				if name != response.Name {
					return conversionResult{}, invalid(ProtocolGenerateContent, partPath+".functionResponse.name", "function response name %q does not match call %q", response.Name, name)
				}
				callIDsByName[response.Name] = removeFirstString(callIDsByName[response.Name], callID)
			} else {
				ids := callIDsByName[response.Name]
				if len(ids) == 0 {
					return conversionResult{}, unsupported(ProtocolGenerateContent, partPath+".functionResponse.id", "function response cannot be correlated with an earlier function call")
				}
				callID = ids[0]
				callIDsByName[response.Name] = ids[1:]
				diagnostics = appendDiagnostic(diagnostics, "warning", "function_response_id_inferred", partPath+".functionResponse.id", "missing Gemini function response id was correlated by function name and order")
			}
			target.Messages = append(target.Messages, chatMessage{
				Role: "tool", Name: response.Name, ToolCallID: callID,
				Content: json.RawMessage(mustJSONString(string(response.Response))),
			})
		}
		if err := flushOrdinary(); err != nil {
			return conversionResult{}, err
		}
	}
	body, err := marshal(ProtocolChat, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}
