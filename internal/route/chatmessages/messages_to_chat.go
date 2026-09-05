package chatmessages

import (
	"context"
	"encoding/json"
	"fmt"
)

func (c *messagesToChatConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolMessages, input, "model", "max_tokens", "messages", "system", "tools", "tool_choice", "temperature", "top_k", "top_p", "stop_sequences", "stream", "thinking", "output_config", "metadata", "container", "cache_control", "inference_geo", "service_tier"); err != nil {
		return conversionResult{}, err
	}
	body, diagnostics, err := mapMessagesRequestToChat(input, options)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func mapChatRequestToMessages(input []byte, options conversionOptions) ([]byte, []Diagnostic, error) {
	var source chatRequest
	if err := decodeJSON(ProtocolChat, input, &source); err != nil {
		return nil, nil, err
	}
	if source.Model == "" || len(source.Messages) == 0 {
		return nil, nil, invalid(ProtocolChat, "$", "model and at least one message are required")
	}
	if source.N != nil && *source.N != 1 {
		return nil, nil, unsupported(ProtocolChat, "$.n", "cross-protocol conversion supports exactly one choice")
	}
	if err := validateOpenAIReasoningEffortForMessages(ProtocolChat, "$.reasoning_effort", source.ReasoningEffort); err != nil {
		return nil, nil, err
	}
	for path, present := range map[string]bool{
		"$.frequency_penalty": source.FrequencyPenalty != nil, "$.presence_penalty": source.PresencePenalty != nil,
		"$.logprobs": source.Logprobs != nil, "$.top_logprobs": source.TopLogprobs != nil,
		"$.verbosity": rawJSONValuePresent(source.Verbosity), "$.user": rawJSONValuePresent(source.User),
		"$.service_tier":     rawJSONValuePresent(source.ServiceTier),
		"$.store":            rawJSONValuePresent(source.Store),
		"$.prompt_cache_key": rawJSONValuePresent(source.PromptCacheKey), "$.prompt_cache_retention": rawJSONValuePresent(source.PromptCacheRetention),
	} {
		if present {
			return nil, nil, unsupported(ProtocolChat, path, "field is not representable on this route")
		}
	}
	maxTokens := 4096
	var diagnostics []Diagnostic
	if source.MaxCompletion != nil {
		maxTokens = *source.MaxCompletion
	} else if source.MaxTokens != nil {
		maxTokens = *source.MaxTokens
	} else {
		diagnostics = appendDiagnostic(diagnostics, "warning", "default_max_tokens", "$.max_tokens", "Messages requires max_tokens; RouteMorph used 4096")
	}
	model := source.Model
	if options.Exchange.UpstreamModel != "" {
		model = options.Exchange.UpstreamModel
	}
	target := messagesRequest{Model: model, MaxTokens: maxTokens, Temperature: source.Temperature, TopP: source.TopP, Stream: resolveExchangeStream(source.Stream, options.Exchange)}
	if len(source.Metadata) > 0 {
		return nil, diagnostics, unsupported(ProtocolChat, "$.metadata", "OpenAI metadata is storage/query metadata, not Anthropic user attribution")
	}
	safetyID, err := decodeOpenAISafetyIdentifier(source.SafetyIdentifier, "$.safety_identifier")
	if err != nil {
		return nil, diagnostics, err
	}
	if safetyID != "" {
		target.Metadata = map[string]string{"user_id": safetyID}
	}
	stop, err := decodeStop(ProtocolChat, source.Stop)
	if err != nil {
		return nil, diagnostics, err
	}
	target.StopSequences = stop
	choice, err := decodeChatToolChoice(source.ToolChoice)
	if err != nil {
		return nil, diagnostics, err
	}
	if choice.Mode != "" || source.ParallelToolCalls != nil {
		target.ToolChoice = encodeMessagesToolChoice(choice, source.ParallelToolCalls)
	}
	format, err := decodeChatResponseFormat(source.ResponseFormat)
	if err != nil {
		return nil, diagnostics, err
	}
	if source.ReasoningEffort != "" || format != nil {
		target.OutputConfig = &messagesOutputConfig{}
		if source.ReasoningEffort != "" {
			target.Thinking = &messagesThinking{Type: "adaptive"}
			target.OutputConfig.Effort = source.ReasoningEffort
		}
		if format != nil {
			target.OutputConfig.Format = &struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			}{Type: "json_schema", Schema: format.Schema}
		}
	}
	for index, tool := range source.Tools {
		if tool.Type != "function" || tool.Function.Name == "" {
			return nil, diagnostics, invalid(ProtocolChat, fmt.Sprintf("$.tools[%d]", index), "function tool with a name is required")
		}
		schema, err := normalizeMessagesInputSchema(tool.Function.Parameters, fmt.Sprintf("$.tools[%d].function.parameters", index))
		if err != nil {
			return nil, diagnostics, err
		}
		target.Tools = append(target.Tools, messagesTool{Name: tool.Function.Name, Description: tool.Function.Description, InputSchema: schema, Strict: tool.Function.Strict})
	}
	var system []messagesBlock
	callNames := make(map[string]string)
	consumedCallIDs := make(map[string]bool)
	seenConversation := false
	for index, sourceMessage := range source.Messages {
		path := fmt.Sprintf("$.messages[%d]", index)
		message, err := decodeChatMessage(sourceMessage, index)
		if err != nil {
			return nil, diagnostics, err
		}
		if message.Role == roleSystem || message.Role == roleDeveloper {
			if seenConversation {
				return nil, diagnostics, unsupported(ProtocolChat, path+".role", "interleaved system/developer messages cannot be moved to Messages system")
			}
			for partIndex, part := range message.Parts {
				if part.Kind != partText {
					return nil, diagnostics, unsupported(ProtocolChat, fmt.Sprintf("%s.content[%d]", path, partIndex), "Messages system instructions support text only")
				}
			}
		} else {
			seenConversation = true
		}
		for partIndex, part := range message.Parts {
			switch part.Kind {
			case partToolCall:
				if message.Role != roleAssistant || part.ToolCall == nil {
					return nil, diagnostics, invalid(ProtocolChat, fmt.Sprintf("%s.tool_calls[%d]", path, partIndex), "tool calls require the assistant role")
				}
				if _, duplicate := callNames[part.ToolCall.ID]; duplicate {
					return nil, diagnostics, invalid(ProtocolChat, path+".tool_calls", "duplicate tool call id %q", part.ToolCall.ID)
				}
				callNames[part.ToolCall.ID] = part.ToolCall.Name
			case partToolResult:
				if message.Role != roleTool || part.ToolResult == nil {
					return nil, diagnostics, invalid(ProtocolChat, path, "tool results require the tool role")
				}
				name, found := callNames[part.ToolResult.CallID]
				if !found {
					return nil, diagnostics, invalid(ProtocolChat, path+".tool_call_id", "tool result references unknown call %q", part.ToolResult.CallID)
				}
				if part.ToolResult.Name != "" && part.ToolResult.Name != name {
					return nil, diagnostics, invalid(ProtocolChat, path+".name", "tool result name %q does not match call name %q", part.ToolResult.Name, name)
				}
				if consumedCallIDs[part.ToolResult.CallID] {
					return nil, diagnostics, invalid(ProtocolChat, path+".tool_call_id", "duplicate tool result for call %q", part.ToolResult.CallID)
				}
				consumedCallIDs[part.ToolResult.CallID] = true
			}
		}
		blocks, err := encodeMessagesContent(message.Parts)
		if err != nil {
			return nil, diagnostics, err
		}
		if message.Role == roleSystem || message.Role == roleDeveloper {
			system = append(system, blocks...)
			continue
		}
		role := string(message.Role)
		if message.Role == roleTool {
			role = "user"
		}
		appendMessagesTurn(&target.Messages, messagesMessage{Role: role, Content: mustJSON(blocks)})
	}
	if len(system) > 0 {
		target.System = mustJSON(system)
	}
	body, err := marshal(ProtocolMessages, target)
	return body, diagnostics, err
}

func mapMessagesRequestToChat(input []byte, options conversionOptions) ([]byte, []Diagnostic, error) {
	var source messagesRequest
	if err := decodeJSON(ProtocolMessages, input, &source); err != nil {
		return nil, nil, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(input, &envelope); err != nil || !jsonValuePresent(envelope["max_tokens"]) {
		return nil, nil, invalid(ProtocolMessages, "$.max_tokens", "max_tokens is required")
	}
	if source.Model == "" || len(source.Messages) == 0 {
		return nil, nil, invalid(ProtocolMessages, "$", "model and at least one message are required")
	}
	if source.MaxTokens == 0 {
		return nil, nil, unsupported(ProtocolMessages, "$.max_tokens", "prompt-cache pre-warming has no portable cross-protocol equivalent")
	}
	if source.MaxTokens < 0 {
		return nil, nil, invalid(ProtocolMessages, "$.max_tokens", "must not be negative")
	}
	if messagesRawNonNull(source.Container) {
		return nil, nil, unsupported(ProtocolMessages, "$.container", "container state requires a native Messages provider")
	}
	for path, present := range map[string]bool{
		"$.top_k":         source.TopK != nil,
		"$.cache_control": jsonValuePresent(source.CacheControl),
		"$.inference_geo": source.InferenceGeo != "",
		"$.service_tier":  source.ServiceTier != "",
	} {
		if present {
			return nil, nil, unsupported(ProtocolMessages, path, "field has no semantically equivalent Chat control")
		}
	}
	if err := validateMessagesThinkingBudget(source.Thinking, source.MaxTokens, "$.thinking"); err != nil {
		return nil, nil, err
	}
	if err := validateMessagesOutputConfig(source.OutputConfig, "$.output_config"); err != nil {
		return nil, nil, err
	}
	model := source.Model
	if options.Exchange.UpstreamModel != "" {
		model = options.Exchange.UpstreamModel
	}
	target := chatRequest{Model: model, MaxCompletion: &source.MaxTokens, Temperature: source.Temperature, TopP: source.TopP, Stream: resolveExchangeStream(source.Stream, options.Exchange)}
	safetyID, err := messagesSafetyIdentifier(source.Metadata, "$.metadata")
	if err != nil {
		return nil, nil, err
	}
	if safetyID != "" {
		target.SafetyIdentifier = mustJSON(safetyID)
	}
	if len(source.StopSequences) == 1 {
		target.Stop = mustJSON(source.StopSequences[0])
	} else if len(source.StopSequences) > 1 {
		target.Stop = mustJSON(source.StopSequences)
	}
	choice, parallel, err := decodeMessagesToolChoice(source.ToolChoice)
	if err != nil {
		return nil, nil, err
	}
	target.ParallelToolCalls = parallel
	if choice.Mode != "" {
		target.ToolChoice = encodeChatToolChoice(choice)
	}
	var diagnostics []Diagnostic
	if source.Thinking != nil && source.Thinking.Type != "disabled" {
		if options.LossPolicy == rejectSemanticLoss {
			return nil, diagnostics, unsupported(ProtocolMessages, "$.thinking", "Messages thinking configuration is not semantically equivalent to Chat reasoning_effort")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "thinking_policy_approximated", "$.thinking", "Messages thinking configuration was approximated as Chat reasoning_effort")
		target.ReasoningEffort = "medium"
	}
	if source.OutputConfig != nil {
		if source.OutputConfig.Effort != "" {
			target.ReasoningEffort = source.OutputConfig.Effort
		}
		if source.OutputConfig.Format != nil {
			target.ResponseFormat = mustJSON(map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "response", "schema": source.OutputConfig.Format.Schema}})
		}
	}
	for index, tool := range source.Tools {
		path := fmt.Sprintf("$.tools[%d]", index)
		if jsonValuePresent(tool.CacheControl) {
			return nil, diagnostics, unsupported(ProtocolMessages, path+".cache_control", "tool cache control has no Chat equivalent")
		}
		if tool.EagerInputStreaming != nil {
			return nil, diagnostics, unsupported(ProtocolMessages, path+".eager_input_streaming", "Chat has no eager tool-input streaming control")
		}
		if tool.DeferLoading != nil {
			return nil, diagnostics, unsupported(ProtocolMessages, path+".defer_loading", "Chat has no deferred tool-loading control")
		}
		if len(tool.AllowedCallers) > 0 {
			return nil, diagnostics, unsupported(ProtocolMessages, path+".allowed_callers", "Chat cannot preserve tool caller constraints")
		}
		if jsonValuePresent(tool.InputExamples) {
			return nil, diagnostics, unsupported(ProtocolMessages, path+".input_examples", "Chat function tools have no input examples field")
		}
		if tool.Type != "" && tool.Type != "custom" {
			return nil, diagnostics, unsupported(ProtocolMessages, path+".type", "server tool %q requires a native Messages provider", tool.Type)
		}
		if tool.Name == "" {
			return nil, diagnostics, invalid(ProtocolMessages, fmt.Sprintf("$.tools[%d].name", index), "name is required")
		}
		parameters, err := normalizeFunctionParameters(ProtocolChat, fmt.Sprintf("$.tools[%d].input_schema", index), tool.InputSchema)
		if err != nil {
			return nil, diagnostics, err
		}
		var converted chatTool
		converted.Type, converted.Function.Name, converted.Function.Description = "function", tool.Name, tool.Description
		converted.Function.Parameters, converted.Function.Strict = parameters, tool.Strict
		target.Tools = append(target.Tools, converted)
	}
	if jsonValuePresent(source.System) {
		parts, err := decodeMessagesContent(source.System, "$.system")
		if err != nil {
			return nil, diagnostics, err
		}
		for index, part := range parts {
			if part.Kind != partText {
				return nil, diagnostics, unsupported(ProtocolMessages, fmt.Sprintf("$.system[%d]", index), "Chat developer instructions support text only")
			}
		}
		content, err := encodeChatContent(parts)
		if err != nil {
			return nil, diagnostics, err
		}
		target.Messages = append(target.Messages, chatMessage{Role: "developer", Content: content})
	}
	portable := make([]portableMessage, 0, len(source.Messages))
	callNames := make(map[string]string)
	consumedCallIDs := make(map[string]bool)
	seenConversation := false
	for index, sourceMessage := range source.Messages {
		path := fmt.Sprintf("$.messages[%d]", index)
		if sourceMessage.Role != "user" && sourceMessage.Role != "assistant" && sourceMessage.Role != "system" {
			return nil, diagnostics, invalid(ProtocolMessages, path+".role", "role must be user, assistant, or system")
		}
		parts, err := decodeMessagesContent(sourceMessage.Content, path+".content")
		if err != nil {
			return nil, diagnostics, err
		}
		if sourceMessage.Role == "system" {
			if seenConversation {
				return nil, diagnostics, unsupported(ProtocolMessages, path+".role", "interleaved system messages cannot be moved to Chat developer instructions")
			}
			for partIndex, part := range parts {
				if part.Kind != partText {
					return nil, diagnostics, unsupported(ProtocolMessages, fmt.Sprintf("%s.content[%d]", path, partIndex), "Chat developer instructions support text only")
				}
			}
		} else {
			seenConversation = true
		}
		for partIndex, part := range parts {
			blockPath := fmt.Sprintf("%s.content[%d]", path, partIndex)
			switch part.Kind {
			case partToolCall:
				if sourceMessage.Role != "assistant" || part.ToolCall == nil {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath, "tool_use blocks require the assistant role")
				}
				if _, duplicate := callNames[part.ToolCall.ID]; duplicate {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".id", "duplicate tool_use id %q", part.ToolCall.ID)
				}
				callNames[part.ToolCall.ID] = part.ToolCall.Name
			case partToolResult:
				if sourceMessage.Role != "user" || part.ToolResult == nil {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath, "tool_result blocks require the user role")
				}
				if _, found := callNames[part.ToolResult.CallID]; !found {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".tool_use_id", "tool_result references unknown call %q", part.ToolResult.CallID)
				}
				if consumedCallIDs[part.ToolResult.CallID] {
					return nil, diagnostics, invalid(ProtocolMessages, blockPath+".tool_use_id", "duplicate tool_result for call %q", part.ToolResult.CallID)
				}
				consumedCallIDs[part.ToolResult.CallID] = true
			}
		}
		role := semanticRole(sourceMessage.Role)
		if role == roleSystem {
			role = roleDeveloper
		}
		for _, part := range parts {
			if part.Kind == partToolResult {
				role = roleTool
			}
			if part.Kind == partReasoning && part.Opaque != "" {
				if options.LossPolicy == rejectSemanticLoss {
					return nil, diagnostics, unsupported(ProtocolMessages, fmt.Sprintf("$.messages[%d].content", index), "signed Messages thinking cannot be represented by Chat")
				}
				diagnostics = appendDiagnostic(diagnostics, "warning", "thinking_signature_not_representable", fmt.Sprintf("$.messages[%d].content", index), "Messages thinking signature was omitted")
			}
		}
		portable = append(portable, portableMessage{Role: role, Parts: parts})
	}
	portable, diagnostics, err = prepareMessagesForChat(portable, diagnostics, options.LossPolicy)
	if err != nil {
		return nil, diagnostics, err
	}
	for index, message := range portable {
		converted, err := encodeChatMessage(message, index)
		if err != nil {
			return nil, diagnostics, err
		}
		target.Messages = append(target.Messages, converted...)
	}
	body, err := marshal(ProtocolChat, target)
	return body, diagnostics, err
}

func prepareMessagesForChat(messages []portableMessage, diagnostics []Diagnostic, policy lossPolicy) ([]portableMessage, []Diagnostic, error) {
	converted := make([]portableMessage, 0, len(messages))
	for messageIndex, message := range messages {
		hasToolResult := false
		for _, part := range message.Parts {
			if part.Kind == partToolResult {
				hasToolResult = true
				break
			}
		}
		if !hasToolResult {
			converted = append(converted, message)
			continue
		}

		ordinary := make([]portablePart, 0, len(message.Parts))
		flushOrdinary := func() {
			if len(ordinary) == 0 {
				return
			}
			converted = append(converted, portableMessage{Role: roleUser, Parts: ordinary})
			ordinary = nil
		}
		for partIndex, part := range message.Parts {
			if part.Kind != partToolResult {
				ordinary = append(ordinary, part)
				continue
			}
			flushOrdinary()
			path := fmt.Sprintf("$.messages[%d].content[%d]", messageIndex, partIndex)
			if part.ToolResult == nil {
				return nil, diagnostics, invalid(ProtocolMessages, path, "nil tool_result")
			}
			if part.ToolResult.IsError {
				if policy == rejectSemanticLoss {
					return nil, diagnostics, unsupported(ProtocolMessages, path+".is_error", "Chat tool messages cannot preserve Anthropic tool-result error state")
				}
				diagnostics = appendDiagnostic(diagnostics, "warning", "tool_result_error_state_not_representable", path+".is_error", "the Anthropic tool-result error state was omitted")
				part.ToolResult.IsError = false
			}
			converted = append(converted, portableMessage{Role: roleTool, Parts: []portablePart{part}})
		}
		flushOrdinary()
	}
	return converted, diagnostics, nil
}

func (c *messagesToChatConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source chatResponse
	if err := decodeJSON(ProtocolChat, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Error != nil {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.error", "%s", source.Error.Message)
	}
	if len(source.Choices) != 1 {
		return conversionResult{}, unsupported(ProtocolChat, "$.choices", "cross-protocol conversion requires exactly one choice")
	}
	if jsonValuePresent(source.Choices[0].Logprobs) {
		return conversionResult{}, unsupported(ProtocolChat, "$.choices[0].logprobs", "Messages cannot represent token log probabilities")
	}
	diagnostics, err := chatResponseExtensionDiagnostics(source, options.LossPolicy)
	if err != nil {
		return conversionResult{}, err
	}
	message, err := decodeChatMessage(source.Choices[0].Message, 0)
	if err != nil {
		return conversionResult{}, err
	}
	for index, part := range message.Parts {
		switch part.Kind {
		case partReasoning:
			return conversionResult{}, unsupported(ProtocolChat, "$.choices[0].message.reasoning_content", "Chat reasoning cannot be converted into a valid signed Messages thinking block")
		case partRefusal:
			if options.LossPolicy == rejectSemanticLoss {
				return conversionResult{}, unsupported(ProtocolChat, "$.choices[0].message.refusal", "Chat refusal has no distinct Messages response block")
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "refusal_content_approximated", fmt.Sprintf("$.parts[%d]", index), "Chat refusal was emitted as Messages text")
		}
	}
	blocks, err := encodeMessagesContent(message.Parts)
	if err != nil {
		return conversionResult{}, err
	}
	finish, err := parseChatFinish(source.Choices[0].FinishReason)
	if err != nil {
		return conversionResult{}, err
	}
	model := source.Model
	if options.Exchange.ClientModel != "" {
		model = options.Exchange.ClientModel
	}
	target := messagesResponse{ID: source.ID, Type: "message", Role: "assistant", Model: model, Content: mustJSON(blocks), StopReason: messagesStop(finish)}
	target.Usage.InputTokens = source.Usage.PromptTokens - source.Usage.PromptDetails.CachedTokens
	if target.Usage.InputTokens < 0 {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.usage.prompt_tokens_details.cached_tokens", "cached tokens exceed prompt tokens")
	}
	target.Usage.CacheReadInputTokens = source.Usage.PromptDetails.CachedTokens
	target.Usage.OutputTokens = source.Usage.CompletionTokens
	if source.Usage.CompletionDetails.ReasoningTokens < 0 || source.Usage.CompletionDetails.ReasoningTokens > source.Usage.CompletionTokens {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.usage.completion_tokens_details.reasoning_tokens", "must be between zero and completion_tokens")
	}
	if source.Usage.CompletionDetails.ReasoningTokens > 0 {
		target.Usage.OutputTokensDetails = &messagesOutputTokensDetails{ThinkingTokens: source.Usage.CompletionDetails.ReasoningTokens}
	}
	body, err := marshal(ProtocolMessages, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *messagesToChatConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return c.buffered(c.spec, options, c.ToClientResponse), nil
}

func validateChatToMessagesRequest(input []byte, policy lossPolicy) ([]Diagnostic, error) {
	var source chatRequest
	if err := decodeJSON(ProtocolChat, input, &source); err != nil {
		return nil, err
	}
	diagnostics, err := validateChatStreamOptionsForMessages(input, source.Stream, policy)
	if err != nil {
		return nil, err
	}
	sawConversation := false
	for messageIndex, message := range source.Messages {
		path := fmt.Sprintf("$.messages[%d]", messageIndex)
		if rawJSONValuePresent(message.Audio) {
			return nil, unsupported(ProtocolChat, path+".audio", "Chat assistant audio cannot be represented by Messages input")
		}
		if chatAnnotationsPresent(message.Annotations) {
			return nil, unsupported(ProtocolChat, path+".annotations", "Chat URL citations cannot be represented as Messages input")
		}
		if message.Role == "system" || message.Role == "developer" {
			if sawConversation {
				return nil, unsupported(ProtocolChat, path+".role", "interleaved system/developer messages cannot be moved to Messages top-level system")
			}
			continue
		}
		sawConversation = true
		if message.ReasoningContent != "" {
			return nil, unsupported(ProtocolChat, path+".reasoning_content", "Chat reasoning cannot be converted into a valid signed Messages thinking block")
		}
		if message.Refusal != "" {
			if policy == rejectSemanticLoss {
				return nil, unsupported(ProtocolChat, path+".refusal", "Chat refusal has no distinct Messages request block")
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "refusal_content_approximated", path+".refusal", "Chat refusal was emitted as Messages text")
		}
	}
	if jsonValuePresent(source.ResponseFormat) {
		var format struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Strict      *bool           `json:"strict"`
				Schema      json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		if err := json.Unmarshal(source.ResponseFormat, &format); err != nil {
			return nil, invalid(ProtocolChat, "$.response_format", "invalid response format")
		}
		if format.Type == "json_object" {
			return nil, unsupported(ProtocolChat, "$.response_format.type", "Messages has no schema-free JSON object response mode")
		}
		if format.Type == "json_schema" && (format.JSONSchema.Name != "" || format.JSONSchema.Description != "" || format.JSONSchema.Strict != nil) {
			if policy == rejectSemanticLoss {
				return nil, unsupported(ProtocolChat, "$.response_format.json_schema", "Messages can preserve the schema but not its name, description, or strict flag")
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "json_schema_metadata_not_representable", "$.response_format.json_schema", "JSON schema name, description, and strict flag were omitted")
		}
	}
	return diagnostics, nil
}
