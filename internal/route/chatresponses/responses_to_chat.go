package chatresponses

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	chatresponsesstream "github.com/2218342221/RouteMorphSDK/internal/chatresponsesstream"
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

func validateResponsesStreamOptions(raw json.RawMessage) error {
	if !rawJSONValuePresent(raw) {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return invalid(ProtocolResponses, "$.stream_options", "stream_options must be an object")
	}
	for name, value := range fields {
		if name != "include_obfuscation" {
			return unsupported(ProtocolResponses, "$.stream_options."+name, "stream option has no Chat equivalent")
		}
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err != nil {
			return invalid(ProtocolResponses, "$.stream_options.include_obfuscation", "must be a boolean")
		}
		if enabled {
			return unsupported(ProtocolResponses, "$.stream_options.include_obfuscation", "Chat cannot preserve Responses stream obfuscation")
		}
	}
	return nil
}

func validateChatStreamOptions(input []byte) error {
	_, _, err := inspectChatStreamIncludeUsage(input)
	return err
}

func inspectChatStreamIncludeUsage(input []byte) (bool, bool, error) {
	object, err := rawObject(ProtocolChat, input)
	if err != nil || !rawJSONValuePresent(object["stream_options"]) {
		return false, false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(object["stream_options"], &fields); err != nil {
		return false, false, invalid(ProtocolChat, "$.stream_options", "stream_options must be an object")
	}
	includeUsage, includeUsageSet := false, false
	for name, value := range fields {
		if name != "include_usage" {
			return false, false, unsupported(ProtocolChat, "$.stream_options."+name, "stream option has no Responses equivalent")
		}
		if !rawJSONValuePresent(value) {
			return false, false, invalid(ProtocolChat, "$.stream_options.include_usage", "must be a boolean")
		}
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err != nil {
			return false, false, invalid(ProtocolChat, "$.stream_options.include_usage", "must be a boolean")
		}
		includeUsage, includeUsageSet = enabled, true
	}
	return includeUsage, includeUsageSet, nil
}

func (c *responsesToChatConverter) Specification() routeSpec { return c.spec }

func (c *responsesToChatConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolResponses, input, "model", "input", "instructions", "tools", "tool_choice", "max_output_tokens", "temperature", "top_p", "stream", "parallel_tool_calls", "reasoning", "text", "metadata", "conversation", "previous_response_id", "prompt", "context_management", "background", "include", "top_logprobs", "frequency_penalty", "presence_penalty", "service_tier", "store", "prompt_cache_key", "prompt_cache_options", "prompt_cache_retention", "safety_identifier", "stream_options", "truncation", "user", "max_tool_calls", "client_metadata", "moderation"); err != nil {
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
	if rawJSONValuePresent(source.PromptCacheOptions) {
		return conversionResult{}, unsupported(ProtocolResponses, "$.prompt_cache_options", "Chat has no prompt_cache_options equivalent")
	}
	if rawJSONValuePresent(source.ClientMetadata) {
		return conversionResult{}, unsupported(ProtocolResponses, "$.client_metadata", "Chat has no client_metadata equivalent")
	}
	if rawJSONValuePresent(source.Moderation) {
		return conversionResult{}, unsupported(ProtocolResponses, "$.moderation", "Chat has no moderation equivalent")
	}
	if source.MaxToolCalls != nil {
		return conversionResult{}, unsupported(ProtocolResponses, "$.max_tool_calls", "Chat cannot constrain the number of tool calls")
	}
	if rawJSONValuePresent(source.Truncation) && rawString(source.Truncation) != "disabled" {
		return conversionResult{}, unsupported(ProtocolResponses, "$.truncation", "Responses truncation has no Chat equivalent")
	}
	if err := validateResponsesStreamOptions(source.StreamOptions); err != nil {
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
		FrequencyPenalty:     source.FrequencyPenalty,
		PresencePenalty:      source.PresencePenalty,
		TopLogprobs:          source.TopLogprobs,
		User:                 source.User,
		ServiceTier:          source.ServiceTier,
		Store:                source.Store,
		PromptCacheKey:       source.PromptCacheKey,
		PromptCacheRetention: source.PromptCacheRetention,
		SafetyIdentifier:     source.SafetyIdentifier,
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
		target.StreamOptions = &chatStreamOptions{IncludeUsage: true}
	}
	choice, err := decodeResponsesToolChoice(source.ToolChoice)
	if err != nil {
		return conversionResult{}, err
	}
	if choice.Mode != "" {
		target.ToolChoice = encodeChatToolChoice(choice)
	}
	format, verbosity, err := decodeResponsesTextOptions(source.Text)
	if err != nil {
		return conversionResult{}, err
	}
	if format != nil {
		value := map[string]any{"name": format.Name, "schema": format.Schema}
		if format.Description != "" {
			value["description"] = format.Description
		}
		if format.Strict != nil {
			value["strict"] = *format.Strict
		}
		target.ResponseFormat = mustJSON(map[string]any{"type": "json_schema", "json_schema": value})
	}
	target.Verbosity = verbosity
	for index, tool := range source.Tools {
		if tool.Type != "function" {
			return conversionResult{}, unsupported(ProtocolResponses, fmt.Sprintf("$.tools[%d].type", index), "built-in tool %q requires a native Responses provider", tool.Type)
		}
		if tool.Strict == nil {
			return conversionResult{}, unsupported(ProtocolResponses, fmt.Sprintf("$.tools[%d].strict", index), "Responses defaults omitted strict to true, which Chat cannot preserve")
		}
		var converted chatTool
		converted.Type, converted.Function.Name, converted.Function.Description = "function", tool.Name, tool.Description
		parameters, err := normalizeFunctionParameters(ProtocolResponses, fmt.Sprintf("$.tools[%d].parameters", index), tool.Parameters)
		if err != nil {
			return conversionResult{}, err
		}
		converted.Function.Parameters, converted.Function.Strict = parameters, tool.Strict
		target.Tools = append(target.Tools, converted)
	}
	if len(source.Instructions) > 0 && string(source.Instructions) != "null" {
		parts, err := decodeResponsesInstructions(source.Instructions)
		if err != nil {
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
	for index, item := range items {
		path := fmt.Sprintf("$.input[%d]", index)
		switch item.Type {
		case "message", "":
			parts, err := decodeResponsesContentRaw(item.Content, path+".content", true)
			if err != nil {
				return conversionResult{}, err
			}
			content, err := encodeChatContent(parts)
			if err != nil {
				return conversionResult{}, err
			}
			target.Messages = append(target.Messages, chatMessage{Role: item.Role, Content: content})
		case "function_call":
			arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, path+".arguments", item.Arguments)
			if err != nil {
				return conversionResult{}, err
			}
			var call chatToolCall
			call.ID, call.Type, call.Function.Name = item.CallID, "function", item.Name
			call.Function.Arguments = json.RawMessage(mustJSONString(string(arguments)))
			target.Messages = appendChatToolCall(target.Messages, call)
		case "function_call_output":
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
	target := responsesResponse{ID: source.ID, Object: "response", CreatedAt: source.Created, Model: source.Model, Status: "completed"}
	if options.Exchange.ClientModel != "" {
		target.Model = options.Exchange.ClientModel
	}
	choice := source.Choices[0]
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
		target.Output = append(target.Output, responsesItem{Type: "message", ID: "msg_" + source.ID, Role: "assistant", Content: mustJSON(content), Status: "completed"})
	} else if len(contentLogprobs) > 0 {
		return conversionResult{}, upstreamResponseError(ProtocolChat, "$.choices[0].logprobs.content", "content logprobs were returned without message content")
	}
	if choice.Message.ReasoningContent != "" {
		target.Output = append(target.Output, responsesItem{Type: "reasoning", ID: "rs_" + source.ID, Summary: mustJSON([]responsesContentPart{{Type: "summary_text", Text: choice.Message.ReasoningContent}}), Status: "completed"})
	}
	for index, call := range choice.Message.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			return conversionResult{}, unsupported(ProtocolChat, fmt.Sprintf("$.choices[0].message.tool_calls[%d].type", index), "tool call type %q is unsupported", call.Type)
		}
		arguments, err := normalizeOpenAIToolArguments(ProtocolChat, fmt.Sprintf("$.choices[0].message.tool_calls[%d].function.arguments", index), call.Function.Arguments)
		if err != nil {
			return conversionResult{}, err
		}
		target.Output = append(target.Output, responsesItem{Type: "function_call", ID: "fc_" + call.ID, CallID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(mustJSONString(string(arguments))), Status: "completed"})
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
	return conversionResult{Body: body}, err
}

func (c *responsesToChatConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return chatresponsesstream.New(chatresponsesstream.Options{ClientModel: options.Exchange.ClientModel}), nil
}
