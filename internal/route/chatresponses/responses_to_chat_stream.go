package chatresponses

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type responsesToChatStreamConverter struct {
	id             string
	model          string
	providerID     string
	providerModel  string
	created        int64
	toolIndexes    map[string]int
	toolArguments  map[int]string
	nextTool       int
	completed      bool
	finalized      bool
	clientModel    string
	includeUsage   bool
	started        bool
	text           string
	refusal        string
	reasoning      string
	logprobs       []json.RawMessage
	items          map[string]responsesItem
	completedItems map[string]bool
}

func (c *responsesToChatStreamConverter) Convert(_ context.Context, frame streamFrame) ([]streamFrame, []Diagnostic, error) {
	if c.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", ErrInvalidPlan)
	}
	if c.completed {
		if frame.Done || string(frame.Data) == "[DONE]" {
			return nil, nil, nil
		}
		return nil, nil, invalid(ProtocolResponses, "$", "stream event arrived after terminal response")
	}
	if frame.Done || string(frame.Data) == "[DONE]" {
		return nil, nil, nil
	}
	var event struct {
		Type         string          `json:"type"`
		Delta        string          `json:"delta"`
		Logprobs     json.RawMessage `json:"logprobs"`
		ItemID       string          `json:"item_id"`
		OutputIndex  int             `json:"output_index"`
		ContentIndex int             `json:"content_index"`
		Item         responsesItem   `json:"item"`
		Part         json.RawMessage `json:"part"`
		Arguments    string          `json:"arguments"`
		Response     json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(frame.Data, &event); err != nil {
		return nil, nil, invalid(ProtocolResponses, "$", "invalid stream event: %v", err)
	}
	if event.Type == "" {
		return nil, nil, invalid(ProtocolResponses, "$.type", "stream event type is required")
	}
	if frame.Event != "" && frame.Event != event.Type {
		return nil, nil, invalid(ProtocolResponses, "$.type", "SSE event %q does not match payload type %q", frame.Event, event.Type)
	}
	switch event.Type {
	case "response.created":
		var response responsesResponse
		if err := json.Unmarshal(event.Response, &response); err != nil {
			return nil, nil, invalid(ProtocolResponses, "$.response", "invalid response.created object")
		}
		if err := c.setBase(response, "$.response"); err != nil {
			return nil, nil, err
		}
		c.started = true
		return []streamFrame{{Data: c.chatChunk(map[string]any{"role": "assistant"}, nil, nil)}}, nil, nil
	case "response.output_text.delta":
		if err := c.validateKnownItem(event.ItemID, "message"); err != nil {
			return nil, nil, err
		}
		logprobs, err := decodeLogprobArray(ProtocolResponses, "$.logprobs", event.Logprobs)
		if err != nil {
			return nil, nil, err
		}
		c.text += event.Delta
		c.logprobs = append(c.logprobs, logprobs...)
		return c.withStart([]streamFrame{{Data: c.chatChunkWithLogprobs(map[string]any{"content": event.Delta}, nil, nil, logprobs)}}), nil, nil
	case "response.refusal.delta":
		if err := c.validateKnownItem(event.ItemID, "message"); err != nil {
			return nil, nil, err
		}
		c.refusal += event.Delta
		return c.withStart([]streamFrame{{Data: c.chatChunk(map[string]any{"refusal": event.Delta}, nil, nil)}}), nil, nil
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if err := c.validateKnownItem(event.ItemID, "reasoning"); err != nil {
			return nil, nil, err
		}
		c.reasoning += event.Delta
		return c.withStart([]streamFrame{{Data: c.chatChunk(map[string]any{"reasoning_content": event.Delta}, nil, nil)}}), nil, nil
	case "response.output_item.added":
		if event.Item.ID == "" {
			return nil, nil, invalid(ProtocolResponses, "$.item.id", "output item id is required")
		}
		if _, exists := c.items[event.Item.ID]; exists {
			return nil, nil, invalid(ProtocolResponses, "$.item.id", "duplicate output item id %q", event.Item.ID)
		}
		if err := validateResponsesItems([]responsesItem{event.Item}, "$.output"); err != nil {
			return nil, nil, err
		}
		c.items[event.Item.ID] = event.Item
		var phaseDiagnostics []Diagnostic
		if event.Item.Phase != "" {
			phaseDiagnostics = responsesPhaseDiagnostics([]responsesItem{event.Item}, "$.output")
		}
		if event.Item.Type == "reasoning" || event.Item.Type == "message" {
			return nil, phaseDiagnostics, nil
		}
		if event.Item.CallID == "" || event.Item.Name == "" {
			return nil, nil, upstreamResponseError(ProtocolResponses, "$.item", "function_call item is missing call_id or name")
		}
		index := c.nextTool
		c.nextTool++
		c.toolIndexes[event.Item.ID] = index
		if event.Item.CallID != "" {
			c.toolIndexes[event.Item.CallID] = index
		}
		delta := map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": event.Item.CallID, "type": "function", "function": map[string]any{"name": event.Item.Name, "arguments": ""}}}}
		frames := c.withStart([]streamFrame{{Data: c.chatChunk(delta, nil, nil)}})
		if jsonValuePresent(event.Item.Arguments) {
			arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, "$.item.arguments", event.Item.Arguments)
			if err != nil {
				return nil, nil, err
			}
			text := string(arguments)
			c.toolArguments[index] = text
			frames = append(frames, streamFrame{Data: c.chatChunk(map[string]any{"tool_calls": []any{map[string]any{"index": index, "function": map[string]any{"arguments": text}}}}, nil, nil)})
		}
		return frames, phaseDiagnostics, nil
	case "response.function_call_arguments.delta":
		index, ok := c.toolIndexes[event.ItemID]
		if !ok {
			return nil, nil, upstreamResponseError(ProtocolResponses, "$.item_id", "function arguments arrived before their function_call item")
		}
		delta := map[string]any{"tool_calls": []any{map[string]any{"index": index, "function": map[string]any{"arguments": event.Delta}}}}
		c.toolArguments[index] += event.Delta
		return c.withStart([]streamFrame{{Data: c.chatChunk(delta, nil, nil)}}), nil, nil
	case "response.content_part.added", "response.content_part.done":
		if err := c.validateContentPartEvent(event.ItemID, event.Part); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	case "response.output_item.done":
		if err := c.validateOutputItemDone(event.ItemID, event.Item); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	case "response.function_call_arguments.done":
		item, ok := c.items[event.ItemID]
		if !ok || item.Type != "function_call" {
			return nil, nil, invalid(ProtocolResponses, "$.item_id", "function arguments completed for an unknown function_call item")
		}
		index, ok := c.toolIndexes[event.ItemID]
		if !ok {
			return nil, nil, invalid(ProtocolResponses, "$.item_id", "function arguments completed before their function_call item")
		}
		if _, err := remainingStreamText(c.toolArguments[index], event.Arguments, "$.arguments"); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	case "response.output_text.done", "response.refusal.done":
		if err := c.validateKnownItem(event.ItemID, "message"); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done", "response.reasoning_summary_text.done", "response.reasoning_text.done":
		if err := c.validateKnownItem(event.ItemID, "reasoning"); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	case "response.queued", "response.in_progress":
		return nil, nil, nil
	case "response.completed", "response.incomplete", "response.failed":
		var response responsesResponse
		if err := json.Unmarshal(event.Response, &response); err != nil {
			return nil, nil, invalid(ProtocolResponses, "$.response", "invalid terminal response object")
		}
		if err := validateResponsesTerminal(response); err != nil {
			return nil, nil, err
		}
		if (event.Type == "response.completed") != (response.Status == "completed") || (event.Type == "response.incomplete") != (response.Status == "incomplete") {
			return nil, nil, invalid(ProtocolResponses, "$.response.status", "terminal event %q does not match status %q", event.Type, response.Status)
		}
		if err := c.setBase(response, "$.response"); err != nil {
			return nil, nil, err
		}
		fallback, err := c.terminalOutputChunks(response)
		if err != nil {
			return nil, nil, err
		}
		finish := finishStop
		for _, item := range response.Output {
			if item.Type == "function_call" {
				finish = finishToolCalls
			}
		}
		if response.Status == "incomplete" {
			finish = finishLength
			if response.IncompleteDetails != nil && response.IncompleteDetails.Reason == "content_filter" {
				finish = finishContentFilter
			}
		}
		usage := map[string]any{
			"prompt_tokens": response.Usage.InputTokens, "completion_tokens": response.Usage.OutputTokens, "total_tokens": response.Usage.TotalTokens,
			"prompt_tokens_details":     map[string]any{"cached_tokens": response.Usage.InputTokenDetails.CachedTokens},
			"completion_tokens_details": map[string]any{"reasoning_tokens": response.Usage.OutputTokenDetails.ReasoningTokens},
		}
		c.completed = true
		fallback = append(fallback, streamFrame{Data: c.chatChunk(map[string]any{}, string(finish), nil)})
		if c.includeUsage {
			fallback = append(fallback, streamFrame{Data: c.chatUsageChunk(usage)})
		}
		fallback = append(fallback, streamFrame{Data: []byte("[DONE]"), Done: true})
		return fallback, nil, nil
	case "error":
		var streamError struct {
			Message string          `json:"message"`
			Error   *responsesError `json:"error"`
		}
		_ = json.Unmarshal(frame.Data, &streamError)
		message := streamError.Message
		if streamError.Error != nil && streamError.Error.Message != "" {
			message = streamError.Error.Message
		}
		if message == "" {
			message = "Responses stream failed"
		}
		return nil, nil, upstreamResponseError(ProtocolResponses, "$.error", "%s", message)
	default:
		return nil, nil, unsupported(ProtocolResponses, "$.type", "stream event %q is not supported by the Chat conversion", event.Type)
	}
}

func (c *responsesToChatStreamConverter) validateKnownItem(itemID, wantType string) error {
	if itemID == "" {
		return invalid(ProtocolResponses, "$.item_id", "item id is required")
	}
	item, ok := c.items[itemID]
	if !ok {
		return invalid(ProtocolResponses, "$.item_id", "event refers to unknown output item %q", itemID)
	}
	if item.Type != wantType {
		return invalid(ProtocolResponses, "$.item_id", "event for %s item refers to %s item %q", wantType, item.Type, itemID)
	}
	return nil
}

func (c *responsesToChatStreamConverter) validateContentPartEvent(itemID string, raw json.RawMessage) error {
	if err := c.validateKnownItem(itemID, "message"); err != nil {
		return err
	}
	var part responsesContentPart
	if len(raw) == 0 || json.Unmarshal(raw, &part) != nil {
		return invalid(ProtocolResponses, "$.part", "valid content part is required")
	}
	if part.Type != "output_text" && part.Type != "refusal" {
		return unsupported(ProtocolResponses, "$.part.type", "content part %q cannot be represented by Chat", part.Type)
	}
	if len(part.Annotations) > 0 && string(part.Annotations) != "null" && string(part.Annotations) != "[]" {
		return unsupported(ProtocolResponses, "$.part.annotations", "output annotations cannot be represented by Chat")
	}
	if len(part.PromptCacheBreakpoint) > 0 && string(part.PromptCacheBreakpoint) != "null" {
		return unsupported(ProtocolResponses, "$.part.prompt_cache_breakpoint", "prompt cache breakpoints have no portable cross-protocol equivalent")
	}
	return nil
}

func (c *responsesToChatStreamConverter) validateOutputItemDone(itemID string, item responsesItem) error {
	if item.ID == "" {
		return invalid(ProtocolResponses, "$.item.id", "completed output item id is required")
	}
	if itemID != "" && itemID != item.ID {
		return invalid(ProtocolResponses, "$.item_id", "event item_id %q does not match item id %q", itemID, item.ID)
	}
	if err := validateResponsesItems([]responsesItem{item}, "$.output"); err != nil {
		return err
	}
	if item.Type == "function_call" {
		if !jsonValuePresent(item.Arguments) {
			return invalid(ProtocolResponses, "$.item.arguments", "completed function_call arguments are required")
		}
		arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, "$.item.arguments", item.Arguments)
		if err != nil {
			return err
		}
		if index, ok := c.toolIndexes[item.ID]; ok {
			if _, err := remainingStreamText(c.toolArguments[index], string(arguments), "$.item.arguments"); err != nil {
				return err
			}
		}
	}
	if c.completedItems[item.ID] {
		return invalid(ProtocolResponses, "$.item.id", "duplicate output item completion for %q", item.ID)
	}
	if added, ok := c.items[item.ID]; ok {
		if added.Type != item.Type || added.Role != item.Role || added.CallID != item.CallID || added.Name != item.Name {
			return invalid(ProtocolResponses, "$.item", "completed output item identity does not match output_item.added")
		}
	} else {
		c.items[item.ID] = item
	}
	c.completedItems[item.ID] = true
	return nil
}

func (c *responsesToChatStreamConverter) terminalOutputChunks(response responsesResponse) ([]streamFrame, error) {
	var frames []streamFrame
	remainingText, remainingRefusal, remainingReasoning := c.text, c.refusal, c.reasoning
	var terminalLogprobs []json.RawMessage
	terminalItems := make(map[string]responsesItem, len(response.Output))
	emit := func(field, complete, path string, remaining *string, total *string) error {
		suffix, rest, err := consumeStreamPrefix(*remaining, complete, path)
		if err != nil {
			return err
		}
		*remaining = rest
		if suffix != "" {
			frames = append(frames, c.withStart([]streamFrame{{Data: c.chatChunk(map[string]any{field: suffix}, nil, nil)}})...)
			*total += suffix
		}
		return nil
	}
	for outputIndex, item := range response.Output {
		path := fmt.Sprintf("$.response.output[%d]", outputIndex)
		if item.ID != "" {
			if _, duplicate := terminalItems[item.ID]; duplicate {
				return nil, upstreamResponseError(ProtocolResponses, path+".id", "duplicate terminal output item id %q", item.ID)
			}
			terminalItems[item.ID] = item
		}
		if added, ok := c.items[item.ID]; ok {
			if added.Type != item.Type || added.Role != item.Role || added.CallID != item.CallID || added.Name != item.Name {
				return nil, upstreamResponseError(ProtocolResponses, path, "terminal output item identity does not match output_item.added")
			}
		}
		switch item.Type {
		case "message":
			parts, logprobs, err := responsesContentAndLogprobs(item.Content, path+".content")
			if err != nil {
				return nil, err
			}
			terminalLogprobs = append(terminalLogprobs, logprobs...)
			for _, part := range parts {
				if part.Kind == partRefusal {
					if err := emit("refusal", part.Text, path+".content", &remainingRefusal, &c.refusal); err != nil {
						return nil, err
					}
				} else {
					if err := emit("content", part.Text, path+".content", &remainingText, &c.text); err != nil {
						return nil, err
					}
				}
			}
		case "reasoning":
			parts, err := decodeResponsesContentRaw(item.Summary, path+".summary", false)
			if err != nil {
				return nil, err
			}
			if err := emit("reasoning_content", joinText(parts), path+".summary", &remainingReasoning, &c.reasoning); err != nil {
				return nil, err
			}
			parts, err = decodeResponsesContentRaw(item.Content, path+".content", false)
			if err != nil {
				return nil, err
			}
			if err := emit("reasoning_content", joinText(parts), path+".content", &remainingReasoning, &c.reasoning); err != nil {
				return nil, err
			}
		case "function_call":
			key := item.ID
			index, ok := c.toolIndexes[key]
			if !ok {
				index, ok = c.toolIndexes[item.CallID]
			}
			if !ok {
				if item.CallID == "" || item.Name == "" {
					return nil, upstreamResponseError(ProtocolResponses, path, "function_call item is missing call_id or name")
				}
				index = c.nextTool
				c.nextTool++
				c.toolIndexes[item.ID], c.toolIndexes[item.CallID] = index, index
				delta := map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": item.CallID, "type": "function", "function": map[string]any{"name": item.Name, "arguments": ""}}}}
				frames = append(frames, c.withStart([]streamFrame{{Data: c.chatChunk(delta, nil, nil)}})...)
			}
			arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, path+".arguments", item.Arguments)
			if err != nil {
				return nil, err
			}
			suffix, err := remainingStreamText(c.toolArguments[index], string(arguments), path+".arguments")
			if err != nil {
				return nil, err
			}
			if suffix != "" {
				frames = append(frames, streamFrame{Data: c.chatChunk(map[string]any{"tool_calls": []any{map[string]any{"index": index, "function": map[string]any{"arguments": suffix}}}}, nil, nil)})
				c.toolArguments[index] += suffix
			}
		default:
			return nil, unsupported(ProtocolResponses, path+".type", "output item %q cannot be represented by Chat", item.Type)
		}
	}
	for itemID, streamed := range c.items {
		terminal, exists := terminalItems[itemID]
		if !exists {
			return nil, upstreamResponseError(ProtocolResponses, "$.response.output", "terminal output is missing streamed item %q", itemID)
		}
		if terminal.Type != streamed.Type {
			return nil, upstreamResponseError(ProtocolResponses, "$.response.output", "terminal item %q changed type from %q to %q", itemID, streamed.Type, terminal.Type)
		}
	}
	if remainingText != "" || remainingRefusal != "" || remainingReasoning != "" {
		return nil, upstreamResponseError(ProtocolResponses, "$.response.output", "terminal output is missing content previously emitted as deltas")
	}
	logprobSuffix, err := logprobSuffix(c.logprobs, terminalLogprobs, "$.response.output.logprobs")
	if err != nil {
		return nil, err
	}
	if len(logprobSuffix) > 0 {
		frames = append(frames, c.withStart([]streamFrame{{Data: c.chatChunkWithLogprobs(map[string]any{}, nil, nil, logprobSuffix)}})...)
		c.logprobs = append(c.logprobs, logprobSuffix...)
	}
	return frames, nil
}

func consumeStreamPrefix(emitted, complete, path string) (suffix, remaining string, err error) {
	if emitted == "" {
		return complete, "", nil
	}
	if strings.HasPrefix(emitted, complete) {
		return "", strings.TrimPrefix(emitted, complete), nil
	}
	if strings.HasPrefix(complete, emitted) {
		return strings.TrimPrefix(complete, emitted), "", nil
	}
	return "", emitted, upstreamResponseError(ProtocolResponses, path, "terminal output does not match streamed deltas")
}

func remainingStreamText(emitted, complete, path string) (string, error) {
	if emitted == "" {
		return complete, nil
	}
	if !strings.HasPrefix(complete, emitted) {
		return "", upstreamResponseError(ProtocolResponses, path, "terminal output does not match streamed deltas")
	}
	return strings.TrimPrefix(complete, emitted), nil
}

func (c *responsesToChatStreamConverter) withStart(frames []streamFrame) []streamFrame {
	if c.started {
		return frames
	}
	c.started = true
	return append([]streamFrame{{Data: c.chatChunk(map[string]any{"role": "assistant"}, nil, nil)}}, frames...)
}

func (c *responsesToChatStreamConverter) Finalize(context.Context) ([]streamFrame, []Diagnostic, error) {
	if c.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", ErrInvalidPlan)
	}
	c.finalized = true
	if !c.completed {
		return nil, nil, invalid(ProtocolResponses, "$", "stream ended before a terminal response event")
	}
	return nil, nil, nil
}

func (c *responsesToChatStreamConverter) setBase(response responsesResponse, path string) error {
	if response.ID != "" {
		if c.providerID != "" && c.providerID != response.ID {
			return invalid(ProtocolResponses, path+".id", "response id changed from %q to %q", c.providerID, response.ID)
		}
		c.providerID = response.ID
		c.id = c.providerID
	}
	if response.Model != "" {
		if c.providerModel != "" && c.providerModel != response.Model {
			return invalid(ProtocolResponses, path+".model", "response model changed from %q to %q", c.providerModel, response.Model)
		}
		c.providerModel = response.Model
	}
	if response.CreatedAt != 0 {
		c.created = response.CreatedAt
	}
	if c.clientModel != "" {
		c.model = c.clientModel
	} else if c.providerModel != "" {
		c.model = c.providerModel
	}
	return nil
}

func (c *responsesToChatStreamConverter) chatChunk(delta map[string]any, finish any, usage any) []byte {
	return c.chatChunkWithLogprobs(delta, finish, usage, nil)
}

func (c *responsesToChatStreamConverter) chatChunkWithLogprobs(delta map[string]any, finish any, usage any, logprobs []json.RawMessage) []byte {
	choice := map[string]any{"index": 0, "delta": delta, "finish_reason": finish}
	if len(logprobs) > 0 {
		choice["logprobs"] = map[string]any{"content": logprobs, "refusal": nil}
	}
	chunk := map[string]any{
		"id": c.id, "object": "chat.completion.chunk", "created": c.created, "model": c.model,
		"choices": []any{choice},
	}
	if usage != nil {
		chunk["usage"] = usage
	} else if c.includeUsage {
		chunk["usage"] = nil
	}
	return mustJSON(chunk)
}

func (c *responsesToChatStreamConverter) chatUsageChunk(usage any) []byte {
	return mustJSON(map[string]any{
		"id": c.id, "object": "chat.completion.chunk", "created": c.created, "model": c.model,
		"choices": []any{}, "usage": usage,
	})
}
