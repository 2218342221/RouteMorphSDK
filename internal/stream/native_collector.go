package stream

import (
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
		return collectChatStreamResponse(frames)
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
	for index := len(frames) - 1; index >= 0; index-- {
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if json.Unmarshal(frames[index].Data, &event) != nil || event.Type != "response.completed" || len(event.Response) == 0 {
			continue
		}
		var response responsesResponse
		if err := decodeJSON(ProtocolResponses, event.Response, &response); err != nil {
			return nil, nil, err
		}
		if err := validateResponsesTerminal(response); err != nil {
			return nil, nil, err
		}
		return append([]byte(nil), event.Response...), nil, nil
	}
	return nil, nil, invalid(ProtocolResponses, "$", "response.completed event is missing")
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
				&candidate.URLContextMetadata: &target.URLContextMetadata,
			} {
				if jsonValuePresent(*source) {
					*destination = append(json.RawMessage(nil), (*source)...)
				}
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
			response.ResponseID = chunk.ResponseID
		}
		if chunk.ModelVersion != "" {
			response.ModelVersion = chunk.ModelVersion
		}
		if jsonValuePresent(envelope.Usage) {
			response.UsageMetadata = chunk.UsageMetadata
		}
		if jsonValuePresent(chunk.PromptFeedback) {
			response.PromptFeedback = append(json.RawMessage(nil), chunk.PromptFeedback...)
		}
	}
	if !sawCandidate || !sawTerminal {
		return nil, diagnostics, invalid(ProtocolGenerateContent, "$", "Gemini stream ended before a terminal candidate")
	}
	body, err := marshal(ProtocolGenerateContent, response)
	return body, diagnostics, err
}

func collectChatStreamResponse(frames []streamFrame) ([]byte, []Diagnostic, error) {
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
		ID      string `json:"id"`
		Model   string `json:"model"`
		Created int64  `json:"created"`
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Role             string           `json:"role"`
				Content          string           `json:"content"`
				ReasoningContent string           `json:"reasoning_content"`
				Refusal          string           `json:"refusal"`
				ToolCalls        []streamToolCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage chatUsage  `json:"usage"`
		Error *chatError `json:"error"`
	}
	response := chatResponse{Object: "chat.completion"}
	message := chatMessage{Role: "assistant"}
	toolCalls := make(map[int]*chatToolCall)
	toolArguments := make(map[int]string)
	var content string
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
			response.ID = event.ID
		}
		if event.Model != "" {
			response.Model = event.Model
		}
		if event.Created != 0 {
			response.Created = event.Created
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
			content += choice.Delta.Content
			message.ReasoningContent += choice.Delta.ReasoningContent
			message.Refusal += choice.Delta.Refusal
			for _, call := range choice.Delta.ToolCalls {
				current := toolCalls[call.Index]
				if current == nil {
					current = &chatToolCall{Type: "function"}
					toolCalls[call.Index] = current
				}
				if call.ID != "" {
					current.ID = call.ID
				}
				if call.Function.Name != "" {
					current.Function.Name = call.Function.Name
				}
				toolArguments[call.Index] += rawString(call.Function.Arguments)
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
	indexes := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := toolCalls[index]
		arguments, err := normalizeArguments(ProtocolChat, fmt.Sprintf("$.choices[].delta.tool_calls[%d].function.arguments", index), json.RawMessage(toolArguments[index]))
		if err != nil {
			return nil, nil, err
		}
		call.Function.Arguments = json.RawMessage(mustJSONString(string(arguments)))
		message.ToolCalls = append(message.ToolCalls, *call)
	}
	response.Choices[0].Message = message
	body, err := marshal(ProtocolChat, response)
	return body, nil, err
}

func collectMessagesStreamResponse(frames []streamFrame) ([]byte, []Diagnostic, error) {
	type streamUsage struct {
		InputTokens              *int64          `json:"input_tokens"`
		OutputTokens             *int64          `json:"output_tokens"`
		CacheCreationInputTokens *int64          `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     *int64          `json:"cache_read_input_tokens"`
		ServerToolUse            json.RawMessage `json:"server_tool_use"`
	}
	type streamEvent struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message struct {
			ID    string      `json:"id"`
			Type  string      `json:"type"`
			Role  string      `json:"role"`
			Model string      `json:"model"`
			Usage streamUsage `json:"usage"`
		} `json:"message"`
		ContentBlock messagesBlock `json:"content_block"`
		Delta        struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			Thinking     string `json:"thinking"`
			Signature    string `json:"signature"`
			PartialJSON  string `json:"partial_json"`
			StopReason   string `json:"stop_reason"`
			StopSequence string `json:"stop_sequence"`
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
		if jsonValuePresent(usage.ServerToolUse) {
			response.Usage.ServerToolUse = append(json.RawMessage(nil), usage.ServerToolUse...)
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
			sawStart = true
			response.ID, response.Model = event.Message.ID, event.Message.Model
			if err := applyUsage(event.Message.Usage, "$.message.usage"); err != nil {
				return nil, nil, err
			}
		case "content_block_start":
			if !sawStart || sawTerminal || event.Index < 0 || blocks[event.Index] != nil {
				return nil, nil, invalid(ProtocolMessages, "$.index", "invalid or duplicate content block start")
			}
			block := event.ContentBlock
			blocks[event.Index], open[event.Index] = &block, true
		case "content_block_delta":
			block := blocks[event.Index]
			if block == nil || !open[event.Index] {
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
				if block.Type != "tool_use" {
					return nil, nil, invalid(ProtocolMessages, "$.delta.type", "input_json_delta does not match block type")
				}
				arguments[event.Index] += event.Delta.PartialJSON
			default:
				return nil, nil, unsupported(ProtocolMessages, "$.delta.type", "stream delta %q is not supported", event.Delta.Type)
			}
		case "content_block_stop":
			if !open[event.Index] {
				return nil, nil, invalid(ProtocolMessages, "$.index", "content block stop before start")
			}
			delete(open, event.Index)
		case "message_delta":
			if !sawStart || sawTerminal || len(open) != 0 || event.Delta.StopReason == "" {
				return nil, nil, invalid(ProtocolMessages, "$.type", "invalid terminal message_delta")
			}
			if _, err := parseMessagesFinish(event.Delta.StopReason); err != nil {
				return nil, nil, err
			}
			if event.Delta.StopReason == "stop_sequence" && event.Delta.StopSequence == "" {
				return nil, nil, upstreamResponseError(ProtocolMessages, "$.delta.stop_sequence", "stop_sequence is required")
			}
			response.StopReason, response.StopSequence = event.Delta.StopReason, event.Delta.StopSequence
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
		if block.Type == "tool_use" {
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
	body, err := marshal(ProtocolMessages, response)
	return body, nil, err
}
