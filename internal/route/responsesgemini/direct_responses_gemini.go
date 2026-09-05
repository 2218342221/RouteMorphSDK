package responsesgemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type responsesToGeminiConverter struct {
	spec routeSpec
}

func (c *responsesToGeminiConverter) Specification() routeSpec { return c.spec }

func (c *responsesToGeminiConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownCrossTopLevel(ProtocolResponses, input); err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesReasoningConfig(ProtocolResponses, input); err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesTextConfigFields(ProtocolResponses, input); err != nil {
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
	if err := validateResponsesTools(source.Tools, "$.tools"); err != nil {
		return conversionResult{}, err
	}
	target := geminiRequest{}
	var diagnostics []Diagnostic
	if source.ParallelToolCalls != nil {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolResponses, "$.parallel_tool_calls", "Gemini does not expose an equivalent parallel tool-call control")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "parallel_tool_calls_not_representable", "$.parallel_tool_calls", "Responses parallel_tool_calls was omitted")
	}
	if source.MaxOutputTokens != nil || source.Temperature != nil || source.TopP != nil || source.Reasoning != nil || len(source.Text) > 0 {
		target.GenerationConfig = &geminiGenerationConfig{MaxOutputTokens: source.MaxOutputTokens, Temperature: source.Temperature, TopP: source.TopP}
	}
	if source.Reasoning != nil {
		if source.Reasoning.Summary != "" {
			return conversionResult{}, unsupported(ProtocolResponses, "$.reasoning.summary", "Gemini cannot preserve the requested Responses reasoning-summary policy")
		}
		level, err := normalizeGeminiThinkingLevel(ProtocolResponses, "$.reasoning.effort", source.Reasoning.Effort)
		if err != nil {
			return conversionResult{}, err
		}
		if level != "" {
			target.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{ThinkingLevel: level}
		}
	}
	var textEnvelope struct {
		Format struct {
			Type string `json:"type"`
		} `json:"format"`
	}
	if jsonValuePresent(source.Text) && json.Unmarshal(source.Text, &textEnvelope) != nil {
		return conversionResult{}, invalid(ProtocolResponses, "$.text", "invalid text format")
	}
	var format *jsonSchemaFormat
	var verbosity json.RawMessage
	var err error
	if textEnvelope.Format.Type == "json_object" {
		target.GenerationConfig.ResponseMIMEType = "application/json"
	} else {
		format, verbosity, err = decodeResponsesTextOptions(source.Text)
		if err != nil {
			return conversionResult{}, err
		}
	}
	if rawJSONValuePresent(verbosity) {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolResponses, "$.text.verbosity", "Gemini has no equivalent response verbosity control")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "text_verbosity_not_representable", "$.text.verbosity", "Responses text verbosity was omitted")
	}
	if format != nil {
		if format.Name != "" || format.Description != "" || format.Strict != nil {
			if options.LossPolicy == rejectSemanticLoss {
				return conversionResult{}, unsupported(ProtocolResponses, "$.text.format", "Gemini can preserve the JSON schema but not its name, description, or strict flag")
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "json_schema_metadata_not_representable", "$.text.format", "JSON schema name, description, and strict flag were omitted")
		}
		target.GenerationConfig.ResponseMIMEType = "application/json"
		schema, err := normalizeGeminiJSONSchema(ProtocolResponses, "$.text.format.schema", format.Schema)
		if err != nil {
			return conversionResult{}, err
		}
		target.GenerationConfig.ResponseJSONSchema = schema
	}
	if len(source.Instructions) > 0 && string(source.Instructions) != "null" {
		parts, err := decodeResponsesInstructions(source.Instructions)
		if err != nil {
			return conversionResult{}, err
		}
		converted, err := encodeGeminiParts(parts)
		if err != nil {
			return conversionResult{}, err
		}
		target.SystemInstruction = &geminiContent{Parts: converted}
	}
	if len(source.Tools) > 0 {
		tool := geminiTool{}
		for index, function := range source.Tools {
			if function.Type != "function" {
				return conversionResult{}, unsupported(ProtocolResponses, fmt.Sprintf("$.tools[%d].type", index), "built-in tool %q requires a native Responses provider", function.Type)
			}
			if function.Strict != nil {
				if options.LossPolicy == rejectSemanticLoss {
					return conversionResult{}, unsupported(ProtocolResponses, fmt.Sprintf("$.tools[%d].strict", index), "Gemini function declarations have no equivalent strict flag")
				}
				diagnostics = appendDiagnostic(diagnostics, "warning", "function_strict_not_representable", fmt.Sprintf("$.tools[%d].strict", index), "Responses strict tool mode was omitted")
			}
			parameters, err := normalizeGeminiFunctionSchema(ProtocolResponses, fmt.Sprintf("$.tools[%d].parameters", index), function.Parameters)
			if err != nil {
				return conversionResult{}, err
			}
			outputSchema, err := normalizeGeminiJSONSchema(ProtocolResponses, fmt.Sprintf("$.tools[%d].output_schema", index), function.OutputSchema)
			if err != nil {
				return conversionResult{}, err
			}
			tool.FunctionDeclarations = append(tool.FunctionDeclarations, geminiFunctionDeclaration{
				Name:                 function.Name,
				Description:          function.Description,
				ParametersJSONSchema: parameters,
				ResponseJSONSchema:   outputSchema,
			})
		}
		target.Tools = []geminiTool{tool}
	}
	choice, err := decodeResponsesToolChoice(source.ToolChoice)
	if err != nil {
		return conversionResult{}, err
	}
	if choice.Mode == toolChoiceRequired && len(source.Tools) == 0 {
		return conversionResult{}, invalid(ProtocolResponses, "$.tool_choice", "required tool choice requires at least one declared function")
	}
	if choice.Mode == toolChoiceNamed {
		declared := false
		for _, tool := range source.Tools {
			declared = declared || (tool.Type == "function" && tool.Name == choice.Name)
		}
		if !declared {
			return conversionResult{}, invalid(ProtocolResponses, "$.tool_choice.name", "named function %q is not declared in tools", choice.Name)
		}
	} else if choice.Mode == toolChoiceAllowed {
		for index, name := range choice.AllowedNames {
			declared := false
			for _, tool := range source.Tools {
				declared = declared || (tool.Type == "function" && tool.Name == name)
			}
			if !declared {
				return conversionResult{}, invalid(ProtocolResponses, fmt.Sprintf("$.tool_choice.tools[%d].name", index), "allowed function %q is not declared in tools", name)
			}
		}
		if choice.AllowedMode == toolChoiceAuto {
			if len(choice.AllowedNames) != len(source.Tools) {
				return conversionResult{}, unsupported(ProtocolResponses, "$.tool_choice.tools", "Gemini cannot preserve AUTO restricted to a strict subset of functions")
			}
			choice = toolChoice{Mode: toolChoiceAuto}
		}
	}
	if choice.Mode != "" {
		target.ToolConfig = &geminiToolConfig{}
		switch choice.Mode {
		case toolChoiceAuto:
			target.ToolConfig.FunctionCallingConfig.Mode = "AUTO"
		case toolChoiceNone:
			target.ToolConfig.FunctionCallingConfig.Mode = "NONE"
		case toolChoiceRequired:
			target.ToolConfig.FunctionCallingConfig.Mode = "ANY"
		case toolChoiceNamed:
			target.ToolConfig.FunctionCallingConfig.Mode = "ANY"
			target.ToolConfig.FunctionCallingConfig.AllowedFunctionNames = []string{choice.Name}
		case toolChoiceAllowed:
			target.ToolConfig.FunctionCallingConfig.Mode = "ANY"
			target.ToolConfig.FunctionCallingConfig.AllowedFunctionNames = append([]string(nil), choice.AllowedNames...)
		}
	}
	items, err := responseInputItems(source.Input)
	if err != nil {
		return conversionResult{}, err
	}
	callNames := make(map[string]string)
	callIDsByName := make(map[string][]string)
	consumedCallIDs := make(map[string]bool)
	for index, item := range items {
		path := fmt.Sprintf("$.input[%d]", index)
		switch item.Type {
		case "message", "":
			parts, err := decodeResponsesContentRaw(item.Content, path+".content", true)
			if err != nil {
				return conversionResult{}, err
			}
			if item.Role == "system" || item.Role == "developer" {
				for partIndex, part := range parts {
					if part.Kind != partText {
						return conversionResult{}, unsupported(ProtocolResponses, fmt.Sprintf("%s.content[%d]", path, partIndex), "Gemini systemInstruction supports text only")
					}
				}
			}
			converted, err := encodeGeminiParts(parts)
			if err != nil {
				return conversionResult{}, err
			}
			role := "user"
			if item.Role == "assistant" {
				role = "model"
			} else if item.Role == "system" || item.Role == "developer" {
				if len(target.Contents) > 0 {
					return conversionResult{}, unsupported(ProtocolResponses, path+".role", "interleaved system/developer messages cannot be moved to Gemini systemInstruction")
				}
				if target.SystemInstruction == nil {
					target.SystemInstruction = &geminiContent{}
				}
				target.SystemInstruction.Parts = append(target.SystemInstruction.Parts, converted...)
				continue
			} else if item.Role != "user" {
				return conversionResult{}, unsupported(ProtocolResponses, path+".role", "role %q cannot be represented by Gemini", item.Role)
			}
			if len(converted) == 0 {
				return conversionResult{}, invalid(ProtocolResponses, path+".content", "at least one portable content part is required")
			}
			appendGeminiContent(&target, role, converted...)
		case "function_call":
			arguments, err := normalizeGeminiToolArguments(ProtocolResponses, path+".arguments", item.Arguments)
			if err != nil {
				return conversionResult{}, err
			}
			if _, exists := callNames[item.CallID]; exists {
				return conversionResult{}, invalid(ProtocolResponses, path+".call_id", "duplicate call_id %q", item.CallID)
			}
			callNames[item.CallID] = item.Name
			callIDsByName[item.Name] = append(callIDsByName[item.Name], item.CallID)
			appendGeminiContent(&target, "model", geminiPart{FunctionCall: &geminiFunctionCall{ID: item.CallID, Name: item.Name, Args: arguments}})
			diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_thought_signature_unavailable", path, "the source protocol cannot provide a provider-issued Gemini thoughtSignature; Gemini 3 may reject replayed function-call history")
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
			name := callNames[callID]
			if name == "" {
				return conversionResult{}, unsupported(ProtocolResponses, path+".call_id", "Gemini functionResponse requires the corresponding function name")
			}
			if item.Name != "" && item.Name != name {
				return conversionResult{}, invalid(ProtocolResponses, path+".name", "function output name %q does not match call name %q", item.Name, name)
			}
			if consumedCallIDs[callID] {
				return conversionResult{}, invalid(ProtocolResponses, path+".call_id", "function call %q already has an output", callID)
			}
			consumedCallIDs[callID] = true
			if !jsonValuePresent(item.Output) {
				return conversionResult{}, invalid(ProtocolResponses, path+".output", "output is required")
			}
			response, responseParts, err := responsesFunctionOutputToGemini(item.Output, path+".output")
			if err != nil {
				return conversionResult{}, err
			}
			appendGeminiContent(&target, "user", geminiPart{FunctionResponse: &geminiFunctionResponse{ID: callID, Name: name, Response: response, Parts: responseParts}})
		case "reasoning":
			return conversionResult{}, unsupported(ProtocolResponses, path, "reasoning items cannot be injected as signed Gemini thoughts")
		default:
			return conversionResult{}, unsupported(ProtocolResponses, path+".type", "input item %q requires a native Responses provider", item.Type)
		}
	}
	if len(target.Contents) == 0 {
		return conversionResult{}, invalid(ProtocolResponses, "$.input", "Gemini requires at least one non-system content item")
	}
	body, err := marshal(ProtocolGenerateContent, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func responsesFunctionOutputToGemini(raw json.RawMessage, path string) (json.RawMessage, []geminiFunctionResponsePart, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil, invalid(ProtocolResponses, path, "output is required")
	}
	if trimmed[0] != '[' {
		return mustJSON(map[string]any{"output": rawString(raw)}), nil, nil
	}

	parts, err := decodeResponsesContentRaw(trimmed, path, true)
	if err != nil {
		return nil, nil, err
	}
	if len(parts) == 0 {
		return json.RawMessage(`{}`), nil, nil
	}
	if len(parts) == 1 && parts[0].Kind == partText {
		return mustJSON(map[string]any{"output": parts[0].Text}), nil, nil
	}
	allText := true
	var text strings.Builder
	for _, part := range parts {
		if part.Kind != partText {
			allText = false
			break
		}
		text.WriteString(part.Text)
	}
	if allText {
		return mustJSON(map[string]any{"output": text.String()}), nil, nil
	}
	converted := make([]geminiFunctionResponsePart, 0, len(parts))
	for index, part := range parts {
		partPath := fmt.Sprintf("%s[%d]", path, index)
		if part.Kind == partText {
			return nil, nil, unsupported(ProtocolResponses, partPath, "Gemini functionResponse cannot preserve text ordering within a multimodal output list")
		}
		if part.Kind != partImage && part.Kind != partFile {
			return nil, nil, unsupported(ProtocolResponses, partPath, "only inline image and PDF function outputs are portable to Gemini")
		}
		if part.Media == nil {
			return nil, nil, invalid(ProtocolResponses, partPath, "media is required")
		}
		if jsonValuePresent(part.PromptCacheBreakpoint) {
			return nil, nil, unsupported(ProtocolResponses, partPath+".prompt_cache_breakpoint", "Gemini functionResponse.parts cannot preserve prompt cache breakpoints")
		}
		if part.Media.Detail != "" {
			return nil, nil, unsupported(ProtocolResponses, partPath+".detail", "Gemini functionResponse.parts cannot preserve media detail")
		}
		if part.Media.Filename != "" {
			return nil, nil, unsupported(ProtocolResponses, partPath+".filename", "Gemini functionResponse.parts cannot preserve file names")
		}
		if part.Media.FileID != "" {
			return nil, nil, unsupported(ProtocolResponses, partPath+".file_id", "provider-scoped file IDs cannot be represented by Gemini functionResponse.parts")
		}
		if part.Media.URL != "" {
			field := ".file_url"
			if part.Kind == partImage {
				field = ".image_url"
			}
			return nil, nil, unsupported(ProtocolResponses, partPath+field, "Gemini Developer API functionResponse.parts supports inlineData only")
		}
		if part.Media.Data == "" {
			return nil, nil, invalid(ProtocolResponses, partPath, "inline media data is required")
		}
		mimeType := strings.ToLower(part.Media.MIMEType)
		if part.Kind == partImage {
			if !validImageMIMEType(mimeType) {
				return nil, nil, unsupported(ProtocolResponses, partPath, "Gemini functionResponse image output requires JPEG, PNG, GIF, or WebP")
			}
		} else if mimeType != "application/pdf" {
			return nil, nil, unsupported(ProtocolResponses, partPath, "only inline PDF file outputs are portable to Gemini functionResponse.parts")
		}
		converted = append(converted, geminiFunctionResponsePart{InlineData: &geminiBlob{MIMEType: part.Media.MIMEType, Data: part.Media.Data}})
	}
	return json.RawMessage(`{}`), converted, nil
}

func (c *responsesToGeminiConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source geminiResponse
	if err := decodeJSON(ProtocolGenerateContent, input, &source); err != nil {
		return conversionResult{}, err
	}
	diagnostics, err := validateGeminiResponseEnvelope(&source, options.LossPolicy)
	if err != nil {
		return conversionResult{}, err
	}
	target := responsesResponse{ID: source.ResponseID, Object: "response", Model: source.ModelVersion, Status: "completed"}
	if options.Exchange.ClientModel != "" {
		target.Model = options.Exchange.ClientModel
	}
	var messageParts []portablePart
	messageSequence := 0
	flushMessage := func() error {
		if len(messageParts) == 0 {
			return nil
		}
		content, err := encodeResponsesContent(messageParts, false)
		if err != nil {
			return err
		}
		messageID := "msg_" + source.ResponseID
		if messageSequence > 0 {
			messageID = fmt.Sprintf("%s_%d", messageID, messageSequence)
		}
		messageSequence++
		target.Output = append(target.Output, responsesItem{Type: "message", ID: messageID, Role: "assistant", Content: mustJSON(content), Status: "completed"})
		messageParts = nil
		return nil
	}
	reservedCallIDs, err := reserveGeminiResponseCallIDs(source.Candidates[0].Content.Parts, nil, "$.candidates[0].content.parts")
	if err != nil {
		return conversionResult{}, err
	}
	generatedCallID := 0
	for index, part := range source.Candidates[0].Content.Parts {
		path := fmt.Sprintf("$.candidates[0].content.parts[%d]", index)
		if err := validateGeminiPart(part, path); err != nil {
			return conversionResult{}, err
		}
		switch {
		case part.FunctionCall != nil:
			if err := flushMessage(); err != nil {
				return conversionResult{}, err
			}
			if part.ThoughtSignature != "" {
				return conversionResult{}, unsupported(ProtocolGenerateContent, path+".thoughtSignature", "provider-issued Gemini function-call signatures cannot be represented by Responses and must not be dropped")
			}
			arguments, err := normalizeGeminiToolArguments(ProtocolGenerateContent, path+".functionCall.args", part.FunctionCall.Args)
			if err != nil {
				return conversionResult{}, err
			}
			callID := part.FunctionCall.ID
			if callID == "" {
				callID = nextGeneratedGeminiCallID(&generatedCallID, reservedCallIDs)
				diagnostics = appendDiagnostic(diagnostics, "warning", "generated_function_call_id", path+".functionCall.id", "Gemini omitted the optional function call id; RouteMorph generated a response-local id")
			}
			target.Output = append(target.Output, responsesItem{Type: "function_call", ID: "fc_" + callID, CallID: callID, Name: part.FunctionCall.Name, Arguments: json.RawMessage(mustJSONString(string(arguments))), Status: "completed"})
		case part.Thought:
			if err := flushMessage(); err != nil {
				return conversionResult{}, err
			}
			if part.ThoughtSignature != "" {
				return conversionResult{}, unsupported(ProtocolGenerateContent, path+".thoughtSignature", "provider-issued Gemini thought signatures cannot be represented by Responses and must not be dropped")
			}
			target.Output = append(target.Output, responsesItem{
				Type: "reasoning", ID: fmt.Sprintf("rs_%d", index), Status: "completed",
				Summary: mustJSON([]responsesContentPart{}),
				Content: mustJSON([]responsesContentPart{{Type: "reasoning_text", Text: part.Text}}),
			})
		case part.FunctionResponse != nil:
			return conversionResult{}, unsupported(ProtocolGenerateContent, path, "functionResponse is not a valid model output item")
		default:
			parts, err := decodeGeminiParts([]geminiPart{part}, path)
			if err != nil {
				return conversionResult{}, err
			}
			for _, portable := range parts {
				if portable.Kind != partText {
					return conversionResult{}, unsupported(ProtocolGenerateContent, path, "non-text Gemini model output cannot be represented by Responses message output")
				}
				messageParts = append(messageParts, portable)
			}
		}
	}
	if err := flushMessage(); err != nil {
		return conversionResult{}, err
	}
	finish, err := parseGeminiFinish(source.Candidates[0].FinishReason)
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
	} else if finish == finishError {
		target.Status = "failed"
	}
	target.Usage.InputTokens = source.UsageMetadata.PromptTokenCount + source.UsageMetadata.ToolUsePromptTokenCount
	target.Usage.OutputTokens = source.UsageMetadata.CandidatesTokenCount + source.UsageMetadata.ThoughtsTokenCount
	target.Usage.TotalTokens = source.UsageMetadata.TotalTokenCount
	target.Usage.InputTokenDetails.CachedTokens = source.UsageMetadata.CachedContentTokenCount
	target.Usage.OutputTokenDetails.ReasoningTokens = source.UsageMetadata.ThoughtsTokenCount
	body, err := marshal(ProtocolResponses, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *responsesToGeminiConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return &geminiToResponsesStreamConverter{clientModel: options.Exchange.ClientModel, LossPolicy: options.LossPolicy}, nil
}

type geminiToResponsesStreamConverter struct {
	id, model                                                             string
	providerID, providerModel                                             string
	clientModel                                                           string
	LossPolicy                                                            lossPolicy
	sequence                                                              int
	messageStarted                                                        bool
	messageOutputIndex                                                    int
	messageID                                                             string
	messageText                                                           string
	nextMessage                                                           int
	reasoningStarted                                                      bool
	reasoningOutputIndex                                                  int
	reasoningID                                                           string
	reasoningText                                                         string
	nextReasoning                                                         int
	output                                                                []responsesItem
	generatedCallID                                                       int
	callIDs                                                               map[string]struct{}
	sourceToOutputCallIDs                                                 map[string]string
	created                                                               bool
	completed                                                             bool
	finalized                                                             bool
	sawUsage                                                              bool
	pendingStatus                                                         string
	pendingIncompleteReason                                               string
	inputTokens, outputTokens, totalTokens, cachedTokens, reasoningTokens int64
}

func (c *geminiToResponsesStreamConverter) Convert(ctx context.Context, frame streamFrame) ([]streamFrame, []Diagnostic, error) {
	if c.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", ErrInvalidPlan)
	}
	if c.completed {
		return nil, nil, invalid(ProtocolGenerateContent, "$", "stream chunk arrived after terminal response")
	}
	trimmed := bytes.TrimSpace(frame.Data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var chunks []json.RawMessage
		if err := json.Unmarshal(trimmed, &chunks); err != nil {
			return nil, nil, invalid(ProtocolGenerateContent, "$", "invalid JSON-array stream: %v", err)
		}
		var frames []streamFrame
		var diagnostics []Diagnostic
		for _, chunk := range chunks {
			converted, chunkDiagnostics, err := c.Convert(ctx, streamFrame{Data: chunk})
			if err != nil {
				return nil, diagnostics, err
			}
			frames = append(frames, converted...)
			diagnostics = append(diagnostics, chunkDiagnostics...)
		}
		return frames, diagnostics, nil
	}
	var source geminiResponse
	if err := decodeJSON(ProtocolGenerateContent, frame.Data, &source); err != nil {
		return nil, nil, err
	}
	if err := normalizeGeminiTerminalEmptyTextParts(frame.Data, &source); err != nil {
		return nil, nil, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(frame.Data, &envelope); err != nil {
		return nil, nil, invalid(ProtocolGenerateContent, "$", "invalid stream chunk: %v", err)
	}
	if err := c.lockProviderIdentity(source.ResponseID, source.ModelVersion); err != nil {
		return nil, nil, err
	}
	hasUsage := jsonValuePresent(envelope["usageMetadata"])
	if hasUsage {
		c.sawUsage = true
		c.inputTokens = source.UsageMetadata.PromptTokenCount + source.UsageMetadata.ToolUsePromptTokenCount
		c.outputTokens = source.UsageMetadata.CandidatesTokenCount + source.UsageMetadata.ThoughtsTokenCount
		c.totalTokens = source.UsageMetadata.TotalTokenCount
		c.cachedTokens = source.UsageMetadata.CachedContentTokenCount
		c.reasoningTokens = source.UsageMetadata.ThoughtsTokenCount
	}
	if c.pendingStatus != "" {
		if len(source.Candidates) != 0 || !hasUsage {
			return nil, nil, invalid(ProtocolGenerateContent, "$", "only a usage-only chunk may follow a terminal Gemini candidate")
		}
		return c.completePending(), nil, nil
	}
	diagnostics, err := validateGeminiResponseEnvelope(&source, c.LossPolicy)
	if err != nil {
		return nil, nil, err
	}
	if !c.created {
		c.selectOutputIdentity()
	}
	var frames []streamFrame
	if !c.created {
		c.created = true
		response := c.response("in_progress", nil)
		frames = append(frames, c.event("response.created", map[string]any{"response": response}))
	}
	candidate := source.Candidates[0]
	outputCallIDs, err := c.planGeminiStreamCallIDs(candidate.Content.Parts, "$.candidates[0].content.parts")
	if err != nil {
		return nil, diagnostics, err
	}
	for index, part := range candidate.Content.Parts {
		path := fmt.Sprintf("$.candidates[0].content.parts[%d]", index)
		if err := validateGeminiPart(part, path); err != nil {
			return nil, diagnostics, err
		}
		switch {
		case part.Text != "" && !part.Thought:
			if part.ThoughtSignature != "" {
				return nil, diagnostics, unsupported(ProtocolGenerateContent, path+".thoughtSignature", "signed text cannot be represented by Responses")
			}
			frames = append(frames, c.finishReasoning()...)
			if !c.messageStarted {
				frames = append(frames, c.startMessage()...)
			}
			c.messageText += part.Text
			frames = append(frames, c.event("response.output_text.delta", map[string]any{"item_id": c.messageID, "output_index": c.messageOutputIndex, "content_index": 0, "delta": part.Text, "logprobs": []any{}}))
		case part.FunctionCall != nil:
			if part.ThoughtSignature != "" {
				return nil, diagnostics, unsupported(ProtocolGenerateContent, path+".thoughtSignature", "provider-issued Gemini function-call signatures cannot be represented by Responses and must not be dropped")
			}
			arguments, err := normalizeGeminiToolArguments(ProtocolGenerateContent, path+".functionCall.args", part.FunctionCall.Args)
			if err != nil {
				return nil, diagnostics, err
			}
			callID := outputCallIDs[index]
			if part.FunctionCall.ID == "" {
				diagnostics = appendDiagnostic(diagnostics, "warning", "generated_function_call_id", path+".functionCall.id", "Gemini omitted the optional function call id; RouteMorph generated a response-local id")
			} else if callID != part.FunctionCall.ID {
				diagnostics = appendDiagnostic(diagnostics, "warning", "remapped_function_call_id", path+".functionCall.id", "Gemini function call id collided with an earlier generated Responses call id; RouteMorph remapped it for stable correlation")
			}
			frames = append(frames, c.finishMessage()...)
			frames = append(frames, c.finishReasoning()...)
			item := responsesItem{Type: "function_call", ID: "fc_" + callID, CallID: callID, Name: part.FunctionCall.Name, Arguments: json.RawMessage(mustJSONString(string(arguments))), Status: "completed"}
			outputIndex := len(c.output)
			frames = append(frames,
				c.event("response.output_item.added", map[string]any{"output_index": outputIndex, "item": map[string]any{"id": item.ID, "type": item.Type, "call_id": item.CallID, "name": item.Name, "arguments": "", "status": "in_progress"}}),
				c.event("response.function_call_arguments.delta", map[string]any{"item_id": item.ID, "output_index": outputIndex, "delta": string(arguments)}),
				c.event("response.function_call_arguments.done", map[string]any{"item_id": item.ID, "output_index": outputIndex, "name": item.Name, "arguments": string(arguments)}),
				c.event("response.output_item.done", map[string]any{"output_index": outputIndex, "item": item}),
			)
			c.output = append(c.output, item)
		case part.Thought:
			if part.ThoughtSignature != "" {
				return nil, diagnostics, unsupported(ProtocolGenerateContent, path+".thoughtSignature", "provider-issued Gemini thought signatures cannot be represented by Responses and must not be dropped")
			}
			frames = append(frames, c.finishMessage()...)
			if !c.reasoningStarted {
				frames = append(frames, c.startReasoning()...)
			}
			c.reasoningText += part.Text
			frames = append(frames, c.event("response.reasoning_text.delta", map[string]any{"item_id": c.reasoningID, "output_index": c.reasoningOutputIndex, "content_index": 0, "delta": part.Text}))
		default:
			return nil, diagnostics, unsupported(ProtocolGenerateContent, path, "Gemini stream output part is not representable by Responses")
		}
	}
	if candidate.FinishReason == "" {
		return frames, diagnostics, nil
	}
	finish, err := parseGeminiFinish(candidate.FinishReason)
	if err != nil {
		return nil, diagnostics, err
	}
	frames = append(frames, c.finishMessage()...)
	frames = append(frames, c.finishReasoning()...)
	status := "completed"
	var incomplete *struct {
		Reason string `json:"reason"`
	}
	if finish == finishLength || finish == finishContentFilter {
		status = "incomplete"
		reason := "max_output_tokens"
		if finish == finishContentFilter {
			reason = "content_filter"
		}
		incomplete = &struct {
			Reason string `json:"reason"`
		}{Reason: reason}
	}
	c.pendingStatus = status
	if incomplete != nil {
		c.pendingIncompleteReason = incomplete.Reason
	}
	if c.sawUsage {
		frames = append(frames, c.completePending()...)
	}
	return frames, diagnostics, nil
}

func reserveGeminiResponseCallIDs(parts []geminiPart, existing map[string]struct{}, path string) (map[string]struct{}, error) {
	reserved := make(map[string]struct{}, len(existing)+len(parts))
	for callID := range existing {
		reserved[callID] = struct{}{}
	}
	for index, part := range parts {
		if part.FunctionCall == nil || part.FunctionCall.ID == "" {
			continue
		}
		callID := part.FunctionCall.ID
		if _, duplicate := reserved[callID]; duplicate {
			return nil, upstreamResponseError(ProtocolGenerateContent, fmt.Sprintf("%s[%d].functionCall.id", path, index), "duplicate function call id %q", callID)
		}
		reserved[callID] = struct{}{}
	}
	return reserved, nil
}

// planGeminiStreamCallIDs keeps provider IDs and emitted Responses IDs in
// separate namespaces. A generated ID may have been emitted before a later
// Gemini chunk reveals the same explicit provider ID; in that case the
// explicit ID is assigned a new output ID and retained in sourceToOutputCallIDs
// so correlation remains stable. Repeating an explicit provider ID is still an
// invalid upstream response.
func (c *geminiToResponsesStreamConverter) planGeminiStreamCallIDs(parts []geminiPart, path string) (map[int]string, error) {
	if c.callIDs == nil {
		c.callIDs = make(map[string]struct{})
	}
	if c.sourceToOutputCallIDs == nil {
		c.sourceToOutputCallIDs = make(map[string]string)
	}

	currentSourceIDs := make(map[string]struct{})
	reserved := make(map[string]struct{}, len(c.callIDs)+len(parts))
	for callID := range c.callIDs {
		reserved[callID] = struct{}{}
	}
	for index, part := range parts {
		if part.FunctionCall == nil || part.FunctionCall.ID == "" {
			continue
		}
		sourceID := part.FunctionCall.ID
		if _, duplicate := c.sourceToOutputCallIDs[sourceID]; duplicate {
			return nil, upstreamResponseError(ProtocolGenerateContent, fmt.Sprintf("%s[%d].functionCall.id", path, index), "duplicate function call id %q", sourceID)
		}
		if _, duplicate := currentSourceIDs[sourceID]; duplicate {
			return nil, upstreamResponseError(ProtocolGenerateContent, fmt.Sprintf("%s[%d].functionCall.id", path, index), "duplicate function call id %q", sourceID)
		}
		currentSourceIDs[sourceID] = struct{}{}
		// Reserve every explicit ID before assigning generated IDs so a
		// missing-ID call in this chunk cannot claim a later explicit ID.
		reserved[sourceID] = struct{}{}
	}

	outputCallIDs := make(map[int]string)
	for index, part := range parts {
		if part.FunctionCall == nil {
			continue
		}
		sourceID := part.FunctionCall.ID
		outputID := sourceID
		if sourceID == "" {
			outputID = nextGeneratedGeminiCallID(&c.generatedCallID, reserved)
		} else {
			if _, collision := c.callIDs[sourceID]; collision {
				outputID = nextGeneratedGeminiCallID(&c.generatedCallID, reserved)
			}
			c.sourceToOutputCallIDs[sourceID] = outputID
		}
		reserved[outputID] = struct{}{}
		c.callIDs[outputID] = struct{}{}
		outputCallIDs[index] = outputID
	}
	return outputCallIDs, nil
}

func nextGeneratedGeminiCallID(counter *int, reserved map[string]struct{}) string {
	for {
		(*counter)++
		callID := fmt.Sprintf("rm_call_%d", *counter)
		if _, exists := reserved[callID]; exists {
			continue
		}
		reserved[callID] = struct{}{}
		return callID
	}
}

func (c *geminiToResponsesStreamConverter) startMessage() []streamFrame {
	c.messageStarted = true
	c.messageOutputIndex = len(c.output)
	c.messageText = ""
	c.nextMessage++
	c.messageID = "msg_" + c.id
	if c.nextMessage > 1 {
		c.messageID = fmt.Sprintf("msg_%s_%d", c.id, c.nextMessage)
	}
	c.output = append(c.output, responsesItem{Type: "message", ID: c.messageID, Role: "assistant", Status: "in_progress"})
	return []streamFrame{
		c.event("response.output_item.added", map[string]any{"output_index": c.messageOutputIndex, "item": map[string]any{"id": c.messageID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}),
		c.event("response.content_part.added", map[string]any{"item_id": c.messageID, "output_index": c.messageOutputIndex, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}),
	}
}

func (c *geminiToResponsesStreamConverter) finishMessage() []streamFrame {
	if !c.messageStarted {
		return nil
	}
	content := []responsesContentPart{{Type: "output_text", Text: c.messageText, Annotations: json.RawMessage(`[]`)}}
	item := responsesItem{Type: "message", ID: c.messageID, Role: "assistant", Content: mustJSON(content), Status: "completed"}
	outputIndex := c.messageOutputIndex
	c.output[outputIndex] = item
	c.messageStarted = false
	c.messageID = ""
	c.messageText = ""
	return []streamFrame{
		c.event("response.output_text.done", map[string]any{"item_id": item.ID, "output_index": outputIndex, "content_index": 0, "text": content[0].Text, "logprobs": []any{}}),
		c.event("response.content_part.done", map[string]any{"item_id": item.ID, "output_index": outputIndex, "content_index": 0, "part": content[0]}),
		c.event("response.output_item.done", map[string]any{"output_index": outputIndex, "item": item}),
	}
}

func (c *geminiToResponsesStreamConverter) startReasoning() []streamFrame {
	c.reasoningStarted = true
	c.reasoningOutputIndex = len(c.output)
	c.reasoningText = ""
	c.nextReasoning++
	c.reasoningID = "rs_" + c.id
	if c.nextReasoning > 1 {
		c.reasoningID = fmt.Sprintf("rs_%s_%d", c.id, c.nextReasoning)
	}
	c.output = append(c.output, responsesItem{Type: "reasoning", ID: c.reasoningID, Status: "in_progress", Summary: mustJSON([]responsesContentPart{}), Content: mustJSON([]responsesContentPart{})})
	return []streamFrame{
		c.event("response.output_item.added", map[string]any{"output_index": c.reasoningOutputIndex, "item": map[string]any{"id": c.reasoningID, "type": "reasoning", "status": "in_progress", "summary": []any{}, "content": []any{}}}),
		c.event("response.content_part.added", map[string]any{"item_id": c.reasoningID, "output_index": c.reasoningOutputIndex, "content_index": 0, "part": map[string]any{"type": "reasoning_text", "text": ""}}),
	}
}

func (c *geminiToResponsesStreamConverter) finishReasoning() []streamFrame {
	if !c.reasoningStarted {
		return nil
	}
	content := []responsesContentPart{{Type: "reasoning_text", Text: c.reasoningText}}
	item := responsesItem{Type: "reasoning", ID: c.reasoningID, Status: "completed", Summary: mustJSON([]responsesContentPart{}), Content: mustJSON(content)}
	outputIndex := c.reasoningOutputIndex
	c.output[outputIndex] = item
	c.reasoningStarted = false
	c.reasoningID = ""
	c.reasoningText = ""
	return []streamFrame{
		c.event("response.reasoning_text.done", map[string]any{"item_id": item.ID, "output_index": outputIndex, "content_index": 0, "text": content[0].Text}),
		c.event("response.content_part.done", map[string]any{"item_id": item.ID, "output_index": outputIndex, "content_index": 0, "part": content[0]}),
		c.event("response.output_item.done", map[string]any{"output_index": outputIndex, "item": item}),
	}
}

func (c *geminiToResponsesStreamConverter) lockProviderIdentity(id, model string) error {
	if id != "" {
		if c.providerID != "" && c.providerID != id {
			return invalid(ProtocolGenerateContent, "$.responseId", "response id changed from %q to %q", c.providerID, id)
		}
		c.providerID = id
	}
	if model != "" {
		if c.providerModel != "" && c.providerModel != model {
			return invalid(ProtocolGenerateContent, "$.modelVersion", "response model changed from %q to %q", c.providerModel, model)
		}
		c.providerModel = model
	}
	return nil
}

func (c *geminiToResponsesStreamConverter) selectOutputIdentity() {
	if c.providerID != "" {
		c.id = c.providerID
	}
	if c.id == "" {
		c.id = "resp_routemorph"
	}
	if c.clientModel != "" {
		c.model = c.clientModel
	} else if c.providerModel != "" {
		c.model = c.providerModel
	}
}

func (c *geminiToResponsesStreamConverter) Finalize(context.Context) ([]streamFrame, []Diagnostic, error) {
	if c.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", ErrInvalidPlan)
	}
	c.finalized = true
	if c.pendingStatus != "" {
		return c.completePending(), nil, nil
	}
	if !c.completed {
		return nil, nil, invalid(ProtocolGenerateContent, "$", "stream ended before a terminal Gemini candidate")
	}
	return nil, nil, nil
}

func (c *geminiToResponsesStreamConverter) completePending() []streamFrame {
	status := c.pendingStatus
	var incomplete *struct {
		Reason string `json:"reason"`
	}
	if c.pendingIncompleteReason != "" {
		incomplete = &struct {
			Reason string `json:"reason"`
		}{Reason: c.pendingIncompleteReason}
	}
	eventType := "response.completed"
	if status == "incomplete" {
		eventType = "response.incomplete"
	}
	c.pendingStatus = ""
	c.pendingIncompleteReason = ""
	c.completed = true
	return []streamFrame{c.event(eventType, map[string]any{"response": c.response(status, incomplete)})}
}

func (c *geminiToResponsesStreamConverter) event(eventType string, fields map[string]any) streamFrame {
	fields["type"] = eventType
	fields["sequence_number"] = c.sequence
	c.sequence++
	return streamFrame{Event: eventType, Data: mustJSON(fields)}
}

func (c *geminiToResponsesStreamConverter) response(status string, incomplete *struct {
	Reason string `json:"reason"`
}) responsesResponse {
	response := responsesResponse{ID: c.id, Object: "response", Model: c.model, Status: status, Output: append([]responsesItem(nil), c.output...), IncompleteDetails: incomplete}
	response.Usage.InputTokens = c.inputTokens
	response.Usage.OutputTokens = c.outputTokens
	response.Usage.TotalTokens = c.totalTokens
	response.Usage.InputTokenDetails.CachedTokens = c.cachedTokens
	response.Usage.OutputTokenDetails.ReasoningTokens = c.reasoningTokens
	return response
}
