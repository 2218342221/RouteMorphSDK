package responsesmessages

import (
	"context"
	"encoding/json"
	"fmt"
)

type responsesToMessagesStreamConverter struct {
	id             string
	model          string
	providerModel  string
	clientModel    string
	LossPolicy     lossPolicy
	openBlocks     map[int]bool
	blockIndexes   map[string]int
	itemBlocks     map[string][]int
	itemTypes      map[string]string
	textByBlock    map[int]string
	toolArgs       map[string]string
	toolIdentities map[string]streamToolIdentity
	completedItems map[string]bool
	nextBlock      int
	started        bool
	identityKnown  bool
	completed      bool
	finalized      bool
}

type streamToolIdentity struct {
	callID string
	name   string
}

func (c *responsesToMessagesStreamConverter) Convert(_ context.Context, frame streamFrame) ([]streamFrame, []Diagnostic, error) {
	if c.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", ErrInvalidPlan)
	}
	var event struct {
		Type         string          `json:"type"`
		Delta        string          `json:"delta"`
		Arguments    string          `json:"arguments"`
		ItemID       string          `json:"item_id"`
		OutputIndex  int             `json:"output_index"`
		ContentIndex int             `json:"content_index"`
		Item         responsesItem   `json:"item"`
		Part         json.RawMessage `json:"part"`
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
	if c.completed {
		return nil, nil, invalid(ProtocolResponses, "$.type", "event %q arrived after the terminal response", event.Type)
	}
	switch event.Type {
	case "response.created":
		if c.started {
			return nil, nil, invalid(ProtocolResponses, "$.type", "duplicate response.created event")
		}
		var response responsesResponse
		if len(event.Response) == 0 || string(event.Response) == "null" {
			return nil, nil, invalid(ProtocolResponses, "$.response", "response.created object is required")
		}
		if err := json.Unmarshal(event.Response, &response); err != nil {
			return nil, nil, invalid(ProtocolResponses, "$.response", "invalid response.created object")
		}
		if response.ID == "" || response.Model == "" {
			return nil, nil, invalid(ProtocolResponses, "$.response", "response.created requires id and model")
		}
		if response.Status != "in_progress" && response.Status != "queued" {
			return nil, nil, invalid(ProtocolResponses, "$.response.status", "unexpected created status %q", response.Status)
		}
		c.id, c.providerModel = response.ID, response.Model
		c.model = c.providerModel
		c.started = true
		c.identityKnown = true
		if c.clientModel != "" {
			c.model = c.clientModel
		}
		if response.Usage.InputTokens < response.Usage.InputTokenDetails.CachedTokens {
			return nil, nil, upstreamResponseError(ProtocolResponses, "$.response.usage.input_tokens_details.cached_tokens", "cached tokens exceed input tokens")
		}
		message := map[string]any{"id": c.id, "type": "message", "role": "assistant", "model": c.model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": response.Usage.InputTokens - response.Usage.InputTokenDetails.CachedTokens, "output_tokens": 0, "cache_read_input_tokens": response.Usage.InputTokenDetails.CachedTokens}}
		var diagnostics []Diagnostic
		if response.Usage.InputTokens == 0 {
			diagnostics = appendDiagnostic(diagnostics, "warning", "stream_input_usage_deferred", "$.response.usage", "Responses does not provide final input usage in response.created; cumulative usage is emitted in message_delta")
		}
		return []streamFrame{{Event: "message_start", Data: mustJSON(map[string]any{"type": "message_start", "message": message})}}, diagnostics, nil
	case "response.output_item.added":
		startFrames, startDiagnostics := c.ensureStarted()
		if event.Item.ID == "" {
			return nil, nil, invalid(ProtocolResponses, "$.item.id", "output item id is required")
		}
		if _, exists := c.itemTypes[event.Item.ID]; exists {
			return nil, nil, invalid(ProtocolResponses, "$.item.id", "duplicate output item id %q", event.Item.ID)
		}
		if err := validateResponsesItems([]responsesItem{event.Item}, "$.output"); err != nil {
			return nil, nil, err
		}
		if event.Item.Type != "message" && event.Item.Type != "function_call" {
			return nil, nil, unsupported(ProtocolResponses, "$.item.type", "output item %q cannot stream to Messages", event.Item.Type)
		}
		if event.Item.Type == "message" && event.Item.Role != "assistant" {
			return nil, nil, invalid(ProtocolResponses, "$.item.role", "output message role must be assistant")
		}
		c.itemTypes[event.Item.ID] = event.Item.Type
		phaseDiagnostics := responsesPhaseDiagnostics([]responsesItem{event.Item}, "$.output")
		if event.Item.Type != "function_call" {
			return startFrames, append(startDiagnostics, phaseDiagnostics...), nil
		}
		c.toolIdentities[event.Item.ID] = streamToolIdentity{callID: event.Item.CallID, name: event.Item.Name}
		index := c.allocateBlock(event.Item.ID, -1)
		c.openBlocks[index] = true
		block := map[string]any{"type": "tool_use", "id": event.Item.CallID, "name": event.Item.Name, "input": map[string]any{}}
		startFrames = append(startFrames, streamFrame{Event: "content_block_start", Data: mustJSON(map[string]any{"type": "content_block_start", "index": index, "content_block": block})})
		return startFrames, append(startDiagnostics, phaseDiagnostics...), nil
	case "response.content_part.added":
		startFrames, startDiagnostics := c.ensureStarted()
		if event.ItemID == "" {
			return nil, nil, invalid(ProtocolResponses, "$.item_id", "content part item id is required")
		}
		if itemType, ok := c.itemTypes[event.ItemID]; ok && itemType != "message" {
			return nil, nil, invalid(ProtocolResponses, "$.item_id", "content part belongs to non-message output item %q", event.ItemID)
		} else if !ok {
			// A few Responses-compatible providers omit output_item.added for
			// message items. The content_part event still identifies its item
			// unambiguously, so accept that narrow lifecycle omission.
			c.itemTypes[event.ItemID] = "message"
		}
		var part responsesContentPart
		if err := json.Unmarshal(event.Part, &part); err != nil {
			return nil, nil, invalid(ProtocolResponses, "$.part", "invalid content part")
		}
		if _, err := decodeResponsesContent([]responsesContentPart{part}, "$.part", false); err != nil {
			return nil, nil, err
		}
		if part.Type != "output_text" && part.Type != "refusal" {
			return nil, nil, unsupported(ProtocolResponses, "$.part.type", "content part %q cannot stream to Messages", part.Type)
		}
		if part.Type == "refusal" && c.LossPolicy == rejectSemanticLoss {
			return nil, nil, unsupported(ProtocolResponses, "$.part.type", "Responses refusal content has no equivalent Messages content block")
		}
		key := streamBlockKey(event.ItemID, event.ContentIndex)
		if _, exists := c.blockIndexes[key]; exists {
			return nil, nil, invalid(ProtocolResponses, "$.content_index", "duplicate content part index %d", event.ContentIndex)
		}
		index := c.allocateBlock(event.ItemID, event.ContentIndex)
		c.openBlocks[index] = true
		var diagnostics []Diagnostic
		if part.Type == "refusal" {
			diagnostics = appendDiagnostic(diagnostics, "warning", "refusal_content_approximated", "$.part.type", "Responses refusal content was emitted as a Messages text block")
		}
		diagnostics = append(startDiagnostics, diagnostics...)
		startFrames = append(startFrames, streamFrame{Event: "content_block_start", Data: mustJSON(map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}})})
		return startFrames, diagnostics, nil
	case "response.output_text.delta", "response.refusal.delta":
		startFrames, diagnostics := c.ensureStarted()
		if event.Type == "response.refusal.delta" {
			if c.LossPolicy == rejectSemanticLoss {
				return nil, diagnostics, unsupported(ProtocolResponses, "$.delta", "Responses refusal content has no equivalent Messages content block")
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "refusal_content_approximated", "$.delta", "Responses refusal content was emitted as a Messages text block")
		}
		itemID := event.ItemID
		if itemID == "" {
			itemID = "rm_message"
		}
		key := streamBlockKey(itemID, event.ContentIndex)
		index, ok := c.blockIndexes[key]
		if !ok {
			c.itemTypes[itemID] = "message"
			index = c.allocateBlock(itemID, event.ContentIndex)
			c.openBlocks[index] = true
			startFrames = append(startFrames, streamFrame{Event: "content_block_start", Data: mustJSON(map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}})})
			diagnostics = appendDiagnostic(diagnostics, "warning", "responses_stream_lifecycle_recovered", "$.type", "missing response.created/content_part.added events were synthesized")
		} else if !c.openBlocks[index] {
			return nil, diagnostics, invalid(ProtocolResponses, "$.item_id", "text delta arrived after content part completion")
		}
		c.textByBlock[index] += event.Delta
		startFrames = append(startFrames, streamFrame{Event: "content_block_delta", Data: mustJSON(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": event.Delta}})})
		return startFrames, diagnostics, nil
	case "response.function_call_arguments.delta":
		index, ok := c.blockIndexes[streamBlockKey(event.ItemID, -1)]
		if !ok {
			// Some compatible providers only send argument deltas plus a full
			// terminal item. Buffer them until the item identity is available.
			c.toolArgs[event.ItemID] += event.Delta
			startFrames, diagnostics := c.ensureStarted()
			return startFrames, appendDiagnostic(diagnostics, "warning", "responses_stream_lifecycle_recovered", "$.item_id", "function arguments arrived before output_item.added and were buffered"), nil
		}
		if !c.openBlocks[index] {
			return nil, nil, invalid(ProtocolResponses, "$.item_id", "function delta arrived after output item completion")
		}
		c.toolArgs[event.ItemID] += event.Delta
		return []streamFrame{{Event: "content_block_delta", Data: mustJSON(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": event.Delta}})}}, nil, nil
	case "response.function_call_arguments.done":
		itemID := event.ItemID
		index, ok := c.blockIndexes[streamBlockKey(itemID, -1)]
		if !ok || !c.openBlocks[index] {
			return nil, nil, nil
		}
		complete := event.Arguments
		suffix, err := remainingStreamText(c.toolArgs[itemID], complete, "$.arguments")
		if err != nil {
			return nil, nil, err
		}
		if suffix == "" {
			return nil, nil, nil
		}
		c.toolArgs[itemID] += suffix
		return []streamFrame{{Event: "content_block_delta", Data: mustJSON(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": suffix}})}}, nil, nil
	case "response.content_part.done":
		index, ok := c.blockIndexes[streamBlockKey(event.ItemID, event.ContentIndex)]
		if !ok || !c.openBlocks[index] {
			return nil, nil, nil
		}
		delete(c.openBlocks, index)
		return []streamFrame{{Event: "content_block_stop", Data: mustJSON(map[string]any{"type": "content_block_stop", "index": index})}}, nil, nil
	case "response.output_item.done":
		if event.Item.ID == "" {
			return nil, nil, invalid(ProtocolResponses, "$.item.id", "completed output item id is required")
		}
		if event.ItemID != "" && event.ItemID != event.Item.ID {
			return nil, nil, invalid(ProtocolResponses, "$.item_id", "output item identity changed from %q to %q", event.ItemID, event.Item.ID)
		}
		itemID := event.Item.ID
		if c.completedItems[itemID] {
			return nil, nil, invalid(ProtocolResponses, "$.item.id", "duplicate output item completion for %q", itemID)
		}
		knownType, known := c.itemTypes[itemID]
		if !known && event.Item.Type != "function_call" {
			return nil, nil, invalid(ProtocolResponses, "$.item_id", "output item done before output item added")
		}
		if err := validateResponsesItems([]responsesItem{event.Item}, "$.output"); err != nil {
			return nil, nil, err
		}
		if known && event.Item.Type != knownType {
			return nil, nil, invalid(ProtocolResponses, "$.item.type", "output item identity changed from type %q to %q", knownType, event.Item.Type)
		}
		if event.Item.Type == "function_call" {
			if err := c.validateToolIdentity(event.Item, "$.item"); err != nil {
				return nil, nil, err
			}
			if !jsonValuePresent(event.Item.Arguments) {
				return nil, nil, invalid(ProtocolResponses, "$.item.arguments", "completed function_call arguments are required")
			}
		}
		if !known {
			startFrames, diagnostics := c.ensureStarted()
			c.itemTypes[itemID] = "function_call"
			index := c.allocateBlock(itemID, -1)
			c.openBlocks[index] = true
			startFrames = append(startFrames, streamFrame{Event: "content_block_start", Data: mustJSON(map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": event.Item.CallID, "name": event.Item.Name, "input": map[string]any{}}})})
			arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, "$.item.arguments", event.Item.Arguments)
			if err != nil {
				return nil, diagnostics, err
			}
			if _, err := remainingStreamText(c.toolArgs[itemID], string(arguments), "$.item.arguments"); err != nil {
				return nil, diagnostics, err
			}
			if len(arguments) > 0 {
				startFrames = append(startFrames, streamFrame{Event: "content_block_delta", Data: mustJSON(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(arguments)}})})
			}
			c.toolArgs[itemID] = string(arguments)
			delete(c.openBlocks, index)
			startFrames = append(startFrames, streamFrame{Event: "content_block_stop", Data: mustJSON(map[string]any{"type": "content_block_stop", "index": index})})
			c.completedItems[itemID] = true
			return startFrames, appendDiagnostic(diagnostics, "warning", "responses_stream_lifecycle_recovered", "$.item", "missing output_item.added event was synthesized"), nil
		}
		var frames []streamFrame
		if event.Item.Type == "function_call" {
			index := c.blockIndexes[streamBlockKey(itemID, -1)]
			arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, "$.item.arguments", event.Item.Arguments)
			if err != nil {
				return nil, nil, err
			}
			suffix, err := remainingStreamText(c.toolArgs[itemID], string(arguments), "$.item.arguments")
			if err != nil {
				return nil, nil, err
			}
			if suffix != "" {
				frames = append(frames, streamFrame{Event: "content_block_delta", Data: mustJSON(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": suffix}})})
				c.toolArgs[itemID] += suffix
			}
		}
		for _, index := range c.itemBlocks[itemID] {
			if c.openBlocks[index] {
				delete(c.openBlocks, index)
				frames = append(frames, streamFrame{Event: "content_block_stop", Data: mustJSON(map[string]any{"type": "content_block_stop", "index": index})})
			}
		}
		c.completedItems[itemID] = true
		return frames, nil, nil
	case "response.completed", "response.incomplete", "response.failed":
		var response responsesResponse
		if err := json.Unmarshal(event.Response, &response); err != nil {
			return nil, nil, invalid(ProtocolResponses, "$.response", "invalid terminal response")
		}
		if err := validateResponsesTerminal(response); err != nil {
			return nil, nil, err
		}
		if response.Usage.InputTokens < response.Usage.InputTokenDetails.CachedTokens {
			return nil, nil, upstreamResponseError(ProtocolResponses, "$.response.usage.input_tokens_details.cached_tokens", "cached tokens exceed input tokens")
		}
		if c.identityKnown && response.ID != c.id {
			return nil, nil, invalid(ProtocolResponses, "$.response.id", "terminal response id %q does not match %q", response.ID, c.id)
		}
		if c.identityKnown && response.Model != "" && response.Model != c.providerModel {
			return nil, nil, invalid(ProtocolResponses, "$.response.model", "terminal response model %q does not match %q", response.Model, c.providerModel)
		}
		if (event.Type == "response.completed") != (response.Status == "completed") || (event.Type == "response.incomplete") != (response.Status == "incomplete") {
			return nil, nil, invalid(ProtocolResponses, "$.response.status", "terminal event %q does not match status %q", event.Type, response.Status)
		}
		for index, item := range response.Output {
			path := fmt.Sprintf("$.response.output[%d]", index)
			if item.Type != "message" && item.Type != "function_call" {
				return nil, nil, unsupported(ProtocolResponses, path+".type", "output item %q cannot stream to Messages", item.Type)
			}
			if item.Type == "message" && item.Role != "assistant" {
				return nil, nil, upstreamResponseError(ProtocolResponses, path+".role", "output message role must be assistant")
			}
		}
		var diagnostics []Diagnostic
		if response.Usage.OutputTokenDetails.ReasoningTokens > 0 {
			if c.LossPolicy == rejectSemanticLoss {
				return nil, nil, unsupported(ProtocolResponses, "$.response.usage.output_tokens_details.reasoning_tokens", "Messages usage has no reasoning-token field")
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "reasoning_usage_not_representable", "$.response.usage.output_tokens_details.reasoning_tokens", "reasoning-token usage was omitted")
		}
		frames, fallbackDiagnostics, err := c.terminalOutputFrames(response)
		if err != nil {
			return nil, append(diagnostics, fallbackDiagnostics...), err
		}
		diagnostics = append(diagnostics, fallbackDiagnostics...)
		for blockIndex := 0; blockIndex < c.nextBlock; blockIndex++ {
			if c.openBlocks[blockIndex] {
				frames = append(frames, streamFrame{Event: "content_block_stop", Data: mustJSON(map[string]any{"type": "content_block_stop", "index": blockIndex})})
			}
		}
		c.openBlocks = make(map[int]bool)
		stop := "end_turn"
		for _, item := range response.Output {
			if item.Type == "function_call" {
				stop = "tool_use"
			}
		}
		if response.Status == "incomplete" {
			stop = "max_tokens"
			if response.IncompleteDetails != nil && response.IncompleteDetails.Reason == "content_filter" {
				stop = "refusal"
			}
		}
		frames = append(frames,
			streamFrame{Event: "message_delta", Data: mustJSON(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"input_tokens": response.Usage.InputTokens - response.Usage.InputTokenDetails.CachedTokens, "output_tokens": response.Usage.OutputTokens, "cache_read_input_tokens": response.Usage.InputTokenDetails.CachedTokens, "cache_creation_input_tokens": 0}})},
			streamFrame{Event: "message_stop", Data: mustJSON(map[string]any{"type": "message_stop"})},
		)
		c.completed = true
		return frames, diagnostics, nil
	case "response.queued", "response.in_progress", "response.output_text.done", "response.refusal.done":
		return nil, nil, nil
	case "error":
		return nil, nil, upstreamResponseError(ProtocolResponses, "$", "Responses stream returned an error event")
	default:
		return nil, nil, unsupported(ProtocolResponses, "$.type", "stream event %q is not supported", event.Type)
	}
}

func (c *responsesToMessagesStreamConverter) validateToolIdentity(item responsesItem, path string) error {
	identity := streamToolIdentity{callID: item.CallID, name: item.Name}
	expected, exists := c.toolIdentities[item.ID]
	if !exists {
		c.toolIdentities[item.ID] = identity
		return nil
	}
	if expected != identity {
		return invalid(ProtocolResponses, path, "function_call identity changed for item %q", item.ID)
	}
	return nil
}

func (c *responsesToMessagesStreamConverter) ensureStarted() ([]streamFrame, []Diagnostic) {
	if c.started {
		return nil, nil
	}
	c.started = true
	if c.id == "" {
		c.id = "msg_routemorph"
	}
	if c.model == "" {
		c.model = c.clientModel
	}
	if c.model == "" {
		c.model = "routemorph"
	}
	message := map[string]any{"id": c.id, "type": "message", "role": "assistant", "model": c.model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}
	return []streamFrame{{Event: "message_start", Data: mustJSON(map[string]any{"type": "message_start", "message": message})}}, []Diagnostic{{Severity: "warning", Code: "responses_stream_lifecycle_recovered", Path: "$.type", Message: "response.created was missing; a Messages stream start was synthesized"}}
}

func (c *responsesToMessagesStreamConverter) terminalOutputFrames(response responsesResponse) ([]streamFrame, []Diagnostic, error) {
	var emittedText string
	for index := 0; index < c.nextBlock; index++ {
		emittedText += c.textByBlock[index]
	}
	frames, diagnostics := c.ensureStarted()
	remainingText := emittedText
	terminalItems := make(map[string]string, len(response.Output))
	for index, item := range response.Output {
		path := fmt.Sprintf("$.response.output[%d]", index)
		if item.ID != "" {
			if _, duplicate := terminalItems[item.ID]; duplicate {
				return nil, diagnostics, upstreamResponseError(ProtocolResponses, path+".id", "duplicate terminal output item id %q", item.ID)
			}
			terminalItems[item.ID] = item.Type
		}
		if item.Type == "message" {
			parts, err := decodeResponsesContentRaw(item.Content, path+".content", false)
			if err != nil {
				return nil, diagnostics, err
			}
			for partIndex, part := range parts {
				if part.Kind == partRefusal {
					if c.LossPolicy == rejectSemanticLoss {
						return nil, diagnostics, unsupported(ProtocolResponses, fmt.Sprintf("%s.content[%d]", path, partIndex), "Responses refusal content has no equivalent Messages content block")
					}
					diagnostics = appendDiagnostic(diagnostics, "warning", "refusal_content_approximated", fmt.Sprintf("%s.content[%d]", path, partIndex), "Responses refusal content was emitted as a Messages text block")
				}
				suffix, rest, err := consumeStreamPrefix(remainingText, part.Text, path+".content")
				if err != nil {
					return nil, diagnostics, err
				}
				remainingText = rest
				if suffix != "" {
					itemID := fmt.Sprintf("rm_terminal_message_%d_%d", index, partIndex)
					blockIndex := c.allocateBlock(itemID, 0)
					c.openBlocks[blockIndex] = true
					frames = append(frames,
						streamFrame{Event: "content_block_start", Data: mustJSON(map[string]any{"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{"type": "text", "text": ""}})},
						streamFrame{Event: "content_block_delta", Data: mustJSON(map[string]any{"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "text_delta", "text": suffix}})},
					)
					c.textByBlock[blockIndex] = suffix
				}
			}
			continue
		}
		if item.Type != "function_call" {
			continue
		}
		if item.ID == "" || item.CallID == "" || item.Name == "" {
			return nil, diagnostics, upstreamResponseError(ProtocolResponses, path, "function_call requires id, call_id, and name")
		}
		if err := c.validateToolIdentity(item, path); err != nil {
			return nil, diagnostics, err
		}
		if !jsonValuePresent(item.Arguments) {
			return nil, diagnostics, upstreamResponseError(ProtocolResponses, path+".arguments", "terminal function_call arguments are required")
		}
		arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, path+".arguments", item.Arguments)
		if err != nil {
			return nil, diagnostics, err
		}
		if c.completedItems[item.ID] {
			suffix, err := remainingStreamText(c.toolArgs[item.ID], string(arguments), path+".arguments")
			if err != nil {
				return nil, diagnostics, err
			}
			if suffix != "" {
				return nil, diagnostics, upstreamResponseError(ProtocolResponses, path+".arguments", "terminal arguments contain data after output item completion")
			}
			continue
		}
		blockIndex, exists := c.blockIndexes[streamBlockKey(item.ID, -1)]
		if !exists {
			blockIndex = c.allocateBlock(item.ID, -1)
			c.openBlocks[blockIndex] = true
			block := map[string]any{"type": "tool_use", "id": item.CallID, "name": item.Name, "input": map[string]any{}}
			frames = append(frames, streamFrame{
				Event: "content_block_start",
				Data:  mustJSON(map[string]any{"type": "content_block_start", "index": blockIndex, "content_block": block}),
			})
		}
		suffix, err := remainingStreamText(c.toolArgs[item.ID], string(arguments), path+".arguments")
		if err != nil {
			return nil, diagnostics, err
		}
		if suffix != "" {
			frames = append(frames, streamFrame{Event: "content_block_delta", Data: mustJSON(map[string]any{"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "input_json_delta", "partial_json": suffix}})})
			c.toolArgs[item.ID] += suffix
		}
		c.completedItems[item.ID] = true
	}
	if remainingText != "" {
		return nil, diagnostics, upstreamResponseError(ProtocolResponses, "$.response.output", "terminal output is missing content previously emitted as deltas")
	}
	for itemID, itemType := range c.itemTypes {
		if itemID == "rm_message" {
			continue
		}
		terminalType, exists := terminalItems[itemID]
		if !exists {
			return nil, diagnostics, upstreamResponseError(ProtocolResponses, "$.response.output", "terminal output is missing streamed item %q", itemID)
		}
		if terminalType != itemType {
			return nil, diagnostics, upstreamResponseError(ProtocolResponses, "$.response.output", "terminal item %q changed type from %q to %q", itemID, itemType, terminalType)
		}
	}
	return frames, diagnostics, nil
}

func (c *responsesToMessagesStreamConverter) allocateBlock(itemID string, contentIndex int) int {
	key := streamBlockKey(itemID, contentIndex)
	if index, ok := c.blockIndexes[key]; ok {
		return index
	}
	index := c.nextBlock
	c.nextBlock++
	c.blockIndexes[key] = index
	c.itemBlocks[itemID] = append(c.itemBlocks[itemID], index)
	return index
}

func streamBlockKey(itemID string, contentIndex int) string {
	return fmt.Sprintf("%s/%d", itemID, contentIndex)
}

func (c *responsesToMessagesStreamConverter) Finalize(context.Context) ([]streamFrame, []Diagnostic, error) {
	if c.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", ErrInvalidPlan)
	}
	c.finalized = true
	if !c.completed {
		return nil, nil, invalid(ProtocolResponses, "$", "stream ended before a terminal response event")
	}
	return nil, nil, nil
}
