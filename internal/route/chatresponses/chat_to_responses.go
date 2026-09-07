package chatresponses

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/2218342221/RouteMorphSDK/internal/openaicompat"
)

func (c *chatToResponsesConverter) Specification() routeSpec { return c.spec }

func (c *chatToResponsesConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolChat, input, "model", "messages", "tools", "tool_choice", "max_tokens", "max_completion_tokens", "temperature", "top_p", "stop", "stream", "parallel_tool_calls", "response_format", "reasoning_effort", "metadata", "n", "logprobs", "top_logprobs", "verbosity", "user", "service_tier", "store", "prompt_cache_key", "prompt_cache_options", "prompt_cache_retention", "safety_identifier", "moderation", "stream_options", "web_search_options"); err != nil {
		return conversionResult{}, err
	}
	if err := validateChatMessageContentFields(ProtocolChat, input); err != nil {
		return conversionResult{}, err
	}
	var source chatRequest
	if err := decodeJSON(ProtocolChat, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Model == "" || len(source.Messages) == 0 {
		return conversionResult{}, invalid(ProtocolChat, "$.messages", "model and at least one message are required")
	}
	if err := validateChatToolDeclarationNames(source.Tools); err != nil {
		return conversionResult{}, err
	}
	if err := validateOpenAIReasoningEffort(ProtocolChat, "$.reasoning_effort", source.ReasoningEffort); err != nil {
		return conversionResult{}, err
	}
	if len(source.Stop) > 0 && string(source.Stop) != "null" {
		return conversionResult{}, unsupported(ProtocolChat, "$.stop", "Responses has no equivalent stop parameter")
	}
	if source.N != nil && *source.N != 1 {
		return conversionResult{}, unsupported(ProtocolChat, "$.n", "Responses supports exactly one generated response")
	}
	if source.TopLogprobs != nil && (source.Logprobs == nil || !*source.Logprobs) {
		return conversionResult{}, invalid(ProtocolChat, "$.top_logprobs", "top_logprobs requires logprobs=true")
	}
	if err := validatePromptCacheOptions(ProtocolChat, source.PromptCacheOptions); err != nil {
		return conversionResult{}, err
	}
	if err := validateModeration(ProtocolChat, source.Moderation); err != nil {
		return conversionResult{}, err
	}
	if err := openaicompat.ValidateChatServiceTier(source.ServiceTier, "$.service_tier", false); err != nil {
		return conversionResult{}, err
	}
	_, _, includeObfuscation, err := inspectChatStreamOptions(input)
	if err != nil {
		return conversionResult{}, err
	}
	target := responsesRequest{
		Model:                source.Model,
		MaxOutputTokens:      source.MaxCompletion,
		Temperature:          source.Temperature,
		TopP:                 source.TopP,
		Stream:               source.Stream,
		ParallelToolCalls:    source.ParallelToolCalls,
		Metadata:             source.Metadata,
		TopLogprobs:          source.TopLogprobs,
		User:                 source.User,
		ServiceTier:          source.ServiceTier,
		Store:                source.Store,
		PromptCacheKey:       source.PromptCacheKey,
		PromptCacheOptions:   source.PromptCacheOptions,
		PromptCacheRetention: source.PromptCacheRetention,
		SafetyIdentifier:     source.SafetyIdentifier,
		Moderation:           source.Moderation,
	}
	if includeObfuscation != nil {
		target.StreamOptions = mustJSON(map[string]any{"include_obfuscation": *includeObfuscation})
	}
	if source.Logprobs != nil && *source.Logprobs {
		target.Include = mustJSON([]string{"message.output_text.logprobs"})
	}
	if target.MaxOutputTokens == nil {
		target.MaxOutputTokens = source.MaxTokens
	}
	if options.Exchange.UpstreamModel != "" {
		target.Model = options.Exchange.UpstreamModel
	}
	target.Stream = resolveExchangeStream(source.Stream, options.Exchange)
	if !target.Stream && source.StreamOptions != nil {
		return conversionResult{}, invalid(ProtocolChat, "$.stream_options", "stream_options requires stream=true")
	}
	if source.ReasoningEffort != "" {
		target.Reasoning = &reasoningConfig{Effort: source.ReasoningEffort}
	}
	choice, err := decodeChatToolChoice(source.ToolChoice)
	if err != nil {
		return conversionResult{}, err
	}
	format, err := decodeChatResponseFormat(source.ResponseFormat)
	if err != nil {
		return conversionResult{}, err
	}
	if format != nil || rawJSONValuePresent(source.Verbosity) {
		text := map[string]any{}
		if format != nil {
			value := map[string]any{"type": format.Type}
			if format.Type == "json_schema" {
				value["name"], value["schema"] = format.Name, format.Schema
				if format.Description != "" {
					value["description"] = format.Description
				}
				if format.Strict != nil {
					value["strict"] = *format.Strict
				}
			}
			text["format"] = value
		}
		if rawJSONValuePresent(source.Verbosity) {
			text["verbosity"] = json.RawMessage(source.Verbosity)
		}
		target.Text = mustJSON(text)
	}
	seenToolNames := make(map[string]struct{}, len(source.Tools))
	for index, tool := range source.Tools {
		path := fmt.Sprintf("$.tools[%d]", index)
		if tool.Type == "" {
			return conversionResult{}, invalid(ProtocolChat, path+".type", "tool type is required")
		}
		switch tool.Type {
		case "function":
			if tool.Custom != nil {
				return conversionResult{}, invalid(ProtocolChat, path+".custom", "is not valid for a function tool")
			}
			if tool.Function.Name == "" {
				return conversionResult{}, invalid(ProtocolChat, path+".function.name", "name is required")
			}
			key := "function\x00" + tool.Function.Name
			if _, duplicate := seenToolNames[key]; duplicate {
				return conversionResult{}, invalid(ProtocolChat, path+".function.name", "duplicate function name %q", tool.Function.Name)
			}
			seenToolNames[key] = struct{}{}
			parameters, err := normalizeFunctionParameters(ProtocolChat, path+".function.parameters", tool.Function.Parameters)
			if err != nil {
				return conversionResult{}, err
			}
			strict := tool.Function.Strict
			if strict == nil {
				defaultStrict := false
				strict = &defaultStrict
			}
			target.Tools = append(target.Tools, responsesTool{Type: "function", Name: tool.Function.Name, Description: tool.Function.Description, Parameters: parameters, Strict: strict})
		case "custom":
			if target.Stream {
				return conversionResult{}, unsupported(ProtocolChat, path+".type", "custom tool calls have no official Chat streaming delta representation")
			}
			if tool.Custom == nil || tool.Custom.Name == "" {
				return conversionResult{}, invalid(ProtocolChat, path+".custom.name", "name is required")
			}
			key := "custom\x00" + tool.Custom.Name
			if _, duplicate := seenToolNames[key]; duplicate {
				return conversionResult{}, invalid(ProtocolChat, path+".custom.name", "duplicate custom tool name %q", tool.Custom.Name)
			}
			seenToolNames[key] = struct{}{}
			if tool.Function.Name != "" || tool.Function.Description != "" || nonNullJSON(tool.Function.Parameters) || tool.Function.Strict != nil {
				return conversionResult{}, invalid(ProtocolChat, path+".function", "is not valid for a custom tool")
			}
			format, err := chatCustomFormatToResponses(tool.Custom.Format, path+".custom.format")
			if err != nil {
				return conversionResult{}, err
			}
			target.Tools = append(target.Tools, responsesTool{Type: "custom", Name: tool.Custom.Name, Description: tool.Custom.Description, Format: format})
		default:
			return conversionResult{}, unsupported(ProtocolChat, path+".type", "tool type %q requires a native Chat provider", tool.Type)
		}
	}
	webSearch, err := chatWebSearchToResponses(source.WebSearchOptions)
	if err != nil {
		return conversionResult{}, err
	}
	if webSearch != nil {
		if target.Stream {
			return conversionResult{}, unsupported(ProtocolChat, "$.web_search_options", "web-search citations have no official Chat streaming representation")
		}
		target.Tools = append(target.Tools, *webSearch)
	}
	if err := validateToolChoiceReferences(ProtocolChat, choice, target.Tools); err != nil {
		return conversionResult{}, err
	}
	if choice.Mode != "" {
		target.ToolChoice = encodeResponsesToolChoice(choice)
	}
	items := make([]responsesItem, 0, len(source.Messages))
	callKinds := make(map[string]string)
	consumedCallIDs := make(map[string]bool)
	for index, message := range source.Messages {
		path := fmt.Sprintf("$.messages[%d]", index)
		if nonNullJSON(message.Annotations) {
			return conversionResult{}, invalid(ProtocolChat, path+".annotations", "annotations are response-only")
		}
		if rawJSONValuePresent(message.Audio) {
			return conversionResult{}, unsupported(ProtocolChat, path+".audio", "Chat assistant audio cannot be represented by Responses input")
		}
		if rawJSONValuePresent(message.FunctionCall) {
			return conversionResult{}, unsupported(ProtocolChat, path+".function_call", "deprecated function_call cannot be represented without semantic loss")
		}
		if message.Role != "assistant" && len(message.ToolCalls) > 0 {
			return conversionResult{}, invalid(ProtocolChat, path+".tool_calls", "tool_calls are only valid on assistant messages")
		}
		if message.Role != "assistant" && message.Refusal != "" {
			return conversionResult{}, invalid(ProtocolChat, path+".refusal", "refusal is only valid on assistant messages")
		}
		if message.Role != "assistant" && message.ReasoningContent != "" {
			return conversionResult{}, invalid(ProtocolChat, path+".reasoning_content", "reasoning_content is only valid on assistant messages")
		}
		if message.Role != "tool" && message.ToolCallID != "" {
			return conversionResult{}, invalid(ProtocolChat, path+".tool_call_id", "tool_call_id is only valid on tool messages")
		}
		if message.Name != "" {
			return conversionResult{}, unsupported(ProtocolChat, path+".name", "Responses messages cannot preserve Chat message names")
		}
		if message.ReasoningContent != "" {
			return conversionResult{}, unsupported(ProtocolChat, path+".reasoning_content", "provider reasoning cannot be injected into Responses input")
		}
		if message.Role == "tool" {
			if message.ToolCallID == "" {
				return conversionResult{}, invalid(ProtocolChat, path+".tool_call_id", "tool_call_id is required")
			}
			parts, err := decodeChatContent(message.Content, path+".content")
			if err != nil {
				return conversionResult{}, err
			}
			if err := validateChatContentRole(message.Role, parts, path+".content"); err != nil {
				return conversionResult{}, err
			}
			output, err := chatToolOutputToResponses(parts, path+".content")
			if err != nil {
				return conversionResult{}, err
			}
			callKind, found := callKinds[message.ToolCallID]
			if !found {
				return conversionResult{}, unsupported(ProtocolChat, path+".tool_call_id", "tool result type is ambiguous without a preceding tool call in the request")
			}
			if consumedCallIDs[message.ToolCallID] {
				return conversionResult{}, invalid(ProtocolChat, path+".tool_call_id", "tool call %q already has an output", message.ToolCallID)
			}
			consumedCallIDs[message.ToolCallID] = true
			outputType := "function_call_output"
			if callKind == "custom" {
				outputType = "custom_tool_call_output"
			}
			items = append(items, responsesItem{Type: outputType, CallID: message.ToolCallID, Output: output})
			continue
		}
		if message.Role != "system" && message.Role != "developer" && message.Role != "user" && message.Role != "assistant" {
			return conversionResult{}, invalid(ProtocolChat, path+".role", "unsupported role %q", message.Role)
		}
		parts, err := decodeChatContent(message.Content, path+".content")
		if err != nil {
			return conversionResult{}, err
		}
		if err := validateChatContentRole(message.Role, parts, path+".content"); err != nil {
			return conversionResult{}, err
		}
		if message.Refusal != "" {
			return conversionResult{}, unsupported(ProtocolChat, path+".refusal", "refusal input has no portable Responses request representation")
		}
		if message.Role == "assistant" {
			for partIndex, part := range parts {
				if jsonValuePresent(part.PromptCacheBreakpoint) {
					return conversionResult{}, unsupported(ProtocolChat, fmt.Sprintf("%s.content[%d].prompt_cache_breakpoint", path, partIndex), "Responses output_text cannot carry an input prompt cache breakpoint")
				}
			}
		}
		if len(parts) > 0 {
			content, err := encodeResponsesContent(parts, message.Role != "assistant")
			if err != nil {
				return conversionResult{}, err
			}
			items = append(items, responsesItem{Type: "message", Role: message.Role, Content: mustJSON(content)})
		}
		for callIndex, call := range message.ToolCalls {
			callPath := fmt.Sprintf("%s.tool_calls[%d]", path, callIndex)
			switch call.Type {
			case "", "function":
				if call.Custom != nil {
					return conversionResult{}, invalid(ProtocolChat, callPath+".custom", "is not valid for a function call")
				}
				if call.ID == "" || call.Function.Name == "" {
					return conversionResult{}, invalid(ProtocolChat, callPath, "tool call id and function name are required")
				}
				arguments, err := normalizeOpenAIToolArguments(ProtocolChat, callPath+".function.arguments", call.Function.Arguments)
				if err != nil {
					return conversionResult{}, err
				}
				if previous, duplicate := callKinds[call.ID]; duplicate {
					return conversionResult{}, invalid(ProtocolChat, callPath+".id", "duplicate tool call id %q (already used by %s call)", call.ID, previous)
				}
				callKinds[call.ID] = "function"
				items = append(items, responsesItem{Type: "function_call", CallID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(mustJSONString(string(arguments)))})
			case "custom":
				if target.Stream {
					return conversionResult{}, unsupported(ProtocolChat, callPath+".type", "custom tool calls have no official Chat streaming delta representation")
				}
				if call.ID == "" || call.Custom == nil || call.Custom.Name == "" || call.Custom.Input == nil {
					return conversionResult{}, invalid(ProtocolChat, callPath, "custom tool call id and name are required")
				}
				if call.Function.Name != "" || nonNullJSON(call.Function.Arguments) {
					return conversionResult{}, invalid(ProtocolChat, callPath+".function", "is not valid for a custom call")
				}
				if previous, duplicate := callKinds[call.ID]; duplicate {
					return conversionResult{}, invalid(ProtocolChat, callPath+".id", "duplicate tool call id %q (already used by %s call)", call.ID, previous)
				}
				callKinds[call.ID] = "custom"
				items = append(items, responsesItem{Type: "custom_tool_call", CallID: call.ID, Name: call.Custom.Name, Input: json.RawMessage(mustJSONString(*call.Custom.Input))})
			default:
				return conversionResult{}, unsupported(ProtocolChat, callPath+".type", "tool call type %q is unsupported", call.Type)
			}
		}
	}
	target.Input = mustJSON(items)
	body, err := marshal(ProtocolResponses, target)
	return conversionResult{Body: body}, err
}

func (c *chatToResponsesConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source responsesResponse
	if err := decodeJSON(ProtocolResponses, input, &source); err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesTerminal(source); err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesOutputItems(ProtocolResponses, input); err != nil {
		return conversionResult{}, err
	}
	var target chatResponse
	target.ID, target.Object, target.Created, target.Model = source.ID, "chat.completion", source.CreatedAt, source.Model
	target.Metadata, target.ServiceTier = source.Metadata, source.ServiceTier
	if err := openaicompat.ValidateResponsesServiceTierForChat(source.ServiceTier, "$.service_tier", true); err != nil {
		return conversionResult{}, err
	}
	if err := openaicompat.ValidateCompleteModeration(source.Moderation, ProtocolResponses, "$.moderation", true); err != nil {
		return conversionResult{}, err
	}
	moderation, err := openaicompat.ResponsesModerationToChat(source.Moderation, "$.moderation", true)
	if err != nil {
		return conversionResult{}, err
	}
	target.Moderation = moderation
	if options.Exchange.ClientModel != "" {
		target.Model = options.Exchange.ClientModel
	}
	message := chatMessage{Role: "assistant", Content: json.RawMessage(`null`)}
	var content []portablePart
	var contentLogprobs []json.RawMessage
	var citations []urlCitation
	diagnostics := responsesPhaseDiagnostics(source.Output, "$.output")
	sawWebSearch := false
	sawPortableOutput := false
	if nonNullJSON(source.PromptCacheOptions) {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolResponses, "$.prompt_cache_options", "Chat responses have no prompt cache options field")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "responses_prompt_cache_options_not_representable", "$.prompt_cache_options", "Responses prompt cache options were omitted from the Chat response")
	}
	if err := rejectUnrepresentableResponsesUsage(input, "$"); err != nil {
		return conversionResult{}, err
	}
	finish := finishStop
	seenToolCallIDs := make(map[string]string)
	for index, item := range source.Output {
		path := fmt.Sprintf("$.output[%d]", index)
		switch item.Type {
		case "message":
			sawPortableOutput = true
			parts, logprobs, convertedCitations, err := responsesContentLogprobsAndAnnotations(item.Content, path+".content", true, textRuneCount(content))
			if err != nil {
				return conversionResult{}, err
			}
			contentLogprobs = append(contentLogprobs, logprobs...)
			citations = append(citations, convertedCitations...)
			for _, part := range parts {
				if part.Kind == partRefusal {
					message.Refusal += part.Text
				} else {
					content = append(content, part)
				}
			}
		case "function_call":
			sawPortableOutput = true
			if previous, duplicate := seenToolCallIDs[item.CallID]; duplicate {
				return conversionResult{}, upstreamResponseError(ProtocolResponses, path+".call_id", "duplicate tool call id %q (already used by %s call)", item.CallID, previous)
			}
			seenToolCallIDs[item.CallID] = "function"
			if item.Status != "" && item.Status != "completed" {
				return conversionResult{}, unsupported(ProtocolResponses, path+".status", "Chat cannot preserve function_call status %q", item.Status)
			}
			arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, path+".arguments", item.Arguments)
			if err != nil {
				return conversionResult{}, err
			}
			var call chatToolCall
			call.ID, call.Type, call.Function.Name = item.CallID, "function", item.Name
			call.Function.Arguments = json.RawMessage(mustJSONString(string(arguments)))
			message.ToolCalls = append(message.ToolCalls, call)
			finish = finishToolCalls
			if item.ID != "" && item.ID != item.CallID {
				diagnostics = appendDiagnostic(diagnostics, "warning", "responses_item_id_not_representable", path+".id", "Chat preserves call_id but has no separate output item id")
			}
		case "custom_tool_call":
			sawPortableOutput = true
			if previous, duplicate := seenToolCallIDs[item.CallID]; duplicate {
				return conversionResult{}, upstreamResponseError(ProtocolResponses, path+".call_id", "duplicate tool call id %q (already used by %s call)", item.CallID, previous)
			}
			seenToolCallIDs[item.CallID] = "custom"
			if item.Status != "" && item.Status != "completed" {
				return conversionResult{}, unsupported(ProtocolResponses, path+".status", "Chat cannot preserve custom_tool_call status %q", item.Status)
			}
			input, err := customInput(item.Input, ProtocolResponses, path+".input")
			if err != nil {
				return conversionResult{}, err
			}
			call := chatToolCall{ID: item.CallID, Type: "custom", Custom: &chatCustomToolCall{Name: item.Name, Input: &input}}
			message.ToolCalls = append(message.ToolCalls, call)
			finish = finishToolCalls
			if item.ID != "" && item.ID != item.CallID {
				diagnostics = appendDiagnostic(diagnostics, "warning", "responses_item_id_not_representable", path+".id", "Chat preserves call_id but has no separate output item id")
			}
		case "web_search_call":
			sawWebSearch = true
			diagnostics = appendDiagnostic(diagnostics, "warning", "responses_web_search_call_not_representable", path, "Chat preserves cited answer text but has no web-search lifecycle output item")
		case "reasoning":
			sawPortableOutput = true
			summaryParts, err := decodeResponsesContentRaw(item.Summary, path+".summary", false)
			if err != nil {
				return conversionResult{}, err
			}
			contentParts, err := decodeResponsesContentRaw(item.Content, path+".content", false)
			if err != nil {
				return conversionResult{}, err
			}
			if len(summaryParts) > 0 {
				if options.LossPolicy == rejectSemanticLoss {
					return conversionResult{}, unsupported(ProtocolResponses, path+".summary", "Chat reasoning_content represents raw reasoning text, not a Responses reasoning summary")
				}
				diagnostics = appendDiagnostic(diagnostics, "warning", "responses_reasoning_summary_mapped_to_reasoning_content", path+".summary", "Responses reasoning summary was appended to Chat reasoning_content; summary and raw-reasoning boundaries are not representable")
				message.ReasoningContent += joinText(summaryParts)
			}
			message.ReasoningContent += joinText(contentParts)
		default:
			return conversionResult{}, unsupported(ProtocolResponses, path+".type", "output item %q cannot be represented by Chat", item.Type)
		}
	}
	if sawWebSearch && !sawPortableOutput {
		return conversionResult{}, unsupported(ProtocolResponses, "$.output", "a web_search_call without a portable answer item cannot be represented by Chat")
	}
	encodedContent, err := encodeChatResponseText(content, "$.output.content")
	if err != nil {
		return conversionResult{}, err
	}
	message.Content = encodedContent
	message.Annotations = encodeChatURLCitations(citations)
	if source.Status == "incomplete" {
		finish = finishLength
		if source.IncompleteDetails != nil && source.IncompleteDetails.Reason == "content_filter" {
			finish = finishContentFilter
		}
	} else if source.Status == "failed" || source.Status == "cancelled" {
		finish = finishError
	}
	target.Choices = append(target.Choices, struct {
		Index        int             `json:"index"`
		Message      chatMessage     `json:"message"`
		FinishReason string          `json:"finish_reason"`
		Logprobs     json.RawMessage `json:"logprobs,omitempty"`
	}{Message: message, FinishReason: string(finish), Logprobs: encodeChatLogprobs(contentLogprobs, nil)})
	target.Usage.PromptTokens = source.Usage.InputTokens
	target.Usage.CompletionTokens = source.Usage.OutputTokens
	target.Usage.TotalTokens = source.Usage.TotalTokens
	target.Usage.PromptDetails.CachedTokens = source.Usage.InputTokenDetails.CachedTokens
	target.Usage.CompletionDetails.ReasoningTokens = source.Usage.OutputTokenDetails.ReasoningTokens
	body, err := marshal(ProtocolChat, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func appendChatToolCall(messages []chatMessage, call chatToolCall) []chatMessage {
	if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
		messages[len(messages)-1].ToolCalls = append(messages[len(messages)-1].ToolCalls, call)
		return messages
	}
	return append(messages, chatMessage{Role: "assistant", Content: json.RawMessage(`null`), ToolCalls: []chatToolCall{call}})
}

func responsesToolOutputToChatContent(raw json.RawMessage, path string) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`""`), nil
	}
	if raw[0] == '"' {
		return append(json.RawMessage(nil), raw...), nil
	}
	if raw[0] == '[' {
		parts, err := decodeResponsesContentRaw(raw, path, true)
		if err != nil {
			return nil, err
		}
		for index, part := range parts {
			if part.Kind != partText {
				return nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].type", path, index), "Chat tool messages only support text content")
			}
		}
		return encodeChatContent(parts)
	}
	return json.RawMessage(mustJSONString(string(raw))), nil
}

func chatToolOutputToResponses(parts []portablePart, path string) (json.RawMessage, error) {
	if len(parts) == 0 {
		return json.RawMessage(`""`), nil
	}
	textOnly := true
	var text strings.Builder
	for _, part := range parts {
		if part.Kind != partText {
			textOnly = false
			break
		}
		text.WriteString(part.Text)
	}
	if textOnly {
		return json.RawMessage(mustJSONString(text.String())), nil
	}
	content, err := encodeResponsesContent(parts, true)
	if err != nil {
		return nil, unsupported(ProtocolChat, path, "tool output cannot be represented by Responses: %v", err)
	}
	return mustJSON(content), nil
}

func (c *chatToResponsesConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return &responsesToChatStreamConverter{clientModel: options.Exchange.ClientModel, includeUsage: options.Exchange.ChatStreamIncludeUsage, lossPolicy: options.LossPolicy, items: make(map[string]responsesItem), completedItems: make(map[string]bool), toolIndexes: make(map[string]int), toolArguments: make(map[int]string)}, nil
}
