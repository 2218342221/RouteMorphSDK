package responsesgemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type geminiToResponsesConverter struct {
	spec routeSpec
}

func (c *geminiToResponsesConverter) Specification() routeSpec { return c.spec }

func (c *geminiToResponsesConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
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
	if err := validateGeminiPortableRequest(&source); err != nil {
		return conversionResult{}, err
	}
	if source.CachedContent != "" {
		return conversionResult{}, unsupported(ProtocolGenerateContent, "$.cachedContent", "cached content state requires a native generateContent provider")
	}
	if len(source.SafetySettings) > 0 && string(source.SafetySettings) != "null" && string(source.SafetySettings) != "[]" {
		return conversionResult{}, unsupported(ProtocolGenerateContent, "$.safetySettings", "provider safety policy is not portable")
	}
	target := responsesRequest{Model: options.Exchange.UpstreamModel, Stream: resolveExchangeStream(false, options.Exchange)}
	var diagnostics []Diagnostic
	if source.GenerationConfig != nil {
		config := source.GenerationConfig
		target.MaxOutputTokens, target.Temperature, target.TopP = config.MaxOutputTokens, config.Temperature, config.TopP
		if len(config.StopSequences) > 0 {
			return conversionResult{}, unsupported(ProtocolGenerateContent, "$.generationConfig.stopSequences", "Responses has no equivalent stop parameter")
		}
		schema := config.ResponseJSONSchema
		if !jsonValuePresent(schema) {
			schema = config.ResponseSchema
		}
		if jsonValuePresent(schema) {
			normalized, err := messagesGeminiSchemaToJSONSchema(schema, "$.generationConfig.responseSchema")
			if err != nil {
				return conversionResult{}, err
			}
			target.Text = mustJSON(map[string]any{"format": map[string]any{"type": "json_schema", "name": "response", "schema": normalized}})
		} else if config.ResponseMIMEType == "application/json" {
			target.Text = mustJSON(map[string]any{"format": map[string]any{"type": "json_object"}})
		}
		if config.ThinkingConfig != nil {
			if options.LossPolicy == rejectSemanticLoss {
				return conversionResult{}, unsupported(ProtocolGenerateContent, "$.generationConfig.thinkingConfig", "Gemini thinking is not semantically equivalent to Responses reasoning")
			}
			target.Reasoning = &reasoningConfig{Effort: strings.ToLower(config.ThinkingConfig.ThinkingLevel)}
			if config.ThinkingConfig.IncludeThoughts {
				target.Reasoning.Summary = "auto"
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "thinking_policy_approximated", "$.generationConfig.thinkingConfig", "Gemini thinking level was approximated as Responses reasoning effort")
		}
	}
	if source.SystemInstruction != nil {
		parts, err := decodeGeminiParts(source.SystemInstruction.Parts, "$.systemInstruction.parts")
		if err != nil {
			return conversionResult{}, err
		}
		content, err := encodeResponsesContent(parts, true)
		if err != nil {
			return conversionResult{}, err
		}
		instructionText, err := portableTextOnly(ProtocolGenerateContent, content, "$.systemInstruction")
		if err != nil {
			return conversionResult{}, err
		}
		target.Instructions = json.RawMessage(mustJSONString(instructionText))
	}
	for toolIndex, tool := range source.Tools {
		for functionIndex, function := range tool.FunctionDeclarations {
			parameters, err := messagesGeminiSchemaToJSONSchema(function.Parameters, fmt.Sprintf("$.tools[%d].functionDeclarations[%d].parameters", toolIndex, functionIndex))
			if err != nil {
				return conversionResult{}, err
			}
			target.Tools = append(target.Tools, responsesTool{Type: "function", Name: function.Name, Description: function.Description, Parameters: parameters})
		}
	}
	if source.ToolConfig != nil {
		mode := source.ToolConfig.FunctionCallingConfig.Mode
		allowed := source.ToolConfig.FunctionCallingConfig.AllowedFunctionNames
		var choice toolChoice
		switch mode {
		case "", "AUTO":
			if len(allowed) != 0 {
				return conversionResult{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "AUTO with allowedFunctionNames has no exact Responses tool-choice mapping")
			}
			choice.Mode = toolChoiceAuto
		case "NONE":
			if len(allowed) != 0 {
				return conversionResult{}, invalid(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "allowedFunctionNames is not valid with NONE")
			}
			choice.Mode = toolChoiceNone
		case "ANY":
			choice.Mode = toolChoiceRequired
			if len(allowed) == 1 {
				choice = toolChoice{Mode: toolChoiceNamed, Name: allowed[0]}
			} else if len(allowed) > 1 {
				return conversionResult{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.allowedFunctionNames", "Responses cannot express a choice restricted to multiple named functions")
			}
		case "VALIDATED":
			return conversionResult{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.mode", "VALIDATED permits either natural-language output or a validated function call and has no exact Responses mapping")
		default:
			return conversionResult{}, unsupported(ProtocolGenerateContent, "$.toolConfig.functionCallingConfig.mode", "mode %q is not portable", mode)
		}
		target.ToolChoice = encodeResponsesToolChoice(choice)
	}
	var items []responsesItem
	callIDsByName := make(map[string][]string)
	callNamesByID := make(map[string]string)
	consumedCallIDs := make(map[string]bool)
	generatedCallID := 0
	for contentIndex, content := range source.Contents {
		role := "user"
		if content.Role == "model" {
			role = "assistant"
		} else if content.Role != "" && content.Role != "user" {
			return conversionResult{}, unsupported(ProtocolGenerateContent, fmt.Sprintf("$.contents[%d].role", contentIndex), "role %q is not portable", content.Role)
		}
		var ordinary []portablePart
		flush := func() error {
			if len(ordinary) == 0 {
				return nil
			}
			converted, err := encodeResponsesContent(ordinary, true)
			if err != nil {
				return err
			}
			items = append(items, responsesItem{Type: "message", Role: role, Content: mustJSON(converted)})
			ordinary = nil
			return nil
		}
		for partIndex, part := range content.Parts {
			path := fmt.Sprintf("$.contents[%d].parts[%d]", contentIndex, partIndex)
			if err := validateGeminiPart(part, path); err != nil {
				return conversionResult{}, err
			}
			switch {
			case part.FunctionCall != nil:
				if role != "assistant" {
					return conversionResult{}, invalid(ProtocolGenerateContent, path+".functionCall", "functionCall is only valid in model content")
				}
				if part.ThoughtSignature != "" {
					return conversionResult{}, unsupported(ProtocolGenerateContent, path+".thoughtSignature", "Gemini function-call signatures cannot be represented by Responses")
				}
				if err := flush(); err != nil {
					return conversionResult{}, err
				}
				arguments, err := normalizeGeminiToolArguments(ProtocolGenerateContent, path+".functionCall.args", part.FunctionCall.Args)
				if err != nil {
					return conversionResult{}, err
				}
				callID := part.FunctionCall.ID
				if callID == "" {
					generatedCallID++
					callID = fmt.Sprintf("rm_call_%d", generatedCallID)
					diagnostics = appendDiagnostic(diagnostics, "warning", "generated_function_call_id", path+".functionCall.id", "Gemini omitted the optional function call id; RouteMorph generated a request-local id")
				}
				if _, exists := callNamesByID[callID]; exists {
					return conversionResult{}, invalid(ProtocolGenerateContent, path+".functionCall.id", "duplicate function call id %q", callID)
				}
				callNamesByID[callID] = part.FunctionCall.Name
				callIDsByName[part.FunctionCall.Name] = append(callIDsByName[part.FunctionCall.Name], callID)
				items = append(items, responsesItem{Type: "function_call", CallID: callID, Name: part.FunctionCall.Name, Arguments: json.RawMessage(mustJSONString(string(arguments)))})
			case part.FunctionResponse != nil:
				if role != "user" {
					return conversionResult{}, invalid(ProtocolGenerateContent, path+".functionResponse", "functionResponse is only valid in user content")
				}
				if err := flush(); err != nil {
					return conversionResult{}, err
				}
				callID := part.FunctionResponse.ID
				if callID == "" {
					ids := callIDsByName[part.FunctionResponse.Name]
					for len(ids) > 0 && consumedCallIDs[ids[0]] {
						ids = ids[1:]
					}
					if len(ids) > 0 {
						callID, ids = ids[0], ids[1:]
					}
					callIDsByName[part.FunctionResponse.Name] = ids
				}
				if callID == "" {
					return conversionResult{}, unsupported(ProtocolGenerateContent, path+".functionResponse.id", "function response cannot be correlated with an earlier function call")
				}
				callName, exists := callNamesByID[callID]
				if !exists {
					return conversionResult{}, invalid(ProtocolGenerateContent, path+".functionResponse.id", "function response id %q does not match an earlier function call", callID)
				}
				if consumedCallIDs[callID] {
					return conversionResult{}, invalid(ProtocolGenerateContent, path+".functionResponse.id", "function call id %q already has a response", callID)
				}
				if part.FunctionResponse.Name != callName {
					return conversionResult{}, invalid(ProtocolGenerateContent, path+".functionResponse.name", "function response name %q does not match call name %q", part.FunctionResponse.Name, callName)
				}
				consumedCallIDs[callID] = true
				items = append(items, responsesItem{Type: "function_call_output", CallID: callID, Output: json.RawMessage(mustJSONString(string(part.FunctionResponse.Response)))})
			case part.Thought || part.ThoughtSignature != "":
				return conversionResult{}, unsupported(ProtocolGenerateContent, path, "Gemini thought and thoughtSignature cannot be injected into Responses")
			default:
				parts, err := decodeGeminiParts([]geminiPart{part}, path)
				if err != nil {
					return conversionResult{}, err
				}
				ordinary = append(ordinary, parts...)
			}
		}
		if err := flush(); err != nil {
			return conversionResult{}, err
		}
	}
	target.Input = mustJSON(items)
	body, err := marshal(ProtocolResponses, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *geminiToResponsesConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source responsesResponse
	if err := decodeJSON(ProtocolResponses, input, &source); err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesTerminal(source); err != nil {
		return conversionResult{}, err
	}
	var target geminiResponse
	target.ResponseID, target.ModelVersion = source.ID, source.Model
	if options.Exchange.ClientModel != "" {
		target.ModelVersion = options.Exchange.ClientModel
	}
	var parts []geminiPart
	diagnostics := responsesPhaseDiagnostics(source.Output, "$.output")
	for index, item := range source.Output {
		path := fmt.Sprintf("$.output[%d]", index)
		switch item.Type {
		case "message":
			portable, err := decodeResponsesContentRaw(item.Content, path+".content", false)
			if err != nil {
				return conversionResult{}, err
			}
			for partIndex, part := range portable {
				if part.Kind != partRefusal {
					continue
				}
				if options.LossPolicy == rejectSemanticLoss {
					return conversionResult{}, unsupported(ProtocolResponses, fmt.Sprintf("%s.content[%d]", path, partIndex), "Responses refusal content has no Gemini equivalent")
				}
				diagnostics = appendDiagnostic(diagnostics, "warning", "responses_refusal_approximated", fmt.Sprintf("%s.content[%d]", path, partIndex), "Responses refusal content was emitted as Gemini text")
			}
			converted, err := encodeGeminiParts(portable)
			if err != nil {
				return conversionResult{}, err
			}
			parts = append(parts, converted...)
		case "function_call":
			arguments, err := normalizeArguments(ProtocolResponses, path+".arguments", item.Arguments)
			if err != nil {
				return conversionResult{}, err
			}
			parts = append(parts, geminiPart{FunctionCall: &geminiFunctionCall{ID: item.CallID, Name: item.Name, Args: arguments}, ThoughtSignature: geminiThoughtSignatureBypass})
			diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_thought_signature_bypass_added", path, "a Gemini-compatible thought signature bypass was attached to the converted function call")
			if item.ID != "" && item.ID != item.CallID {
				diagnostics = appendDiagnostic(diagnostics, "warning", "responses_item_id_not_representable", path+".id", "Gemini preserves call_id but has no separate output item id")
			}
		case "reasoning":
			return conversionResult{}, unsupported(ProtocolResponses, path, "Responses reasoning summary is not equivalent to Gemini signed thought")
		default:
			return conversionResult{}, unsupported(ProtocolResponses, path+".type", "output item %q cannot be represented by Gemini", item.Type)
		}
	}
	finish := finishStop
	if source.Status == "incomplete" {
		finish = finishLength
		if source.IncompleteDetails != nil && source.IncompleteDetails.Reason == "content_filter" {
			finish = finishContentFilter
		}
	}
	target.Candidates = append(target.Candidates, geminiCandidate{Content: geminiContent{Role: "model", Parts: parts}, FinishReason: geminiStop(finish)})
	target.UsageMetadata.PromptTokenCount = source.Usage.InputTokens
	target.UsageMetadata.CandidatesTokenCount = source.Usage.OutputTokens - source.Usage.OutputTokenDetails.ReasoningTokens
	if target.UsageMetadata.CandidatesTokenCount < 0 {
		return conversionResult{}, invalid(ProtocolResponses, "$.usage.output_tokens_details.reasoning_tokens", "reasoning tokens cannot exceed output tokens")
	}
	target.UsageMetadata.TotalTokenCount = source.Usage.TotalTokens
	target.UsageMetadata.CachedContentTokenCount = source.Usage.InputTokenDetails.CachedTokens
	target.UsageMetadata.ThoughtsTokenCount = source.Usage.OutputTokenDetails.ReasoningTokens
	body, err := marshal(ProtocolGenerateContent, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *geminiToResponsesConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return &responsesToGeminiStreamConverter{clientModel: options.Exchange.ClientModel, LossPolicy: options.LossPolicy, calls: make(map[string]*geminiFunctionCall), items: make(map[string]responsesItem), completedItems: make(map[string]bool), completedArguments: make(map[string]string)}, nil
}

type responsesToGeminiStreamConverter struct {
	id                 string
	model              string
	providerID         string
	providerModel      string
	clientModel        string
	LossPolicy         lossPolicy
	calls              map[string]*geminiFunctionCall
	items              map[string]responsesItem
	completedItems     map[string]bool
	completedArguments map[string]string
	emittedText        string
	emittedRefusal     string
	completed          bool
	finalized          bool
}

func (c *responsesToGeminiStreamConverter) Convert(_ context.Context, frame streamFrame) ([]streamFrame, []Diagnostic, error) {
	if c.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", ErrInvalidPlan)
	}
	if c.completed {
		return nil, nil, invalid(ProtocolResponses, "$", "stream event arrived after terminal response")
	}
	if frame.Done || string(frame.Data) == "[DONE]" {
		return nil, nil, nil
	}
	var event struct {
		Type      string          `json:"type"`
		Delta     string          `json:"delta"`
		Arguments string          `json:"arguments"`
		ItemID    string          `json:"item_id"`
		Item      responsesItem   `json:"item"`
		Part      json.RawMessage `json:"part"`
		Response  json.RawMessage `json:"response"`
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
		return nil, nil, nil
	case "response.output_text.delta":
		if err := c.validateKnownItem(event.ItemID, "message"); err != nil {
			return nil, nil, err
		}
		c.emittedText += event.Delta
		return []streamFrame{{Data: c.chunk([]geminiPart{{Text: event.Delta}}, "", nil)}}, nil, nil
	case "response.refusal.delta":
		if err := c.validateKnownItem(event.ItemID, "message"); err != nil {
			return nil, nil, err
		}
		if c.LossPolicy == rejectSemanticLoss {
			return nil, nil, unsupported(ProtocolResponses, "$.delta", "Responses refusal content has no Gemini equivalent")
		}
		c.emittedRefusal += event.Delta
		return []streamFrame{{Data: c.chunk([]geminiPart{{Text: event.Delta}}, "", nil)}}, []Diagnostic{{Severity: "warning", Code: "responses_refusal_approximated", Path: "$.delta", Message: "Responses refusal content was emitted as Gemini text"}}, nil
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
		phaseDiagnostics := responsesPhaseDiagnostics([]responsesItem{event.Item}, "$.output")
		switch event.Item.Type {
		case "message":
		case "function_call":
			if event.Item.ID == "" || event.Item.CallID == "" || event.Item.Name == "" {
				return nil, nil, invalid(ProtocolResponses, "$.item", "function_call requires id, call_id, and name")
			}
			c.calls[event.Item.ID] = &geminiFunctionCall{ID: event.Item.CallID, Name: event.Item.Name, Args: json.RawMessage{}}
		case "reasoning":
			return nil, nil, unsupported(ProtocolResponses, "$.item", "Responses reasoning cannot be represented as a signed Gemini thought")
		default:
			return nil, nil, unsupported(ProtocolResponses, "$.item.type", "output item %q cannot be represented by Gemini", event.Item.Type)
		}
		return nil, phaseDiagnostics, nil
	case "response.function_call_arguments.delta":
		call := c.calls[event.ItemID]
		if call == nil {
			return nil, nil, invalid(ProtocolResponses, "$.item_id", "function arguments delta before function item")
		}
		call.Args = append(call.Args, event.Delta...)
		return nil, nil, nil
	case "response.output_item.done":
		if err := c.validateOutputItemDone(event.ItemID, event.Item); err != nil {
			return nil, nil, err
		}
		if event.Item.Type == "message" {
			return nil, nil, nil
		}
		call := c.calls[event.Item.ID]
		if call == nil {
			arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, "$.item.arguments", event.Item.Arguments)
			if err != nil {
				return nil, nil, err
			}
			call = &geminiFunctionCall{ID: event.Item.CallID, Name: event.Item.Name, Args: arguments}
		} else {
			arguments := call.Args
			if jsonValuePresent(event.Item.Arguments) {
				complete, err := normalizeOpenAIToolArguments(ProtocolResponses, "$.item.arguments", event.Item.Arguments)
				if err != nil {
					return nil, nil, err
				}
				if len(arguments) > 0 && !strings.HasPrefix(string(complete), string(arguments)) {
					return nil, nil, upstreamResponseError(ProtocolResponses, "$.item.arguments", "completed function arguments do not match streamed deltas")
				}
				arguments = complete
			}
			arguments, err := normalizeGeminiToolArguments(ProtocolResponses, "$.item.arguments", arguments)
			if err != nil {
				return nil, nil, err
			}
			call.Args = arguments
		}
		delete(c.calls, event.Item.ID)
		c.completedItems[event.Item.ID] = true
		c.completedArguments[event.Item.ID] = string(call.Args)
		return []streamFrame{{Data: c.chunk([]geminiPart{{FunctionCall: call, ThoughtSignature: geminiThoughtSignatureBypass}}, "", nil)}}, []Diagnostic{{Severity: "warning", Code: "gemini_thought_signature_bypass_added", Path: "$.item", Message: "a Gemini-compatible thought signature bypass was attached to the converted function call"}}, nil
	case "response.content_part.added", "response.content_part.done":
		if err := c.validateContentPartEvent(event.ItemID, event.Part); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	case "response.output_text.done", "response.refusal.done":
		if err := c.validateKnownItem(event.ItemID, "message"); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	case "response.function_call_arguments.done":
		if err := c.validateKnownItem(event.ItemID, "function_call"); err != nil {
			return nil, nil, err
		}
		call := c.calls[event.ItemID]
		if call == nil {
			return nil, nil, invalid(ProtocolResponses, "$.item_id", "function arguments completed before their function_call item")
		}
		if !strings.HasPrefix(event.Arguments, string(call.Args)) {
			return nil, nil, upstreamResponseError(ProtocolResponses, "$.arguments", "completed function arguments do not match streamed deltas")
		}
		return nil, nil, nil
	case "response.queued", "response.in_progress":
		return nil, nil, nil
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_text.delta", "response.reasoning_text.done":
		if err := c.validateKnownItem(event.ItemID, "reasoning"); err != nil {
			return nil, nil, err
		}
		return nil, nil, unsupported(ProtocolResponses, "$.type", "Responses reasoning cannot be represented as a signed Gemini thought")
	case "response.completed", "response.incomplete", "response.failed":
		var response responsesResponse
		if err := json.Unmarshal(event.Response, &response); err != nil {
			return nil, nil, invalid(ProtocolResponses, "$.response", "invalid terminal response")
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
		fallback, diagnostics, err := c.terminalOutputChunks(response)
		if err != nil {
			return nil, diagnostics, err
		}
		finish := finishStop
		if response.Status == "incomplete" {
			finish = finishLength
			if response.IncompleteDetails != nil && response.IncompleteDetails.Reason == "content_filter" {
				finish = finishContentFilter
			}
		}
		if len(c.calls) != 0 {
			return nil, nil, invalid(ProtocolResponses, "$.output", "terminal response arrived before all function calls completed")
		}
		if response.Usage.OutputTokenDetails.ReasoningTokens > response.Usage.OutputTokens {
			return nil, diagnostics, upstreamResponseError(ProtocolResponses, "$.response.usage.output_tokens_details.reasoning_tokens", "reasoning tokens cannot exceed output tokens")
		}
		usage := &response.Usage
		c.completed = true
		fallback = append(fallback, streamFrame{Data: c.chunk(nil, geminiStop(finish), usage)})
		return fallback, diagnostics, nil
	case "error":
		return nil, nil, upstreamResponseError(ProtocolResponses, "$", "Responses stream returned an error event")
	default:
		return nil, nil, unsupported(ProtocolResponses, "$.type", "stream event %q is not supported by the Gemini conversion", event.Type)
	}
}

func (c *responsesToGeminiStreamConverter) setBase(response responsesResponse, path string) error {
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
	if c.clientModel != "" {
		c.model = c.clientModel
	} else if c.providerModel != "" {
		c.model = c.providerModel
	}
	return nil
}

func (c *responsesToGeminiStreamConverter) validateKnownItem(itemID, wantType string) error {
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

func (c *responsesToGeminiStreamConverter) validateContentPartEvent(itemID string, raw json.RawMessage) error {
	if err := c.validateKnownItem(itemID, "message"); err != nil {
		return err
	}
	var part responsesContentPart
	if len(raw) == 0 || json.Unmarshal(raw, &part) != nil {
		return invalid(ProtocolResponses, "$.part", "valid content part is required")
	}
	if part.Type != "output_text" && part.Type != "refusal" {
		return unsupported(ProtocolResponses, "$.part.type", "content part %q cannot be represented by Gemini", part.Type)
	}
	_, err := decodeResponsesContent([]responsesContentPart{part}, "$.part", false)
	return err
}

func (c *responsesToGeminiStreamConverter) validateOutputItemDone(itemID string, item responsesItem) error {
	if item.ID == "" {
		return invalid(ProtocolResponses, "$.item.id", "completed output item id is required")
	}
	if itemID != "" && itemID != item.ID {
		return invalid(ProtocolResponses, "$.item_id", "event item_id %q does not match item id %q", itemID, item.ID)
	}
	if err := validateResponsesItems([]responsesItem{item}, "$.output"); err != nil {
		return err
	}
	if item.Type == "reasoning" {
		return unsupported(ProtocolResponses, "$.item.type", "Responses reasoning cannot be represented as a signed Gemini thought")
	}
	if item.Type == "function_call" && !jsonValuePresent(item.Arguments) {
		return invalid(ProtocolResponses, "$.item.arguments", "completed function_call arguments are required")
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

func (c *responsesToGeminiStreamConverter) terminalOutputChunks(response responsesResponse) ([]streamFrame, []Diagnostic, error) {
	var frames []streamFrame
	var diagnostics []Diagnostic
	remainingText, remainingRefusal := c.emittedText, c.emittedRefusal
	terminalItems := make(map[string]responsesItem, len(response.Output))
	for index, item := range response.Output {
		path := fmt.Sprintf("$.response.output[%d]", index)
		if item.ID != "" {
			if _, duplicate := terminalItems[item.ID]; duplicate {
				return nil, diagnostics, upstreamResponseError(ProtocolResponses, path+".id", "duplicate terminal output item id %q", item.ID)
			}
			terminalItems[item.ID] = item
		}
		if added, ok := c.items[item.ID]; ok {
			if added.Type != item.Type || added.Role != item.Role || added.CallID != item.CallID || added.Name != item.Name {
				return nil, diagnostics, upstreamResponseError(ProtocolResponses, path, "terminal output item identity does not match output_item.added")
			}
		}
		switch item.Type {
		case "message":
			parts, err := decodeResponsesContentRaw(item.Content, path+".content", false)
			if err != nil {
				return nil, diagnostics, err
			}
			for _, part := range parts {
				if part.Kind == partRefusal {
					if c.LossPolicy == rejectSemanticLoss {
						return nil, diagnostics, unsupported(ProtocolResponses, path+".content", "Responses refusal content has no Gemini equivalent")
					}
					suffix, rest, err := consumeStreamPrefix(remainingRefusal, part.Text, path+".content")
					if err != nil {
						return nil, diagnostics, err
					}
					remainingRefusal = rest
					if suffix != "" {
						frames = append(frames, streamFrame{Data: c.chunk([]geminiPart{{Text: suffix}}, "", nil)})
						c.emittedRefusal += suffix
					}
					diagnostics = appendDiagnostic(diagnostics, "warning", "responses_refusal_approximated", path+".content", "Responses refusal content was emitted as Gemini text")
				} else {
					suffix, rest, err := consumeStreamPrefix(remainingText, part.Text, path+".content")
					if err != nil {
						return nil, diagnostics, err
					}
					remainingText = rest
					if suffix != "" {
						frames = append(frames, streamFrame{Data: c.chunk([]geminiPart{{Text: suffix}}, "", nil)})
						c.emittedText += suffix
					}
				}
			}
		case "function_call":
			if !jsonValuePresent(item.Arguments) {
				return nil, diagnostics, upstreamResponseError(ProtocolResponses, path+".arguments", "terminal function_call arguments are required")
			}
			arguments, err := normalizeOpenAIToolArguments(ProtocolResponses, path+".arguments", item.Arguments)
			if err != nil {
				return nil, diagnostics, err
			}
			if c.completedItems[item.ID] {
				if completed := c.completedArguments[item.ID]; completed != string(arguments) {
					return nil, diagnostics, upstreamResponseError(ProtocolResponses, path+".arguments", "terminal arguments do not match the completed output item")
				}
				continue
			}
			call := &geminiFunctionCall{ID: item.CallID, Name: item.Name, Args: arguments}
			if pending := c.calls[item.ID]; pending != nil {
				call.ID, call.Name = pending.ID, pending.Name
			}
			if call.ID == "" || call.Name == "" {
				return nil, diagnostics, upstreamResponseError(ProtocolResponses, path, "function_call item is missing call_id or name")
			}
			frames = append(frames, streamFrame{Data: c.chunk([]geminiPart{{FunctionCall: call, ThoughtSignature: geminiThoughtSignatureBypass}}, "", nil)})
			diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_thought_signature_bypass_added", path, "a Gemini-compatible thought signature bypass was attached to the converted function call")
			delete(c.calls, item.ID)
			c.completedItems[item.ID] = true
		case "reasoning":
			return nil, diagnostics, unsupported(ProtocolResponses, path, "Responses reasoning cannot be represented as a signed Gemini thought")
		default:
			return nil, diagnostics, unsupported(ProtocolResponses, path+".type", "output item %q cannot be represented by Gemini", item.Type)
		}
	}
	for itemID, streamed := range c.items {
		terminal, exists := terminalItems[itemID]
		if !exists {
			return nil, diagnostics, upstreamResponseError(ProtocolResponses, "$.response.output", "terminal output is missing streamed item %q", itemID)
		}
		if terminal.Type != streamed.Type {
			return nil, diagnostics, upstreamResponseError(ProtocolResponses, "$.response.output", "terminal item %q changed type from %q to %q", itemID, streamed.Type, terminal.Type)
		}
	}
	if remainingText != "" || remainingRefusal != "" {
		return nil, diagnostics, upstreamResponseError(ProtocolResponses, "$.response.output", "terminal output is missing content previously emitted as deltas")
	}
	return frames, diagnostics, nil
}

func (c *responsesToGeminiStreamConverter) Finalize(context.Context) ([]streamFrame, []Diagnostic, error) {
	if c.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", ErrInvalidPlan)
	}
	c.finalized = true
	if !c.completed {
		return nil, nil, invalid(ProtocolResponses, "$", "stream ended before a terminal response event")
	}
	return nil, nil, nil
}

func (c *responsesToGeminiStreamConverter) chunk(parts []geminiPart, finish string, usage *struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	InputTokenDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokenDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}) []byte {
	content := map[string]any{"role": "model"}
	if len(parts) > 0 {
		content["parts"] = parts
	}
	candidate := map[string]any{"content": content}
	if finish != "" {
		candidate["finishReason"] = finish
	}
	response := map[string]any{"responseId": c.id, "modelVersion": c.model, "candidates": []any{candidate}}
	if usage != nil {
		candidateTokens := usage.OutputTokens - usage.OutputTokenDetails.ReasoningTokens
		if candidateTokens < 0 {
			candidateTokens = 0
		}
		response["usageMetadata"] = map[string]any{
			"promptTokenCount": usage.InputTokens, "candidatesTokenCount": candidateTokens, "totalTokenCount": usage.TotalTokens,
			"cachedContentTokenCount": usage.InputTokenDetails.CachedTokens, "thoughtsTokenCount": usage.OutputTokenDetails.ReasoningTokens,
		}
	}
	return mustJSON(response)
}
