package chatresponses

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (c *chatToResponsesConverter) Specification() routeSpec { return c.spec }

func (c *chatToResponsesConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolChat, input, "model", "messages", "tools", "tool_choice", "max_tokens", "max_completion_tokens", "temperature", "top_p", "stop", "stream", "parallel_tool_calls", "response_format", "reasoning_effort", "metadata", "n", "frequency_penalty", "presence_penalty", "logprobs", "top_logprobs", "verbosity", "user", "service_tier", "store", "prompt_cache_key", "prompt_cache_retention", "safety_identifier", "stream_options"); err != nil {
		return conversionResult{}, err
	}
	var source chatRequest
	if err := decodeJSON(ProtocolChat, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Model == "" || len(source.Messages) == 0 {
		return conversionResult{}, invalid(ProtocolChat, "$.messages", "model and at least one message are required")
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
	if err := validateChatStreamOptions(input); err != nil {
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
	if source.ReasoningEffort != "" {
		target.Reasoning = &reasoningConfig{Effort: source.ReasoningEffort}
	}
	choice, err := decodeChatToolChoice(source.ToolChoice)
	if err != nil {
		return conversionResult{}, err
	}
	if choice.Mode != "" {
		target.ToolChoice = encodeResponsesToolChoice(choice)
	}
	format, err := decodeChatResponseFormat(source.ResponseFormat)
	if err != nil {
		return conversionResult{}, err
	}
	if format != nil || rawJSONValuePresent(source.Verbosity) {
		text := map[string]any{}
		if format != nil {
			value := map[string]any{"type": "json_schema", "name": format.Name, "schema": format.Schema}
			if format.Description != "" {
				value["description"] = format.Description
			}
			if format.Strict != nil {
				value["strict"] = *format.Strict
			}
			text["format"] = value
		}
		if rawJSONValuePresent(source.Verbosity) {
			text["verbosity"] = json.RawMessage(source.Verbosity)
		}
		target.Text = mustJSON(text)
	}
	for index, tool := range source.Tools {
		if tool.Type != "function" {
			return conversionResult{}, unsupported(ProtocolChat, fmt.Sprintf("$.tools[%d].type", index), "tool type %q requires a native Chat provider", tool.Type)
		}
		if tool.Function.Name == "" {
			return conversionResult{}, invalid(ProtocolChat, fmt.Sprintf("$.tools[%d].function.name", index), "name is required")
		}
		parameters, err := normalizeFunctionParameters(ProtocolChat, fmt.Sprintf("$.tools[%d].function.parameters", index), tool.Function.Parameters)
		if err != nil {
			return conversionResult{}, err
		}
		strict := tool.Function.Strict
		if strict == nil {
			defaultStrict := false
			strict = &defaultStrict
		}
		target.Tools = append(target.Tools, responsesTool{Type: "function", Name: tool.Function.Name, Description: tool.Function.Description, Parameters: parameters, Strict: strict})
	}
	items := make([]responsesItem, 0, len(source.Messages))
	for index, message := range source.Messages {
		path := fmt.Sprintf("$.messages[%d]", index)
		if rawJSONValuePresent(message.Audio) {
			return conversionResult{}, unsupported(ProtocolChat, path+".audio", "Chat assistant audio cannot be represented by Responses input")
		}
		if rawJSONValuePresent(message.FunctionCall) {
			return conversionResult{}, unsupported(ProtocolChat, path+".function_call", "deprecated function_call cannot be represented without semantic loss")
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
			output, err := chatToolOutputToResponses(parts, path+".content")
			if err != nil {
				return conversionResult{}, err
			}
			items = append(items, responsesItem{Type: "function_call_output", CallID: message.ToolCallID, Output: output})
			continue
		}
		if message.Role != "system" && message.Role != "developer" && message.Role != "user" && message.Role != "assistant" {
			return conversionResult{}, invalid(ProtocolChat, path+".role", "unsupported role %q", message.Role)
		}
		parts, err := decodeChatContent(message.Content, path+".content")
		if err != nil {
			return conversionResult{}, err
		}
		if message.Refusal != "" {
			return conversionResult{}, unsupported(ProtocolChat, path+".refusal", "refusal input has no portable Responses request representation")
		}
		if len(parts) > 0 {
			content, err := encodeResponsesContent(parts, true)
			if err != nil {
				return conversionResult{}, err
			}
			items = append(items, responsesItem{Type: "message", Role: message.Role, Content: mustJSON(content)})
		}
		for callIndex, call := range message.ToolCalls {
			if call.Type != "" && call.Type != "function" {
				return conversionResult{}, unsupported(ProtocolChat, fmt.Sprintf("%s.tool_calls[%d].type", path, callIndex), "tool call type %q is unsupported", call.Type)
			}
			if call.ID == "" || call.Function.Name == "" {
				return conversionResult{}, invalid(ProtocolChat, fmt.Sprintf("%s.tool_calls[%d]", path, callIndex), "tool call id and function name are required")
			}
			arguments, err := normalizeOpenAIToolArguments(ProtocolChat, fmt.Sprintf("%s.tool_calls[%d].function.arguments", path, callIndex), call.Function.Arguments)
			if err != nil {
				return conversionResult{}, err
			}
			items = append(items, responsesItem{Type: "function_call", CallID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(mustJSONString(string(arguments)))})
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
	var target chatResponse
	target.ID, target.Object, target.Created, target.Model = source.ID, "chat.completion", source.CreatedAt, source.Model
	if options.Exchange.ClientModel != "" {
		target.Model = options.Exchange.ClientModel
	}
	message := chatMessage{Role: "assistant", Content: json.RawMessage(`null`)}
	var content []portablePart
	var contentLogprobs []json.RawMessage
	diagnostics := responsesPhaseDiagnostics(source.Output, "$.output")
	finish := finishStop
	for index, item := range source.Output {
		path := fmt.Sprintf("$.output[%d]", index)
		switch item.Type {
		case "message":
			parts, logprobs, err := responsesContentAndLogprobs(item.Content, path+".content")
			if err != nil {
				return conversionResult{}, err
			}
			contentLogprobs = append(contentLogprobs, logprobs...)
			for _, part := range parts {
				if part.Kind == partRefusal {
					message.Refusal += part.Text
				} else {
					content = append(content, part)
				}
			}
		case "function_call":
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
		case "reasoning":
			parts, err := decodeResponsesContentRaw(item.Summary, path+".summary", false)
			if err != nil {
				return conversionResult{}, err
			}
			message.ReasoningContent += joinText(parts)
			parts, err = decodeResponsesContentRaw(item.Content, path+".content", false)
			if err != nil {
				return conversionResult{}, err
			}
			message.ReasoningContent += joinText(parts)
		default:
			return conversionResult{}, unsupported(ProtocolResponses, path+".type", "output item %q cannot be represented by Chat", item.Type)
		}
	}
	encodedContent, err := encodeChatContent(content)
	if err != nil {
		return conversionResult{}, err
	}
	message.Content = encodedContent
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
	return &responsesToChatStreamConverter{clientModel: options.Exchange.ClientModel, includeUsage: options.Exchange.ChatStreamIncludeUsage, items: make(map[string]responsesItem), completedItems: make(map[string]bool), toolIndexes: make(map[string]int), toolArguments: make(map[int]string)}, nil
}
