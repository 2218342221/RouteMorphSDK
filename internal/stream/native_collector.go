package stream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// collectNativeStreamResponse validates a source stream and reconstructs that
// protocol's own non-streaming response DTO. Pair mappers remain responsible
// for all cross-protocol semantics.
func CollectNativeResponse(protocol Protocol, frames []streamFrame, policy lossPolicy) ([]byte, []Diagnostic, error) {
	switch protocol {
	case ProtocolChat:
		return collectChatStreamResponse(frames, policy)
	case ProtocolResponses:
		return collectResponsesStreamResponse(frames)
	case ProtocolMessages:
		return collectMessagesStreamResponse(frames)
	case ProtocolGenerateContent:
		return collectGeminiStreamResponse(frames, policy)
	default:
		return nil, nil, invalid(protocol, "$", "unsupported stream protocol")
	}
}

func collectResponsesStreamResponse(frames []streamFrame) ([]byte, []Diagnostic, error) {
	var terminalType string
	var terminalResponse json.RawMessage
	for _, frame := range frames {
		if frame.Done || string(frame.Data) == "[DONE]" {
			return nil, nil, invalid(ProtocolResponses, "$", "Responses streams do not use a [DONE] marker")
		}
		if len(bytes.TrimSpace(frame.Data)) == 0 {
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal(frame.Data, &event); err != nil || event.Type == "" {
			return nil, nil, invalid(ProtocolResponses, "$", "invalid Responses stream event")
		}
		if terminalType != "" {
			return nil, nil, invalid(ProtocolResponses, "$.type", "event %q arrived after terminal event %q", event.Type, terminalType)
		}
		switch event.Type {
		case "error", "response.failed", "response.cancelled":
			return nil, nil, upstreamResponseError(ProtocolResponses, "$", "Responses stream returned %q", event.Type)
		case "response.completed", "response.incomplete":
			if len(event.Response) == 0 {
				return nil, nil, invalid(ProtocolResponses, "$.response", "terminal response object is required")
			}
			terminalType = event.Type
			terminalResponse = append(json.RawMessage(nil), event.Response...)
		}
	}
	if terminalType == "" {
		return nil, nil, invalid(ProtocolResponses, "$", "terminal response event is missing")
	}
	if _, err := validateNativeResponsesOutput(terminalResponse); err != nil {
		return nil, nil, err
	}
	var response responsesResponse
	if err := decodeJSON(ProtocolResponses, terminalResponse, &response); err != nil {
		return nil, nil, upstreamResponseError(ProtocolResponses, "$.response", "invalid terminal response object: %v", err)
	}
	if err := validateResponsesTerminal(response); err != nil {
		return nil, nil, err
	}
	if (terminalType == "response.completed") != (response.Status == "completed") || (terminalType == "response.incomplete") != (response.Status == "incomplete") {
		return nil, nil, invalid(ProtocolResponses, "$.response.status", "terminal event %q does not match status %q", terminalType, response.Status)
	}
	return append([]byte(nil), terminalResponse...), nil, nil
}

func collectGeminiStreamResponse(frames []streamFrame, policy lossPolicy) ([]byte, []Diagnostic, error) {
	var response geminiResponse
	var diagnostics []Diagnostic
	sawCandidate, sawTerminal := false, false
	for _, frame := range frames {
		if frame.Done || len(frame.Data) == 0 || string(frame.Data) == "[DONE]" {
			continue
		}
		var chunk geminiResponse
		if err := decodeJSON(ProtocolGenerateContent, frame.Data, &chunk); err != nil {
			return nil, diagnostics, err
		}
		if err := normalizeGeminiTerminalEmptyTextParts(frame.Data, &chunk); err != nil {
			return nil, diagnostics, err
		}
		if reason := geminiPromptBlockReason(chunk.PromptFeedback); reason != "" {
			return nil, diagnostics, upstreamResponseError(ProtocolGenerateContent, "$.promptFeedback.blockReason", "request blocked by Gemini API: %s", reason)
		}
		var envelope struct {
			Usage json.RawMessage `json:"usageMetadata"`
		}
		_ = json.Unmarshal(frame.Data, &envelope)
		if len(chunk.Candidates) == 0 {
			if !jsonValuePresent(envelope.Usage) {
				return nil, diagnostics, upstreamResponseError(ProtocolGenerateContent, "$.candidates", "Gemini stream chunk has neither candidates nor usage")
			}
		} else {
			if len(chunk.Candidates) != 1 {
				return nil, diagnostics, unsupported(ProtocolGenerateContent, "$.candidates", "cross-protocol conversion requires exactly one candidate")
			}
			if sawTerminal {
				return nil, diagnostics, invalid(ProtocolGenerateContent, "$.candidates", "candidate content arrived after the terminal Gemini chunk")
			}
			chunkDiagnostics, err := validateGeminiResponseEnvelope(&chunk, policy)
			if err != nil {
				return nil, diagnostics, err
			}
			diagnostics = append(diagnostics, chunkDiagnostics...)
			candidate := chunk.Candidates[0]
			if len(response.Candidates) == 0 {
				response.Candidates = []geminiCandidate{{Content: geminiContent{Role: "model"}}}
			} else if response.Candidates[0].Index != candidate.Index {
				return nil, diagnostics, upstreamResponseError(ProtocolGenerateContent, "$.candidates[0].index", "candidate index changed from %d to %d during the stream", response.Candidates[0].Index, candidate.Index)
			}
			target := &response.Candidates[0]
			target.Content.Parts = append(target.Content.Parts, candidate.Content.Parts...)
			target.Index = candidate.Index
			if candidate.AvgLogprobs != nil {
				target.AvgLogprobs = candidate.AvgLogprobs
			}
			for source, destination := range map[*json.RawMessage]*json.RawMessage{
				&candidate.LogprobsResult: &target.LogprobsResult, &candidate.SafetyRatings: &target.SafetyRatings,
				&candidate.CitationMetadata: &target.CitationMetadata, &candidate.GroundingMetadata: &target.GroundingMetadata,
				&candidate.URLContextMetadata:    &target.URLContextMetadata,
				&candidate.GroundingAttributions: &target.GroundingAttributions,
			} {
				if jsonValuePresent(*source) {
					*destination = append(json.RawMessage(nil), (*source)...)
				}
			}
			if candidate.TokenCount != nil {
				target.TokenCount = candidate.TokenCount
			}
			sawCandidate = true
			if candidate.FinishReason != "" && candidate.FinishReason != "FINISH_REASON_UNSPECIFIED" {
				if _, err := parseGeminiFinish(candidate.FinishReason); err != nil {
					return nil, diagnostics, err
				}
				target.FinishReason, target.FinishMessage = candidate.FinishReason, candidate.FinishMessage
				sawTerminal = true
			}
		}
		if chunk.ResponseID != "" {
			if response.ResponseID != "" && response.ResponseID != chunk.ResponseID {
				return nil, diagnostics, upstreamResponseError(ProtocolGenerateContent, "$.responseId", "response identity changed from %q to %q during the stream", response.ResponseID, chunk.ResponseID)
			}
			response.ResponseID = chunk.ResponseID
		}
		if chunk.ModelVersion != "" {
			if response.ModelVersion != "" && response.ModelVersion != chunk.ModelVersion {
				return nil, diagnostics, upstreamResponseError(ProtocolGenerateContent, "$.modelVersion", "model version changed from %q to %q during the stream", response.ModelVersion, chunk.ModelVersion)
			}
			response.ModelVersion = chunk.ModelVersion
		}
		if jsonValuePresent(envelope.Usage) {
			response.UsageMetadata = chunk.UsageMetadata
		}
		if jsonValuePresent(chunk.PromptFeedback) {
			response.PromptFeedback = append(json.RawMessage(nil), chunk.PromptFeedback...)
		}
		if jsonValuePresent(chunk.ModelStatus) {
			response.ModelStatus = append(json.RawMessage(nil), chunk.ModelStatus...)
		}
	}
	if !sawCandidate || !sawTerminal {
		return nil, diagnostics, invalid(ProtocolGenerateContent, "$", "Gemini stream ended before a terminal candidate")
	}
	body, err := marshal(ProtocolGenerateContent, response)
	return body, diagnostics, err
}

func collectChatStreamResponse(frames []streamFrame, policy lossPolicy) ([]byte, []Diagnostic, error) {
	type streamToolCall struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	type chunk struct {
		ID                string            `json:"id"`
		Model             string            `json:"model"`
		Created           int64             `json:"created"`
		Metadata          map[string]string `json:"metadata"`
		Moderation        json.RawMessage   `json:"moderation"`
		ServiceTier       json.RawMessage   `json:"service_tier"`
		SystemFingerprint string            `json:"system_fingerprint"`
		Obfuscation       string            `json:"obfuscation"`
		Choices           []struct {
			Index int `json:"index"`
			Delta struct {
				Role             string           `json:"role"`
				Content          string           `json:"content"`
				ReasoningContent string           `json:"reasoning_content"`
				Refusal          string           `json:"refusal"`
				Annotations      json.RawMessage  `json:"annotations"`
				ToolCalls        []streamToolCall `json:"tool_calls"`
				FunctionCall     json.RawMessage  `json:"function_call"`
			} `json:"delta"`
			FinishReason string          `json:"finish_reason"`
			Logprobs     json.RawMessage `json:"logprobs"`
		} `json:"choices"`
		Usage chatUsage  `json:"usage"`
		Error *chatError `json:"error"`
	}
	response := chatResponse{Object: "chat.completion"}
	message := chatMessage{Role: "assistant"}
	toolCalls := make(map[int]*chatToolCall)
	toolArguments := make(map[int]string)
	toolCallIDs := make(map[string]int)
	var annotations []json.RawMessage
	var contentLogprobs, refusalLogprobs []json.RawMessage
	sawLogprobs := false
	var diagnostics []Diagnostic
	var content string
	reportedObfuscation := false
	sawChunk, sawTerminal := false, false
	for _, frame := range frames {
		if frame.Done || len(frame.Data) == 0 || string(frame.Data) == "[DONE]" {
			continue
		}
		var event chunk
		if err := json.Unmarshal(frame.Data, &event); err != nil {
			return nil, nil, invalid(ProtocolChat, "$", "invalid stream chunk: %v", err)
		}
		if event.Error != nil {
			return nil, nil, upstreamResponseError(ProtocolChat, "$.error", "%s", event.Error.Message)
		}
		if sawTerminal && len(event.Choices) > 0 {
			return nil, nil, invalid(ProtocolChat, "$.choices", "choice content arrived after the terminal Chat chunk")
		}
		sawChunk = true
		if event.ID != "" {
			if response.ID != "" && response.ID != event.ID {
				return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.id", "response identity changed from %q to %q during the stream", response.ID, event.ID)
			}
			response.ID = event.ID
		}
		if event.Model != "" {
			if response.Model != "" && response.Model != event.Model {
				return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.model", "model changed from %q to %q during the stream", response.Model, event.Model)
			}
			response.Model = event.Model
		}
		if event.Created != 0 {
			if response.Created != 0 && response.Created != event.Created {
				return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.created", "creation time changed from %d to %d during the stream", response.Created, event.Created)
			}
			response.Created = event.Created
		}
		if len(event.Metadata) > 0 {
			response.Metadata = event.Metadata
		}
		if jsonValuePresent(event.Moderation) {
			response.Moderation = append(json.RawMessage(nil), event.Moderation...)
		}
		if jsonValuePresent(event.ServiceTier) {
			response.ServiceTier = append(json.RawMessage(nil), event.ServiceTier...)
		}
		if event.SystemFingerprint != "" {
			if response.SystemFingerprint != "" && response.SystemFingerprint != event.SystemFingerprint {
				return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.system_fingerprint", "system fingerprint changed during the stream")
			}
			response.SystemFingerprint = event.SystemFingerprint
		}
		if event.Obfuscation != "" && !reportedObfuscation {
			if policy == rejectSemanticLoss {
				return nil, diagnostics, unsupported(ProtocolChat, "$.obfuscation", "Chat stream obfuscation has no non-streaming response equivalent")
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "chat_stream_obfuscation_not_representable", "$.obfuscation", "Chat stream obfuscation was omitted while buffering the response")
			reportedObfuscation = true
		}
		if event.Usage != (chatUsage{}) {
			response.Usage = event.Usage
		}
		for _, choice := range event.Choices {
			if choice.Index != 0 {
				return nil, nil, unsupported(ProtocolChat, "$.choices", "cross-protocol conversion requires choice index 0")
			}
			if choice.Delta.Role != "" && choice.Delta.Role != "assistant" {
				return nil, nil, upstreamResponseError(ProtocolChat, "$.choices[0].delta.role", "unexpected role %q", choice.Delta.Role)
			}
			if raw := bytes.TrimSpace(choice.Delta.FunctionCall); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
				return nil, diagnostics, unsupported(ProtocolChat, "$.choices[0].delta.function_call", "deprecated function_call stream data cannot be preserved by cross-protocol conversion")
			}
			if jsonValuePresent(choice.Logprobs) {
				var value struct {
					Content []json.RawMessage `json:"content"`
					Refusal []json.RawMessage `json:"refusal"`
				}
				if err := json.Unmarshal(choice.Logprobs, &value); err != nil || !bytes.HasPrefix(bytes.TrimSpace(choice.Logprobs), []byte("{")) {
					return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.choices[0].logprobs", "logprobs must be an object")
				}
				sawLogprobs = true
				contentLogprobs = append(contentLogprobs, value.Content...)
				refusalLogprobs = append(refusalLogprobs, value.Refusal...)
			}
			content += choice.Delta.Content
			message.ReasoningContent += choice.Delta.ReasoningContent
			message.Refusal += choice.Delta.Refusal
			if jsonValuePresent(choice.Delta.Annotations) {
				var deltaAnnotations []json.RawMessage
				if err := json.Unmarshal(choice.Delta.Annotations, &deltaAnnotations); err != nil {
					return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.choices[0].delta.annotations", "must be an array")
				}
				annotations = append(annotations, deltaAnnotations...)
			}
			for _, call := range choice.Delta.ToolCalls {
				if call.Type != "" && call.Type != "function" {
					return nil, diagnostics, unsupported(ProtocolChat, "$.choices[0].delta.tool_calls[].type", "stream tool call type %q cannot be represented as a function call", call.Type)
				}
				current := toolCalls[call.Index]
				if current == nil {
					current = &chatToolCall{Type: "function"}
					toolCalls[call.Index] = current
				}
				if call.ID != "" {
					if current.ID != "" && current.ID != call.ID {
						return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.choices[0].delta.tool_calls[].id", "tool call id changed from %q to %q at index %d", current.ID, call.ID, call.Index)
					}
					if priorIndex, duplicate := toolCallIDs[call.ID]; duplicate && priorIndex != call.Index {
						return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.choices[0].delta.tool_calls[].id", "tool call id %q is reused at indexes %d and %d", call.ID, priorIndex, call.Index)
					}
					current.ID = call.ID
					toolCallIDs[call.ID] = call.Index
				}
				if call.Function.Name != "" {
					if current.Function.Name != "" && current.Function.Name != call.Function.Name {
						return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.choices[0].delta.tool_calls[].function.name", "tool call name changed from %q to %q at index %d", current.Function.Name, call.Function.Name, call.Index)
					}
					current.Function.Name = call.Function.Name
				}
				if len(bytes.TrimSpace(call.Function.Arguments)) > 0 {
					var arguments string
					if err := json.Unmarshal(call.Function.Arguments, &arguments); err != nil {
						return nil, diagnostics, upstreamResponseError(ProtocolChat, "$.choices[0].delta.tool_calls[].function.arguments", "arguments delta must be a string")
					}
					toolArguments[call.Index] += arguments
				}
			}
			if choice.FinishReason != "" {
				if sawTerminal {
					return nil, nil, invalid(ProtocolChat, "$.choices[].finish_reason", "duplicate terminal Chat chunk")
				}
				if _, err := parseChatFinish(choice.FinishReason); err != nil {
					return nil, nil, err
				}
				response.Choices = []chatResponseChoice{{Index: 0, FinishReason: choice.FinishReason}}
				sawTerminal = true
			}
		}
	}
	if !sawChunk || !sawTerminal {
		return nil, nil, invalid(ProtocolChat, "$", "Chat stream ended before a finish_reason")
	}
	message.Content = json.RawMessage(mustJSONString(content))
	if len(annotations) > 0 {
		message.Annotations = mustJSON(annotations)
	}
	indexes := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := toolCalls[index]
		if call.ID == "" || call.Function.Name == "" {
			return nil, diagnostics, upstreamResponseError(ProtocolChat, fmt.Sprintf("$.choices[].delta.tool_calls[%d]", index), "completed tool call requires id and function name")
		}
		arguments, err := normalizeArguments(ProtocolChat, fmt.Sprintf("$.choices[].delta.tool_calls[%d].function.arguments", index), json.RawMessage(toolArguments[index]))
		if err != nil {
			return nil, nil, err
		}
		call.Function.Arguments = json.RawMessage(mustJSONString(string(arguments)))
		message.ToolCalls = append(message.ToolCalls, *call)
	}
	response.Choices[0].Message = message
	if sawLogprobs {
		response.Choices[0].Logprobs = mustJSON(map[string]any{"content": contentLogprobs, "refusal": refusalLogprobs})
	}
	body, err := marshal(ProtocolChat, response)
	return body, diagnostics, err
}

func collectMessagesStreamResponse(frames []streamFrame) ([]byte, []Diagnostic, error) {
	type streamUsage struct {
		InputTokens              *int64                       `json:"input_tokens"`
		OutputTokens             *int64                       `json:"output_tokens"`
		CacheCreationInputTokens *int64                       `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     *int64                       `json:"cache_read_input_tokens"`
		CacheCreation            *messagesCacheCreation       `json:"cache_creation"`
		InferenceGeo             string                       `json:"inference_geo"`
		OutputTokensDetails      *messagesOutputTokensDetails `json:"output_tokens_details"`
		ServerToolUse            json.RawMessage              `json:"server_tool_use"`
		ServiceTier              string                       `json:"service_tier"`
	}
	type streamEvent struct {
		Type    string `json:"type"`
		Index   *int   `json:"index"`
		Message struct {
			ID           string          `json:"id"`
			Type         string          `json:"type"`
			Role         string          `json:"role"`
			Model        string          `json:"model"`
			Content      json.RawMessage `json:"content"`
			Container    json.RawMessage `json:"container"`
			StopReason   json.RawMessage `json:"stop_reason"`
			StopSequence json.RawMessage `json:"stop_sequence"`
			StopDetails  json.RawMessage `json:"stop_details"`
			Usage        streamUsage     `json:"usage"`
		} `json:"message"`
		ContentBlock messagesBlock `json:"content_block"`
		Delta        struct {
			Type         string          `json:"type"`
			Text         string          `json:"text"`
			Thinking     string          `json:"thinking"`
			Signature    string          `json:"signature"`
			PartialJSON  string          `json:"partial_json"`
			StopReason   json.RawMessage `json:"stop_reason"`
			StopSequence json.RawMessage `json:"stop_sequence"`
			StopDetails  json.RawMessage `json:"stop_details"`
			Container    json.RawMessage `json:"container"`
			Citation     json.RawMessage `json:"citation"`
		} `json:"delta"`
		Usage streamUsage `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	response := messagesResponse{Type: "message", Role: "assistant"}
	blocks := make(map[int]*messagesBlock)
	open := make(map[int]bool)
	arguments := make(map[int]string)
	sawStart, sawTerminal, sawStop := false, false, false
	update := func(current *int64, next *int64, path string) error {
		if next == nil {
			return nil
		}
		if *next < 0 || *next < *current {
			return upstreamResponseError(ProtocolMessages, path, "invalid cumulative token count %d", *next)
		}
		*current = *next
		return nil
	}
	applyUsage := func(usage streamUsage, path string) error {
		if err := update(&response.Usage.InputTokens, usage.InputTokens, path+".input_tokens"); err != nil {
			return err
		}
		if err := update(&response.Usage.OutputTokens, usage.OutputTokens, path+".output_tokens"); err != nil {
			return err
		}
		if err := update(&response.Usage.CacheCreationInputTokens, usage.CacheCreationInputTokens, path+".cache_creation_input_tokens"); err != nil {
			return err
		}
		if err := update(&response.Usage.CacheReadInputTokens, usage.CacheReadInputTokens, path+".cache_read_input_tokens"); err != nil {
			return err
		}
		if usage.CacheCreation != nil {
			if usage.CacheCreation.Ephemeral1hInputTokens < 0 || usage.CacheCreation.Ephemeral5mInputTokens < 0 {
				return upstreamResponseError(ProtocolMessages, path+".cache_creation", "token counts must not be negative")
			}
			if response.Usage.CacheCreation != nil && *response.Usage.CacheCreation != *usage.CacheCreation {
				return upstreamResponseError(ProtocolMessages, path+".cache_creation", "cache-creation breakdown changed during the stream")
			}
			copy := *usage.CacheCreation
			response.Usage.CacheCreation = &copy
		}
		if usage.InferenceGeo != "" {
			if response.Usage.InferenceGeo != "" && response.Usage.InferenceGeo != usage.InferenceGeo {
				return upstreamResponseError(ProtocolMessages, path+".inference_geo", "inference geography changed during the stream")
			}
			response.Usage.InferenceGeo = usage.InferenceGeo
		}
		if usage.OutputTokensDetails != nil {
			thinking := usage.OutputTokensDetails.ThinkingTokens
			if thinking < 0 || response.Usage.OutputTokensDetails != nil && thinking < response.Usage.OutputTokensDetails.ThinkingTokens {
				return upstreamResponseError(ProtocolMessages, path+".output_tokens_details.thinking_tokens", "invalid cumulative thinking token count %d", thinking)
			}
			copy := *usage.OutputTokensDetails
			response.Usage.OutputTokensDetails = &copy
		}
		if jsonValuePresent(usage.ServerToolUse) {
			var next map[string]int64
			if err := json.Unmarshal(usage.ServerToolUse, &next); err != nil || next == nil {
				return upstreamResponseError(ProtocolMessages, path+".server_tool_use", "must be an object")
			}
			current := make(map[string]int64)
			if jsonValuePresent(response.Usage.ServerToolUse) {
				if err := json.Unmarshal(response.Usage.ServerToolUse, &current); err != nil {
					return upstreamResponseError(ProtocolMessages, path+".server_tool_use", "invalid prior server-tool usage")
				}
			}
			for name, count := range next {
				if name != "web_search_requests" && name != "web_fetch_requests" {
					return upstreamResponseError(ProtocolMessages, path+".server_tool_use."+name, "unknown server-tool usage field")
				}
				if count < 0 || count < current[name] {
					return upstreamResponseError(ProtocolMessages, path+".server_tool_use."+name, "invalid cumulative request count %d", count)
				}
				current[name] = count
			}
			response.Usage.ServerToolUse = mustJSON(current)
		}
		if usage.ServiceTier != "" {
			if usage.ServiceTier != "standard" && usage.ServiceTier != "priority" && usage.ServiceTier != "batch" {
				return upstreamResponseError(ProtocolMessages, path+".service_tier", "unsupported service tier %q", usage.ServiceTier)
			}
			if response.Usage.ServiceTier != "" && response.Usage.ServiceTier != usage.ServiceTier {
				return upstreamResponseError(ProtocolMessages, path+".service_tier", "service tier changed during the stream")
			}
			response.Usage.ServiceTier = usage.ServiceTier
		}
		return nil
	}
	isNullish := func(raw json.RawMessage) bool {
		trimmed := bytes.TrimSpace(raw)
		return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
	}
	decodeRequiredString := func(raw json.RawMessage, path string) (string, error) {
		if isNullish(raw) {
			return "", upstreamResponseError(ProtocolMessages, path, "must be a string")
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", upstreamResponseError(ProtocolMessages, path, "must be a string")
		}
		return value, nil
	}
	validateNullableObject := func(raw json.RawMessage, path string) error {
		if isNullish(raw) {
			return nil
		}
		var value map[string]json.RawMessage
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			return upstreamResponseError(ProtocolMessages, path, "must be an object or null")
		}
		return nil
	}
	for _, frame := range frames {
		if frame.Done || len(frame.Data) == 0 {
			continue
		}
		var event streamEvent
		if err := json.Unmarshal(frame.Data, &event); err != nil {
			return nil, nil, invalid(ProtocolMessages, "$", "invalid stream event: %v", err)
		}
		if frame.Event != "" && event.Type != "" && frame.Event != event.Type {
			return nil, nil, invalid(ProtocolMessages, "$.type", "SSE event %q does not match payload type %q", frame.Event, event.Type)
		}
		if event.Type == "" {
			event.Type = frame.Event
		}
		if sawStop || (sawTerminal && event.Type != "message_stop" && event.Type != "ping") {
			return nil, nil, invalid(ProtocolMessages, "$.type", "event %q arrived after terminal state", event.Type)
		}
		switch event.Type {
		case "message_start":
			if sawStart || event.Message.ID == "" || event.Message.Model == "" {
				return nil, nil, upstreamResponseError(ProtocolMessages, "$.message", "invalid or duplicate message_start")
			}
			if event.Message.Type != "" && event.Message.Type != "message" || event.Message.Role != "" && event.Message.Role != "assistant" {
				return nil, nil, upstreamResponseError(ProtocolMessages, "$.message", "unexpected message type or role")
			}
			var initialContent []json.RawMessage
			if len(bytes.TrimSpace(event.Message.Content)) > 0 && (json.Unmarshal(event.Message.Content, &initialContent) != nil || len(initialContent) != 0) {
				return nil, nil, invalid(ProtocolMessages, "$.message.content", "message_start content must be an empty array")
			}
			if !isNullish(event.Message.StopReason) || !isNullish(event.Message.StopSequence) || !isNullish(event.Message.StopDetails) {
				return nil, nil, upstreamResponseError(ProtocolMessages, "$.message", "message_start stop fields must be null")
			}
			if err := validateNullableObject(event.Message.Container, "$.message.container"); err != nil {
				return nil, nil, err
			}
			sawStart = true
			response.ID, response.Model = event.Message.ID, event.Message.Model
			if len(bytes.TrimSpace(event.Message.Container)) > 0 {
				response.Container = append(json.RawMessage(nil), event.Message.Container...)
			}
			if err := applyUsage(event.Message.Usage, "$.message.usage"); err != nil {
				return nil, nil, err
			}
		case "content_block_start":
			if !sawStart || sawTerminal || event.Index == nil || *event.Index < 0 || blocks[*event.Index] != nil {
				return nil, nil, invalid(ProtocolMessages, "$.index", "invalid or duplicate content block start")
			}
			index := *event.Index
			block := event.ContentBlock
			if block.Type == "" {
				return nil, nil, invalid(ProtocolMessages, "$.content_block.type", "content block type is required")
			}
			if block.Type == "tool_use" || block.Type == "server_tool_use" || block.Type == "mcp_tool_use" {
				var input map[string]json.RawMessage
				if json.Unmarshal(block.Input, &input) != nil || input == nil {
					return nil, nil, upstreamResponseError(ProtocolMessages, "$.content_block.input", "tool input must start as an empty object")
				}
				if len(input) != 0 {
					return nil, nil, upstreamResponseError(ProtocolMessages, "$.content_block.input", "streamed tool input must start as an empty object")
				}
			}
			blocks[index], open[index] = &block, true
		case "content_block_delta":
			if event.Index == nil {
				return nil, nil, invalid(ProtocolMessages, "$.index", "content block index is required")
			}
			index := *event.Index
			block := blocks[index]
			if block == nil || !open[index] {
				return nil, nil, invalid(ProtocolMessages, "$.index", "delta before content block start")
			}
			switch event.Delta.Type {
			case "text_delta":
				if block.Type != "text" {
					return nil, nil, invalid(ProtocolMessages, "$.delta.type", "text_delta does not match block type")
				}
				block.Text += event.Delta.Text
			case "thinking_delta":
				if block.Type != "thinking" {
					return nil, nil, invalid(ProtocolMessages, "$.delta.type", "thinking_delta does not match block type")
				}
				block.Thinking += event.Delta.Thinking
			case "signature_delta":
				if block.Type != "thinking" {
					return nil, nil, invalid(ProtocolMessages, "$.delta.type", "signature_delta does not match block type")
				}
				block.Signature += event.Delta.Signature
			case "input_json_delta":
				if block.Type != "tool_use" && block.Type != "server_tool_use" && block.Type != "mcp_tool_use" {
					return nil, nil, invalid(ProtocolMessages, "$.delta.type", "input_json_delta does not match block type")
				}
				arguments[index] += event.Delta.PartialJSON
			case "citations_delta":
				if block.Type != "text" {
					return nil, nil, invalid(ProtocolMessages, "$.delta.type", "citations_delta does not match block type")
				}
				citation := bytes.TrimSpace(event.Delta.Citation)
				if !jsonValuePresent(citation) || len(citation) == 0 || citation[0] != '{' {
					return nil, nil, upstreamResponseError(ProtocolMessages, "$.delta.citation", "citation must be an object")
				}
				var citations []json.RawMessage
				if jsonValuePresent(block.Citations) && json.Unmarshal(block.Citations, &citations) != nil {
					return nil, nil, upstreamResponseError(ProtocolMessages, "$.content_block.citations", "citations must be an array")
				}
				citations = append(citations, append(json.RawMessage(nil), event.Delta.Citation...))
				block.Citations = mustJSON(citations)
			default:
				return nil, nil, unsupported(ProtocolMessages, "$.delta.type", "stream delta %q is not supported", event.Delta.Type)
			}
		case "content_block_stop":
			if event.Index == nil {
				return nil, nil, invalid(ProtocolMessages, "$.index", "content block index is required")
			}
			index := *event.Index
			if !open[index] {
				return nil, nil, invalid(ProtocolMessages, "$.index", "content block stop before start")
			}
			delete(open, index)
		case "message_delta":
			if !sawStart || sawTerminal || len(open) != 0 {
				return nil, nil, invalid(ProtocolMessages, "$.type", "invalid terminal message_delta")
			}
			stopReason, err := decodeRequiredString(event.Delta.StopReason, "$.delta.stop_reason")
			if err != nil {
				return nil, nil, err
			}
			if _, err := parseMessagesFinish(stopReason); err != nil {
				return nil, nil, err
			}
			stopSequence := ""
			if !isNullish(event.Delta.StopSequence) {
				stopSequence, err = decodeRequiredString(event.Delta.StopSequence, "$.delta.stop_sequence")
				if err != nil {
					return nil, nil, err
				}
			}
			if stopReason == "stop_sequence" && stopSequence == "" {
				return nil, nil, upstreamResponseError(ProtocolMessages, "$.delta.stop_sequence", "stop_sequence is required")
			}
			if err := validateNullableObject(event.Delta.Container, "$.delta.container"); err != nil {
				return nil, nil, err
			}
			if err := validateNullableObject(event.Delta.StopDetails, "$.delta.stop_details"); err != nil {
				return nil, nil, err
			}
			response.StopReason, response.StopSequence = stopReason, stopSequence
			if len(bytes.TrimSpace(event.Delta.Container)) > 0 {
				response.Container = append(json.RawMessage(nil), event.Delta.Container...)
			}
			if len(bytes.TrimSpace(event.Delta.StopDetails)) > 0 {
				response.StopDetails = append(json.RawMessage(nil), event.Delta.StopDetails...)
			}
			sawTerminal = true
			if err := applyUsage(event.Usage, "$.usage"); err != nil {
				return nil, nil, err
			}
		case "message_stop":
			if !sawStart || !sawTerminal || len(open) != 0 {
				return nil, nil, invalid(ProtocolMessages, "$.type", "message_stop arrived before the stream was complete")
			}
			sawStop = true
		case "ping":
		case "error":
			message := event.Error.Message
			if message == "" {
				message = "Messages stream returned an error event"
			}
			return nil, nil, upstreamResponseError(ProtocolMessages, "$.error", "%s", message)
		default:
			return nil, nil, unsupported(ProtocolMessages, "$.type", "stream event %q is not supported", event.Type)
		}
	}
	if !sawStart || !sawTerminal || !sawStop {
		return nil, nil, invalid(ProtocolMessages, "$", "stream ended before message_stop")
	}
	ordered := make([]messagesBlock, 0, len(blocks))
	for index := 0; index < len(blocks); index++ {
		block := blocks[index]
		if block == nil {
			return nil, nil, invalid(ProtocolMessages, "$.index", "missing content block index %d", index)
		}
		if block.Type == "tool_use" || block.Type == "server_tool_use" || block.Type == "mcp_tool_use" {
			raw := json.RawMessage(arguments[index])
			if len(raw) == 0 {
				raw = block.Input
			}
			normalized, err := normalizeArguments(ProtocolMessages, "$.content_block.input", raw)
			if err != nil {
				return nil, nil, err
			}
			block.Input = normalized
		}
		ordered = append(ordered, *block)
	}
	response.Content = mustJSON(ordered)
	if err := validateMessagesResponse(response); err != nil {
		return nil, nil, err
	}
	body, err := marshal(ProtocolMessages, response)
	return body, nil, err
}
