package chatresponses

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	chatresponsesstream "github.com/2218342221/RouteMorphSDK/internal/chatresponsesstream"
	"github.com/2218342221/RouteMorphSDK/internal/openaicompat"
)

// chatToResponsesConverter maps the two native DTO families directly. It is
// the highest-quality and most frequently exercised cross-protocol route.
type chatToResponsesConverter struct {
	spec routeSpec
}

type responsesToChatConverter struct {
	spec routeSpec
}

func responsesIncludeLogprobs(raw json.RawMessage) (bool, error) {
	if !rawJSONValuePresent(raw) {
		return false, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return false, invalid(ProtocolResponses, "$.include", "include must be a string array")
	}
	logprobs := false
	for index, value := range values {
		switch value {
		case "message.output_text.logprobs":
			logprobs = true
		default:
			return false, unsupported(ProtocolResponses, fmt.Sprintf("$.include[%d]", index), "included artifact %q has no Chat equivalent", value)
		}
	}
	return logprobs, nil
}

func validateResponsesReasoningForChat(input []byte, reasoning *reasoningConfig) error {
	if reasoning != nil {
		if err := validateOpenAIReasoningEffort(ProtocolResponses, "$.reasoning.effort", reasoning.Effort); err != nil {
			return err
		}
	}
	if reasoning != nil && strings.TrimSpace(reasoning.Summary) != "" {
		return unsupported(ProtocolResponses, "$.reasoning.summary", "reasoning summaries cannot be requested through Chat")
	}
	object, err := rawObject(ProtocolResponses, input)
	if err != nil || !rawJSONValuePresent(object["reasoning"]) {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(object["reasoning"], &fields); err != nil {
		return invalid(ProtocolResponses, "$.reasoning", "reasoning must be an object")
	}
	for name, raw := range fields {
		switch name {
		case "effort", "summary":
		default:
			if rawJSONValuePresent(raw) {
				return unsupported(ProtocolResponses, "$.reasoning."+name, "reasoning field has no Chat equivalent")
			}
		}
	}
	return nil
}

func validateResponsesStreamOptions(raw json.RawMessage) (*bool, error) {
	if !rawJSONValuePresent(raw) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, invalid(ProtocolResponses, "$.stream_options", "stream_options must be an object")
	}
	var includeObfuscation *bool
	for name, value := range fields {
		if name != "include_obfuscation" {
			return nil, unsupported(ProtocolResponses, "$.stream_options."+name, "stream option has no Chat equivalent")
		}
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err != nil {
			return nil, invalid(ProtocolResponses, "$.stream_options.include_obfuscation", "must be a boolean")
		}
		includeObfuscation = &enabled
	}
	return includeObfuscation, nil
}

func inspectChatStreamOptions(input []byte) (includeUsage, includeUsageSet bool, includeObfuscation *bool, err error) {
	object, err := rawObject(ProtocolChat, input)
	if err != nil || !rawJSONValuePresent(object["stream_options"]) {
		return false, false, nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(object["stream_options"], &fields); err != nil {
		return false, false, nil, invalid(ProtocolChat, "$.stream_options", "stream_options must be an object")
	}
	for name, value := range fields {
		if !rawJSONValuePresent(value) {
			return false, false, nil, invalid(ProtocolChat, "$.stream_options."+name, "must be a boolean")
		}
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err != nil {
			return false, false, nil, invalid(ProtocolChat, "$.stream_options."+name, "must be a boolean")
		}
		switch name {
		case "include_usage":
			includeUsage, includeUsageSet = enabled, true
		case "include_obfuscation":
			copy := enabled
			includeObfuscation = &copy
		default:
			return false, false, nil, unsupported(ProtocolChat, "$.stream_options."+name, "stream option has no Responses equivalent")
		}
	}
	return includeUsage, includeUsageSet, includeObfuscation, nil
}

func (c *responsesToChatConverter) Specification() routeSpec { return c.spec }

func (c *responsesToChatConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolResponses, input, "model", "input", "instructions", "tools", "tool_choice", "max_output_tokens", "temperature", "top_p", "stream", "parallel_tool_calls", "reasoning", "text", "metadata", "conversation", "previous_response_id", "prompt", "context_management", "background", "include", "top_logprobs", "service_tier", "store", "prompt_cache_key", "prompt_cache_options", "prompt_cache_retention", "safety_identifier", "stream_options", "truncation", "user", "max_tool_calls", "client_metadata", "moderation"); err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesToolsShape(ProtocolResponses, input); err != nil {
		return conversionResult{}, err
	}
	var source responsesRequest
	if err := decodeJSON(ProtocolResponses, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Model == "" {
		return conversionResult{}, invalid(ProtocolResponses, "$.model", "model is required")
	}
	if err := rejectResponsesState(source); err != nil {
		return conversionResult{}, err
	}
	if err := validatePromptCacheOptions(ProtocolResponses, source.PromptCacheOptions); err != nil {
		return conversionResult{}, err
	}
	if err := validateModeration(ProtocolResponses, source.Moderation); err != nil {
		return conversionResult{}, err
	}
	if err := openaicompat.ValidateResponsesServiceTierForChat(source.ServiceTier, "$.service_tier", false); err != nil {
		return conversionResult{}, err
	}
	if rawJSONValuePresent(source.ClientMetadata) {
		return conversionResult{}, unsupported(ProtocolResponses, "$.client_metadata", "Chat has no client_metadata equivalent")
	}
	if source.MaxToolCalls != nil {
		return conversionResult{}, unsupported(ProtocolResponses, "$.max_tool_calls", "Chat cannot constrain the number of tool calls")
	}
	if rawJSONValuePresent(source.Truncation) && rawString(source.Truncation) != "disabled" {
		return conversionResult{}, unsupported(ProtocolResponses, "$.truncation", "Responses truncation has no Chat equivalent")
	}
	includeObfuscation, err := validateResponsesStreamOptions(source.StreamOptions)
	if err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesReasoningForChat(input, source.Reasoning); err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesTools(source.Tools, "$.tools"); err != nil {
		return conversionResult{}, err
	}
	target := chatRequest{
		Model:                source.Model,
		MaxCompletion:        source.MaxOutputTokens,
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
	if options.Exchange.UpstreamModel != "" {
		target.Model = options.Exchange.UpstreamModel
	}
	target.Stream = resolveExchangeStream(source.Stream, options.Exchange)
	if source.Reasoning != nil {
		target.ReasoningEffort = source.Reasoning.Effort
	}
	logprobs, err := responsesIncludeLogprobs(source.Include)
	if err != nil {
		return conversionResult{}, err
	}
	if source.TopLogprobs != nil && !logprobs {
		return conversionResult{}, invalid(ProtocolResponses, "$.top_logprobs", "top_logprobs requires message.output_text.logprobs in include")
	}
	if logprobs {
		target.Logprobs = new(bool)
		*target.Logprobs = true
	}
	if target.Stream {
		target.StreamOptions = &chatStreamOptions{IncludeUsage: true, IncludeObfuscation: includeObfuscation}
	} else if rawJSONValuePresent(source.StreamOptions) {
		return conversionResult{}, invalid(ProtocolResponses, "$.stream_options", "stream_options requires stream=true")
	}
	choice, err := decodeResponsesToolChoice(source.ToolChoice)
	if err != nil {
		return conversionResult{}, err
	}
	format, verbosity, err := decodeResponsesTextOptions(source.Text)
	if err != nil {
		return conversionResult{}, err
	}
	if format != nil {
		if format.Type == "json_object" {
			target.ResponseFormat = mustJSON(map[string]any{"type": "json_object"})
		} else {
			value := map[string]any{"name": format.Name, "schema": format.Schema}
			if format.Description != "" {
				value["description"] = format.Description
			}
			if format.Strict != nil {
				value["strict"] = *format.Strict
			}
			target.ResponseFormat = mustJSON(map[string]any{"type": "json_schema", "json_schema": value})
		}
	}
	target.Verbosity = verbosity
	seenWebSearch := false
	for index, tool := range source.Tools {
		path := fmt.Sprintf("$.tools[%d]", index)
		switch tool.Type {
		case "function":
			if tool.Strict == nil {
				return conversionResult{}, unsupported(ProtocolResponses, path+".strict", "Responses defaults omitted strict to true, which Chat cannot preserve")
			}
			var converted chatTool
			converted.Type, converted.Function.Name, converted.Function.Description = "function", tool.Name, tool.Description
			parameters, err := normalizeFunctionParameters(ProtocolResponses, path+".parameters", tool.Parameters)
			if err != nil {
				return conversionResult{}, err
			}
			converted.Function.Parameters, converted.Function.Strict = parameters, tool.Strict
			target.Tools = append(target.Tools, converted)
		case "custom":
			if target.Stream {
				return conversionResult{}, unsupported(ProtocolResponses, path+".type", "custom tool calls have no official Chat streaming delta representation")
			}
			format, err := responsesCustomFormatToChat(tool.Format, path+".format")
			if err != nil {
				return conversionResult{}, err
			}
			converted := chatTool{Type: "custom", Custom: &chatCustomTool{Name: tool.Name, Description: tool.Description, Format: format}}
			target.Tools = append(target.Tools, converted)
		case "web_search", "web_search_2025_08_26":
			if target.Stream {
				return conversionResult{}, unsupported(ProtocolResponses, path+".type", "web-search citations have no official Chat streaming representation")
			}
			if seenWebSearch {
				return conversionResult{}, unsupported(ProtocolResponses, path+".type", "Chat supports only one web_search_options object")
			}
			options, err := responsesWebSearchToChat(tool, path)
			if err != nil {
				return conversionResult{}, err
			}
			target.WebSearchOptions = options
			seenWebSearch = true
		default:
			return conversionResult{}, unsupported(ProtocolResponses, path+".type", "built-in tool %q requires a native Responses provider", tool.Type)
		}
	}
	if err := validateToolChoiceReferences(ProtocolResponses, choice, source.Tools); err != nil {
		return conversionResult{}, err
	}
	if choice.Mode != "" {
		target.ToolChoice = encodeChatToolChoice(choice)
	}
	if len(source.Instructions) > 0 && string(source.Instructions) != "null" {
		parts, err := decodeResponsesInstructions(source.Instructions)
		if err != nil {
			return conversionResult{}, err
		}
		if err := validateChatContentRole("system", parts, "$.instructions"); err != nil {
			return conversionResult{}, err
		}
		content, err := encodeChatContent(parts)
		if err != nil {
			return conversionResult{}, err
		}
		target.Messages = append(target.Messages, chatMessage{Role: "system", Content: content})
	}
	items, err := responseInputItems(source.Input)
	if err != nil {
		return conversionResult{}, err
	}
	callIDsByName := make(map[string][]string)
	callKinds := make(map[string]string)
	callNames := make(map[string]string)
	consumedCallIDs := make(map[string]bool)
	for index, item := range items {
		path := fmt.Sprintf("$.input[%d]", index)
		switch item.Type {
		case "message", "":
			parts, err := decodeResponsesContentRaw(item.Content, path+".content", true)
			if err != nil {
				return conversionResult{}, err
			}
			if err := validateChatContentRole(item.Role, parts, path+".content"); err != nil {
				return conversionResult{}, err
			}
			content, err := encodeChatContent(parts)
			if err != nil {
				return conversionResult{}, err
			}
			target.Messages = append(target.Messages, chatMessage{Role: item.Role, Content: content})
		case "function_call":
			if item.ID != "" && item.ID != item.CallID {
				return conversionResult{}, unsupported(ProtocolResponses, path+".id", "Chat cannot preserve a function-call item id separate from call_id")
			}
			arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, path+".arguments", item.Arguments)
			if err != nil {
				return conversionResult{}, err
			}
			if _, duplicate := callKinds[item.CallID]; duplicate {
				return conversionResult{}, invalid(ProtocolResponses, path+".call_id", "duplicate tool call id %q", item.CallID)
			}
			callKinds[item.CallID] = "function"
			callNames[item.CallID] = item.Name
			callIDsByName[item.Name] = append(callIDsByName[item.Name], item.CallID)
			var call chatToolCall
			call.ID, call.Type, call.Function.Name = item.CallID, "function", item.Name
			call.Function.Arguments = json.RawMessage(mustJSONString(string(arguments)))
			target.Messages = appendChatToolCall(target.Messages, call)
		case "function_call_output":
			callID := item.CallID
			if callID == "" && item.Name != "" {
				for _, candidate := range callIDsByName[item.Name] {
					if !consumedCallIDs[candidate] {
						if callID != "" {
							return conversionResult{}, unsupported(ProtocolResponses, path+".name", "multiple pending calls named %q make the output ambiguous without call_id", item.Name)
						}
						callID = candidate
					}
				}
			}
			if callID == "" || callKinds[callID] != "function" {
				return conversionResult{}, unsupported(ProtocolResponses, path+".call_id", "function output cannot be correlated with an earlier function call")
			}
			if item.Name != "" && item.Name != callNames[callID] {
				return conversionResult{}, invalid(ProtocolResponses, path+".name", "function output name %q does not match call name %q", item.Name, callNames[callID])
			}
			if consumedCallIDs[callID] {
				return conversionResult{}, invalid(ProtocolResponses, path+".call_id", "tool call %q already has an output", callID)
			}
			consumedCallIDs[callID] = true
			content, err := responsesToolOutputToChatContent(item.Output, path+".output")
			if err != nil {
				return conversionResult{}, err
			}
			target.Messages = append(target.Messages, chatMessage{Role: "tool", ToolCallID: callID, Content: content})
		case "custom_tool_call":
			if target.Stream {
				return conversionResult{}, unsupported(ProtocolResponses, path+".type", "custom tool calls have no official Chat streaming delta representation")
			}
			if item.ID != "" && item.ID != item.CallID {
				return conversionResult{}, unsupported(ProtocolResponses, path+".id", "Chat cannot preserve a custom-tool item id separate from call_id")
			}
			input, err := customInput(item.Input, ProtocolResponses, path+".input")
			if err != nil {
				return conversionResult{}, err
			}
			if _, duplicate := callKinds[item.CallID]; duplicate {
				return conversionResult{}, invalid(ProtocolResponses, path+".call_id", "duplicate tool call id %q", item.CallID)
			}
			callKinds[item.CallID] = "custom"
			call := chatToolCall{ID: item.CallID, Type: "custom", Custom: &chatCustomToolCall{Name: item.Name, Input: &input}}
			target.Messages = appendChatToolCall(target.Messages, call)
		case "custom_tool_call_output":
			if target.Stream {
				return conversionResult{}, unsupported(ProtocolResponses, path+".type", "custom tool calls have no official Chat streaming delta representation")
			}
			if callKinds[item.CallID] != "custom" {
				return conversionResult{}, unsupported(ProtocolResponses, path+".call_id", "custom output cannot be correlated with an earlier custom tool call")
			}
			if consumedCallIDs[item.CallID] {
				return conversionResult{}, invalid(ProtocolResponses, path+".call_id", "tool call %q already has an output", item.CallID)
			}
			consumedCallIDs[item.CallID] = true
			content, err := responsesToolOutputToChatContent(item.Output, path+".output")
			if err != nil {
				return conversionResult{}, err
			}
			target.Messages = append(target.Messages, chatMessage{Role: "tool", ToolCallID: item.CallID, Content: content})
		case "reasoning":
			return conversionResult{}, unsupported(ProtocolResponses, path, "reasoning input cannot be injected into Chat")
		default:
			return conversionResult{}, unsupported(ProtocolResponses, path+".type", "input item %q requires a native Responses provider", item.Type)
		}
	}
	body, err := marshal(ProtocolChat, target)
	return conversionResult{Body: body}, err
}

func (c *responsesToChatConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source chatResponse
	if err := decodeJSON(ProtocolChat, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Error != nil {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.error", "%s", source.Error.Message)
	}
	if len(source.Choices) != 1 {
		return conversionResult{}, unsupported(ProtocolChat, "$.choices", "Responses conversion requires exactly one Chat choice")
	}
	if err := openaicompat.ValidateChatServiceTier(source.ServiceTier, "$.service_tier", true); err != nil {
		return conversionResult{}, err
	}
	if err := openaicompat.ValidateCompleteModeration(source.Moderation, ProtocolChat, "$.moderation", true); err != nil {
		return conversionResult{}, err
	}
	moderation, err := openaicompat.ChatModerationToResponses(source.Moderation, "$.moderation", true)
	if err != nil {
		return conversionResult{}, err
	}
	target := responsesResponse{
		ID: source.ID, Object: "response", CreatedAt: source.Created, Model: source.Model, Status: "completed",
		Metadata: source.Metadata, Moderation: moderation, ServiceTier: source.ServiceTier,
	}
	var diagnostics []Diagnostic
	if source.SystemFingerprint != "" {
		diagnostics = appendDiagnostic(diagnostics, "warning", "chat_system_fingerprint_not_representable", "$.system_fingerprint", "Responses has no system_fingerprint field")
	}
	if err := rejectUnrepresentableChatUsage(input); err != nil {
		return conversionResult{}, err
	}
	if options.Exchange.ClientModel != "" {
		target.Model = options.Exchange.ClientModel
	}
	choice := source.Choices[0]
	if choice.Message.Role != "" && choice.Message.Role != "assistant" {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.choices[0].message.role", "expected assistant, got %q", choice.Message.Role)
	}
	if rawJSONValuePresent(choice.Message.Audio) {
		return conversionResult{}, unsupported(ProtocolChat, "$.choices[0].message.audio", "Responses output cannot preserve Chat audio output")
	}
	if rawJSONValuePresent(choice.Message.FunctionCall) {
		return conversionResult{}, unsupported(ProtocolChat, "$.choices[0].message.function_call", "deprecated function_call cannot be represented without semantic loss")
	}
	contentLogprobs, refusalLogprobs, err := decodeChatLogprobs(choice.Logprobs, "$.choices[0].logprobs")
	if err != nil {
		return conversionResult{}, err
	}
	if len(refusalLogprobs) > 0 {
		return conversionResult{}, unsupported(ProtocolChat, "$.choices[0].logprobs.refusal", "Responses has no refusal-logprobs representation")
	}
	parts, err := decodeChatContent(choice.Message.Content, "$.choices[0].message.content")
	if err != nil {
		return conversionResult{}, err
	}
	for index, part := range parts {
		if part.Kind != partText || jsonValuePresent(part.PromptCacheBreakpoint) {
			return conversionResult{}, upstreamResponseError(ProtocolChat, fmt.Sprintf("$.choices[0].message.content[%d]", index), "Chat response content must be plain text")
		}
	}
	citations, err := decodeChatURLCitations(choice.Message.Annotations, "$.choices[0].message.annotations", utf8.RuneCountInString(joinText(parts)))
	if err != nil {
		return conversionResult{}, err
	}
	if choice.Message.Refusal != "" {
		parts = append(parts, portablePart{Kind: partRefusal, Text: choice.Message.Refusal})
	}
	if len(parts) > 0 {
		content, err := encodeResponsesContent(parts, false)
		if err != nil {
			return conversionResult{}, err
		}
		content, err = attachResponsesLogprobs(content, contentLogprobs, "$.choices[0].logprobs.content")
		if err != nil {
			return conversionResult{}, err
		}
		if len(citations) > 0 {
			if len(content) != 1 || content[0].Type != "output_text" {
				return conversionResult{}, unsupported(ProtocolChat, "$.choices[0].message.annotations", "URL citations require one text output part")
			}
			content[0].Annotations = encodeResponsesURLCitations(citations)
		}
		target.Output = append(target.Output, responsesItem{Type: "message", ID: "msg_" + source.ID, Role: "assistant", Content: mustJSON(content), Status: "completed"})
	} else if len(contentLogprobs) > 0 {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.choices[0].logprobs.content", "content logprobs were returned without message content")
	} else if len(citations) > 0 {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.choices[0].message.annotations", "URL citations were returned without message content")
	}
	if choice.Message.ReasoningContent != "" {
		target.Output = append(target.Output, responsesItem{
			Type: "reasoning", ID: "rs_" + source.ID, Status: "completed",
			Summary: mustJSON([]responsesContentPart{}),
			Content: mustJSON([]responsesContentPart{{Type: "reasoning_text", Text: choice.Message.ReasoningContent}}),
		})
	}
	seenToolCallIDs := make(map[string]string)
	for index, call := range choice.Message.ToolCalls {
		path := fmt.Sprintf("$.choices[0].message.tool_calls[%d]", index)
		switch call.Type {
		case "", "function":
			if call.Custom != nil {
				return conversionResult{}, upstreamResponseError(ProtocolChat, path+".custom", "is not valid for a function tool call")
			}
			if call.ID == "" || call.Function.Name == "" {
				return conversionResult{}, upstreamResponseError(ProtocolChat, path, "function tool call requires id and name")
			}
			if previous, duplicate := seenToolCallIDs[call.ID]; duplicate {
				return conversionResult{}, upstreamResponseError(ProtocolChat, path+".id", "duplicate tool call id %q (already used by %s call)", call.ID, previous)
			}
			seenToolCallIDs[call.ID] = "function"
			arguments, err := normalizeOpenAIToolArguments(ProtocolChat, path+".function.arguments", call.Function.Arguments)
			if err != nil {
				return conversionResult{}, err
			}
			target.Output = append(target.Output, responsesItem{Type: "function_call", ID: "fc_" + call.ID, CallID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(mustJSONString(string(arguments))), Status: "completed"})
		case "custom":
			if call.ID == "" || call.Custom == nil || call.Custom.Name == "" || call.Custom.Input == nil {
				return conversionResult{}, upstreamResponseError(ProtocolChat, path, "custom tool call requires id, name, and input")
			}
			if previous, duplicate := seenToolCallIDs[call.ID]; duplicate {
				return conversionResult{}, upstreamResponseError(ProtocolChat, path+".id", "duplicate tool call id %q (already used by %s call)", call.ID, previous)
			}
			seenToolCallIDs[call.ID] = "custom"
			if call.Function.Name != "" || nonNullJSON(call.Function.Arguments) {
				return conversionResult{}, upstreamResponseError(ProtocolChat, path+".function", "is not valid for a custom tool call")
			}
			target.Output = append(target.Output, responsesItem{Type: "custom_tool_call", ID: "ctc_" + call.ID, CallID: call.ID, Name: call.Custom.Name, Input: json.RawMessage(mustJSONString(*call.Custom.Input)), Status: "completed"})
		default:
			return conversionResult{}, unsupported(ProtocolChat, path+".type", "tool call type %q is unsupported", call.Type)
		}
	}
	finish, err := parseChatFinish(choice.FinishReason)
	if err != nil {
		return conversionResult{}, err
	}
	if finish == finishLength || finish == finishContentFilter {
		target.Status = "incomplete"
		reason := "max_output_tokens"
		if finish == finishContentFilter {
			reason = "content_filter"
		}
		target.IncompleteDetails = &struct {
			Reason string `json:"reason"`
		}{Reason: reason}
	}
	target.Usage.InputTokens = source.Usage.PromptTokens
	target.Usage.OutputTokens = source.Usage.CompletionTokens
	target.Usage.TotalTokens = source.Usage.TotalTokens
	target.Usage.InputTokenDetails.CachedTokens = source.Usage.PromptDetails.CachedTokens
	target.Usage.OutputTokenDetails.ReasoningTokens = source.Usage.CompletionDetails.ReasoningTokens
	body, err := marshal(ProtocolResponses, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *responsesToChatConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return chatresponsesstream.New(chatresponsesstream.Options{ClientModel: options.Exchange.ClientModel}), nil
}
