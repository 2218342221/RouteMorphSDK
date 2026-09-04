package responsesmessages

import (
	"context"
	"encoding/json"
	"fmt"
)

type responsesToMessagesConverter struct {
	spec     routeSpec
	buffered BufferedFactory
}

func (c *responsesToMessagesConverter) Specification() routeSpec { return c.spec }

func (c *responsesToMessagesConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownCrossTopLevel(ProtocolResponses, input); err != nil {
		return conversionResult{}, err
	}
	var source responsesRequest
	if err := decodeJSON(ProtocolResponses, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Model == "" {
		return conversionResult{}, invalid(ProtocolResponses, "$.model", "model is required")
	}
	if source.MaxOutputTokens != nil && *source.MaxOutputTokens <= 0 {
		return conversionResult{}, invalid(ProtocolResponses, "$.max_output_tokens", "must be greater than zero")
	}
	if err := rejectResponsesState(source); err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesTools(source.Tools, "$.tools"); err != nil {
		return conversionResult{}, err
	}
	maxTokens := 4096
	var diagnostics []Diagnostic
	if source.MaxOutputTokens != nil {
		maxTokens = *source.MaxOutputTokens
	} else {
		diagnostics = appendDiagnostic(diagnostics, "warning", "default_max_tokens", "$.max_tokens", "Messages requires max_tokens; RouteMorph used 4096")
	}
	target := messagesRequest{Model: source.Model, MaxTokens: maxTokens, Temperature: source.Temperature, TopP: source.TopP, Stream: resolveExchangeStream(source.Stream, options.Exchange), Metadata: source.Metadata}
	if options.Exchange.UpstreamModel != "" {
		target.Model = options.Exchange.UpstreamModel
	}
	if source.Reasoning != nil {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolResponses, "$.reasoning", "Responses reasoning is not semantically equivalent to Messages thinking")
		}
		target.Thinking = &messagesThinking{Type: "adaptive"}
		target.OutputConfig = &messagesOutputConfig{Effort: source.Reasoning.Effort}
		diagnostics = appendDiagnostic(diagnostics, "warning", "reasoning_policy_approximated", "$.reasoning", "Responses reasoning was approximated as Messages adaptive thinking")
	}
	var textEnvelope struct {
		Format struct {
			Type string `json:"type"`
		} `json:"format"`
	}
	if jsonValuePresent(source.Text) && json.Unmarshal(source.Text, &textEnvelope) != nil {
		return conversionResult{}, invalid(ProtocolResponses, "$.text", "invalid text format")
	}
	if textEnvelope.Format.Type == "json_object" {
		return conversionResult{}, unsupported(ProtocolResponses, "$.text.format.type", "Messages has no schema-free JSON object response mode")
	}
	format, verbosity, err := decodeResponsesTextOptions(source.Text)
	if err != nil {
		return conversionResult{}, err
	}
	if rawJSONValuePresent(verbosity) {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolResponses, "$.text.verbosity", "Messages has no equivalent response verbosity control")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "text_verbosity_not_representable", "$.text.verbosity", "Responses text verbosity was omitted")
	}
	if format != nil {
		if format.Name != "" || format.Description != "" || format.Strict != nil {
			if options.LossPolicy == rejectSemanticLoss {
				return conversionResult{}, unsupported(ProtocolResponses, "$.text.format", "Messages can preserve the JSON schema but not its name, description, or strict flag")
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "json_schema_metadata_not_representable", "$.text.format", "JSON schema name, description, and strict flag were omitted")
		}
		if target.OutputConfig == nil {
			target.OutputConfig = &messagesOutputConfig{}
		}
		target.OutputConfig.Format = &struct {
			Type   string          `json:"type"`
			Schema json.RawMessage `json:"schema"`
		}{Type: "json_schema", Schema: format.Schema}
	}
	choice, err := decodeResponsesToolChoice(source.ToolChoice)
	if err != nil {
		return conversionResult{}, err
	}
	if choice.Mode != "" || source.ParallelToolCalls != nil {
		target.ToolChoice = encodeMessagesToolChoice(choice, source.ParallelToolCalls)
	}
	for index, tool := range source.Tools {
		if tool.Type != "function" {
			return conversionResult{}, unsupported(ProtocolResponses, fmt.Sprintf("$.tools[%d].type", index), "built-in tool %q requires a native Responses provider", tool.Type)
		}
		schema, err := normalizeMessagesInputSchema(tool.Parameters, fmt.Sprintf("$.tools[%d].parameters", index))
		if err != nil {
			return conversionResult{}, err
		}
		target.Tools = append(target.Tools, messagesTool{Name: tool.Name, Description: tool.Description, InputSchema: schema, Strict: tool.Strict})
	}
	if len(source.Instructions) > 0 && string(source.Instructions) != "null" {
		if source.Instructions[0] != '"' {
			return conversionResult{}, unsupported(ProtocolResponses, "$.instructions", "only string instructions are portable to Messages")
		}
		target.System = mustJSON([]messagesBlock{{Type: "text", Text: rawString(source.Instructions)}})
	}
	items, err := responseInputItems(source.Input)
	if err != nil {
		return conversionResult{}, err
	}
	if len(items) == 0 {
		return conversionResult{}, invalid(ProtocolResponses, "$.input", "at least one input item is required for Messages")
	}
	seenConversationTurn := false
	for index, item := range items {
		path := fmt.Sprintf("$.input[%d]", index)
		var message messagesMessage
		switch item.Type {
		case "message", "":
			parts, err := decodeResponsesContentRaw(item.Content, path+".content", true)
			if err != nil {
				return conversionResult{}, err
			}
			blocks, err := encodeMessagesContent(parts)
			if err != nil {
				return conversionResult{}, err
			}
			if item.Role == "system" || item.Role == "developer" {
				if seenConversationTurn {
					return conversionResult{}, unsupported(ProtocolResponses, path+".role", "interleaved system/developer messages cannot be moved to Messages top-level system")
				}
				var system []messagesBlock
				_ = json.Unmarshal(target.System, &system)
				system = append(system, blocks...)
				target.System = mustJSON(system)
				continue
			}
			if item.Role != "user" && item.Role != "assistant" {
				return conversionResult{}, unsupported(ProtocolResponses, path+".role", "role %q cannot be represented by Messages", item.Role)
			}
			seenConversationTurn = true
			message = messagesMessage{Role: item.Role, Content: mustJSON(blocks)}
		case "function_call":
			seenConversationTurn = true
			arguments, err := normalizeArguments(ProtocolResponses, path+".arguments", item.Arguments)
			if err != nil {
				return conversionResult{}, err
			}
			message = messagesMessage{Role: "assistant", Content: mustJSON([]messagesBlock{{Type: "tool_use", ID: item.CallID, Name: item.Name, Input: arguments}})}
		case "function_call_output":
			seenConversationTurn = true
			content, err := responsesToolOutputToMessages(item.Output, path+".output")
			if err != nil {
				return conversionResult{}, err
			}
			message = messagesMessage{Role: "user", Content: mustJSON([]messagesBlock{{Type: "tool_result", ToolUseID: item.CallID, Content: content}})}
		case "reasoning":
			return conversionResult{}, unsupported(ProtocolResponses, path, "reasoning items cannot be injected as signed Messages thinking")
		default:
			return conversionResult{}, unsupported(ProtocolResponses, path+".type", "input item %q requires a native Responses provider", item.Type)
		}
		appendMessagesTurn(&target.Messages, message)
	}
	body, err := marshal(ProtocolMessages, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *responsesToMessagesConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source messagesResponse
	if err := decodeJSON(ProtocolMessages, input, &source); err != nil {
		return conversionResult{}, err
	}
	if err := validateMessagesResponse(source); err != nil {
		return conversionResult{}, err
	}
	var blocks []messagesBlock
	if err := json.Unmarshal(source.Content, &blocks); err != nil {
		return conversionResult{}, invalid(ProtocolMessages, "$.content", "content must be a block array")
	}
	target := responsesResponse{ID: source.ID, Object: "response", Model: source.Model, Status: "completed"}
	if options.Exchange.ClientModel != "" {
		target.Model = options.Exchange.ClientModel
	}
	var textParts []portablePart
	flushText := func() error {
		if len(textParts) == 0 {
			return nil
		}
		content, err := encodeResponsesContent(textParts, false)
		if err != nil {
			return err
		}
		target.Output = append(target.Output, responsesItem{Type: "message", ID: fmt.Sprintf("msg_%s_%d", source.ID, len(target.Output)), Role: "assistant", Content: mustJSON(content), Status: "completed"})
		textParts = nil
		return nil
	}
	for index, block := range blocks {
		path := fmt.Sprintf("$.content[%d]", index)
		if err := rejectMessagesBlockMetadata(block, path); err != nil {
			return conversionResult{}, err
		}
		switch block.Type {
		case "text":
			textParts = append(textParts, portablePart{Kind: partText, Text: block.Text})
		case "tool_use":
			if err := flushText(); err != nil {
				return conversionResult{}, err
			}
			if block.ID == "" || block.Name == "" {
				return conversionResult{}, upstreamResponseError(ProtocolMessages, path, "tool_use requires id and name")
			}
			arguments, err := normalizeArguments(ProtocolMessages, path+".input", block.Input)
			if err != nil {
				return conversionResult{}, err
			}
			target.Output = append(target.Output, responsesItem{Type: "function_call", ID: "fc_" + block.ID, CallID: block.ID, Name: block.Name, Arguments: json.RawMessage(mustJSONString(string(arguments))), Status: "completed"})
		case "thinking", "redacted_thinking":
			return conversionResult{}, unsupported(ProtocolMessages, path, "signed or redacted thinking cannot be represented as Responses reasoning")
		default:
			return conversionResult{}, unsupported(ProtocolMessages, path+".type", "content block %q cannot be represented by Responses output", block.Type)
		}
	}
	if err := flushText(); err != nil {
		return conversionResult{}, err
	}
	finish, err := parseMessagesFinish(source.StopReason)
	if err != nil {
		return conversionResult{}, err
	}
	var diagnostics []Diagnostic
	if source.StopReason == "stop_sequence" && source.StopSequence != "" {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolMessages, "$.stop_sequence", "Responses cannot expose the matched stop sequence")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "stop_sequence_not_representable", "$.stop_sequence", "the matched Messages stop sequence was omitted")
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
	target.Usage.InputTokens = source.Usage.InputTokens + source.Usage.CacheReadInputTokens + source.Usage.CacheCreationInputTokens
	target.Usage.OutputTokens = source.Usage.OutputTokens
	target.Usage.TotalTokens = target.Usage.InputTokens + source.Usage.OutputTokens
	target.Usage.InputTokenDetails.CachedTokens = source.Usage.CacheReadInputTokens
	if source.Usage.CacheCreationInputTokens > 0 {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolMessages, "$.usage.cache_creation_input_tokens", "Responses usage has no cache-creation token field")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "cache_creation_usage_not_representable", "$.usage.cache_creation_input_tokens", "cache-creation tokens were omitted from the Responses usage object")
	}
	if len(source.Usage.ServerToolUse) > 0 && string(source.Usage.ServerToolUse) != "null" && string(source.Usage.ServerToolUse) != "{}" {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolMessages, "$.usage.server_tool_use", "Responses usage has no server-tool usage field")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "server_tool_usage_not_representable", "$.usage.server_tool_use", "server-tool usage was omitted from the Responses usage object")
	}
	body, err := marshal(ProtocolResponses, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func responsesToolOutputToMessages(raw json.RawMessage, path string) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`""`), nil
	}
	if raw[0] == '"' {
		return append(json.RawMessage(nil), raw...), nil
	}
	parts, err := decodeResponsesContentRaw(raw, path, true)
	if err != nil {
		return nil, err
	}
	blocks, err := encodeMessagesContent(parts)
	if err != nil {
		return nil, err
	}
	return mustJSON(blocks), nil
}

func (c *responsesToMessagesConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return c.buffered(c.spec, options, c.ToClientResponse), nil
}
