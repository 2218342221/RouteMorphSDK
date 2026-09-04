package responsesmessages

import (
	"context"
	"encoding/json"
	"fmt"
)

type messagesToResponsesConverter struct {
	spec routeSpec
}

func (c *messagesToResponsesConverter) Specification() routeSpec { return c.spec }

func (c *messagesToResponsesConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolMessages, input, "model", "max_tokens", "messages", "system", "tools", "tool_choice", "temperature", "top_p", "stop_sequences", "stream", "thinking", "output_config", "metadata", "container"); err != nil {
		return conversionResult{}, err
	}
	var source messagesRequest
	if err := decodeJSON(ProtocolMessages, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Model == "" || len(source.Messages) == 0 {
		return conversionResult{}, invalid(ProtocolMessages, "$", "model and messages are required")
	}
	if source.MaxTokens == 0 {
		return conversionResult{}, unsupported(ProtocolMessages, "$.max_tokens", "prompt-cache pre-warming with max_tokens=0 has no portable Responses equivalent")
	}
	if source.MaxTokens < 0 {
		return conversionResult{}, invalid(ProtocolMessages, "$.max_tokens", "must not be negative")
	}
	if len(source.Container) > 0 && string(source.Container) != "null" {
		return conversionResult{}, unsupported(ProtocolMessages, "$.container", "container state requires a native Messages provider")
	}
	if source.Thinking != nil && source.Thinking.Type != "disabled" && options.LossPolicy == rejectSemanticLoss {
		return conversionResult{}, unsupported(ProtocolMessages, "$.thinking", "Messages thinking configuration is not semantically equivalent to Responses reasoning")
	}
	if err := validateMessagesThinkingBudget(source.Thinking, source.MaxTokens, "$.thinking"); err != nil {
		return conversionResult{}, err
	}
	if err := validateMessagesOutputConfig(source.OutputConfig, "$.output_config"); err != nil {
		return conversionResult{}, err
	}
	if len(source.StopSequences) > 0 {
		return conversionResult{}, unsupported(ProtocolMessages, "$.stop_sequences", "Responses has no equivalent stop parameter")
	}
	target := responsesRequest{
		Model:           source.Model,
		MaxOutputTokens: &source.MaxTokens,
		Temperature:     source.Temperature,
		TopP:            source.TopP,
		Stream:          resolveExchangeStream(source.Stream, options.Exchange),
		Metadata:        source.Metadata,
	}
	if options.Exchange.UpstreamModel != "" {
		target.Model = options.Exchange.UpstreamModel
	}
	var diagnostics []Diagnostic
	hasReasoningPolicy := source.Thinking != nil && source.Thinking.Type != "disabled"
	reasoningPolicyPath := "$.thinking"
	if source.OutputConfig != nil && source.OutputConfig.Effort != "" {
		hasReasoningPolicy = true
		if source.Thinking == nil || source.Thinking.Type == "disabled" {
			reasoningPolicyPath = "$.output_config.effort"
		}
	}
	if hasReasoningPolicy {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolMessages, reasoningPolicyPath, "Messages thinking and effort configuration is not semantically equivalent to Responses reasoning")
		}
		target.Reasoning = &reasoningConfig{Effort: "medium"}
		diagnostics = appendDiagnostic(diagnostics, "warning", "thinking_policy_approximated", reasoningPolicyPath, "Messages thinking and effort policy was approximated as Responses reasoning")
		if source.Thinking != nil && source.Thinking.Display != "" {
			diagnostics = appendDiagnostic(diagnostics, "warning", "thinking_display_not_representable", "$.thinking.display", "Messages thinking display policy was omitted")
		}
	}
	if source.OutputConfig != nil {
		if source.OutputConfig.Effort != "" {
			if target.Reasoning == nil {
				target.Reasoning = &reasoningConfig{}
			}
			target.Reasoning.Effort = source.OutputConfig.Effort
		}
		if source.OutputConfig.Format != nil {
			if source.OutputConfig.Format.Type != "json_schema" {
				return conversionResult{}, unsupported(ProtocolMessages, "$.output_config.format.type", "format %q is not portable", source.OutputConfig.Format.Type)
			}
			if len(source.OutputConfig.Format.Schema) == 0 || string(source.OutputConfig.Format.Schema) == "null" {
				return conversionResult{}, invalid(ProtocolMessages, "$.output_config.format.schema", "schema is required")
			}
			target.Text = mustJSON(map[string]any{"format": map[string]any{"type": "json_schema", "name": "response", "schema": source.OutputConfig.Format.Schema}})
		}
	}
	choice, parallelToolCalls, err := decodeMessagesToolChoice(source.ToolChoice)
	if err != nil {
		return conversionResult{}, err
	}
	if choice.Mode != "" {
		target.ToolChoice = encodeResponsesToolChoice(choice)
	}
	target.ParallelToolCalls = parallelToolCalls
	for index, tool := range source.Tools {
		if len(tool.CacheControl) > 0 && string(tool.CacheControl) != "null" {
			return conversionResult{}, unsupported(ProtocolMessages, fmt.Sprintf("$.tools[%d].cache_control", index), "tool cache control has no portable equivalent")
		}
		if tool.Type != "" && tool.Type != "custom" {
			return conversionResult{}, unsupported(ProtocolMessages, fmt.Sprintf("$.tools[%d].type", index), "server tool %q requires a native Messages provider", tool.Type)
		}
		if tool.Name == "" {
			return conversionResult{}, invalid(ProtocolMessages, fmt.Sprintf("$.tools[%d].name", index), "name is required")
		}
		schema, err := normalizeMessagesInputSchema(tool.InputSchema, fmt.Sprintf("$.tools[%d].input_schema", index))
		if err != nil {
			return conversionResult{}, err
		}
		target.Tools = append(target.Tools, responsesTool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: schema, Strict: tool.Strict})
	}
	if len(source.System) > 0 && string(source.System) != "null" {
		parts, err := decodeMessagesContent(source.System, "$.system")
		if err != nil {
			return conversionResult{}, err
		}
		content, err := encodeResponsesContent(parts, true)
		if err != nil {
			return conversionResult{}, err
		}
		instructionText, err := portableTextOnly(ProtocolMessages, content, "$.system")
		if err != nil {
			return conversionResult{}, err
		}
		target.Instructions = json.RawMessage(mustJSONString(instructionText))
	}
	items := make([]responsesItem, 0, len(source.Messages))
	for messageIndex, message := range source.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return conversionResult{}, invalid(ProtocolMessages, fmt.Sprintf("$.messages[%d].role", messageIndex), "role must be user or assistant")
		}
		if len(message.Content) == 0 || string(message.Content) == "null" {
			return conversionResult{}, invalid(ProtocolMessages, fmt.Sprintf("$.messages[%d].content", messageIndex), "content is required")
		}
		var blocks []messagesBlock
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			if message.Content[0] == '"' {
				blocks = []messagesBlock{{Type: "text", Text: rawString(message.Content)}}
			} else {
				return conversionResult{}, invalid(ProtocolMessages, fmt.Sprintf("$.messages[%d].content", messageIndex), "content must be string or blocks")
			}
		}
		var ordinary []portablePart
		flushOrdinary := func() error {
			if len(ordinary) == 0 {
				return nil
			}
			content, err := encodeResponsesContent(ordinary, true)
			if err != nil {
				return err
			}
			items = append(items, responsesItem{Type: "message", Role: message.Role, Content: mustJSON(content)})
			ordinary = nil
			return nil
		}
		for blockIndex, block := range blocks {
			path := fmt.Sprintf("$.messages[%d].content[%d]", messageIndex, blockIndex)
			if err := rejectMessagesBlockMetadata(block, path); err != nil {
				return conversionResult{}, err
			}
			switch block.Type {
			case "text", "image", "document":
				parts, err := decodeMessagesContent(mustJSON([]messagesBlock{block}), path)
				if err != nil {
					return conversionResult{}, err
				}
				ordinary = append(ordinary, parts...)
			case "tool_use":
				if message.Role != "assistant" {
					return conversionResult{}, invalid(ProtocolMessages, path, "tool_use blocks require the assistant role")
				}
				if err := flushOrdinary(); err != nil {
					return conversionResult{}, err
				}
				if block.ID == "" || block.Name == "" {
					return conversionResult{}, invalid(ProtocolMessages, path, "tool_use requires id and name")
				}
				arguments, err := normalizeArguments(ProtocolMessages, path+".input", block.Input)
				if err != nil {
					return conversionResult{}, err
				}
				items = append(items, responsesItem{Type: "function_call", CallID: block.ID, Name: block.Name, Arguments: json.RawMessage(mustJSONString(string(arguments)))})
			case "tool_result":
				if message.Role != "user" {
					return conversionResult{}, invalid(ProtocolMessages, path, "tool_result blocks require the user role")
				}
				if err := flushOrdinary(); err != nil {
					return conversionResult{}, err
				}
				if block.ToolUseID == "" {
					return conversionResult{}, invalid(ProtocolMessages, path+".tool_use_id", "tool_use_id is required")
				}
				if block.IsError {
					return conversionResult{}, unsupported(ProtocolMessages, path+".is_error", "Responses function_call_output cannot preserve the tool error flag")
				}
				parts, err := decodeMessagesContent(block.Content, path+".content")
				if err != nil {
					return conversionResult{}, err
				}
				output := json.RawMessage(mustJSONString(joinText(parts)))
				if len(block.Content) > 0 && block.Content[0] != '"' {
					converted, err := encodeResponsesContent(parts, true)
					if err != nil {
						return conversionResult{}, err
					}
					output = mustJSON(converted)
				}
				items = append(items, responsesItem{Type: "function_call_output", CallID: block.ToolUseID, Output: output})
			case "thinking", "redacted_thinking":
				return conversionResult{}, unsupported(ProtocolMessages, path, "signed or redacted thinking cannot be injected into Responses")
			default:
				return conversionResult{}, unsupported(ProtocolMessages, path+".type", "content block %q requires a native Messages provider", block.Type)
			}
		}
		if err := flushOrdinary(); err != nil {
			return conversionResult{}, err
		}
	}
	target.Input = mustJSON(items)
	body, err := marshal(ProtocolResponses, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *messagesToResponsesConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source responsesResponse
	if err := decodeJSON(ProtocolResponses, input, &source); err != nil {
		return conversionResult{}, err
	}
	if err := validateResponsesTerminal(source); err != nil {
		return conversionResult{}, err
	}
	var target messagesResponse
	target.ID, target.Type, target.Role, target.Model = source.ID, "message", "assistant", source.Model
	if options.Exchange.ClientModel != "" {
		target.Model = options.Exchange.ClientModel
	}
	var blocks []messagesBlock
	diagnostics := responsesPhaseDiagnostics(source.Output, "$.output")
	for index, item := range source.Output {
		path := fmt.Sprintf("$.output[%d]", index)
		switch item.Type {
		case "message":
			if item.Role != "assistant" {
				return conversionResult{}, upstreamResponseError(ProtocolResponses, path+".role", "output message role must be assistant")
			}
			parts, err := decodeResponsesContentRaw(item.Content, path+".content", false)
			if err != nil {
				return conversionResult{}, err
			}
			for _, part := range parts {
				if part.Kind == partRefusal {
					if options.LossPolicy == rejectSemanticLoss {
						return conversionResult{}, unsupported(ProtocolResponses, path+".content", "Responses refusal content has no equivalent Messages content block")
					}
					diagnostics = appendDiagnostic(diagnostics, "warning", "refusal_content_approximated", path+".content", "Responses refusal content was emitted as a Messages text block")
					blocks = append(blocks, messagesBlock{Type: "text", Text: part.Text})
					continue
				}
				converted, err := encodeMessagesContent([]portablePart{part})
				if err != nil {
					return conversionResult{}, err
				}
				blocks = append(blocks, converted...)
			}
		case "function_call":
			arguments, err := normalizeArguments(ProtocolResponses, path+".arguments", item.Arguments)
			if err != nil {
				return conversionResult{}, err
			}
			blocks = append(blocks, messagesBlock{Type: "tool_use", ID: item.CallID, Name: item.Name, Input: arguments})
			if item.ID != "" && item.ID != item.CallID {
				diagnostics = appendDiagnostic(diagnostics, "warning", "responses_item_id_not_representable", path+".id", "Messages preserves call_id as tool_use.id but has no separate output item id")
			}
		case "reasoning":
			return conversionResult{}, unsupported(ProtocolResponses, path, "Responses reasoning summary is not equivalent to signed Messages thinking")
		default:
			return conversionResult{}, unsupported(ProtocolResponses, path+".type", "output item %q cannot be represented by Messages", item.Type)
		}
	}
	target.Content = mustJSON(blocks)
	target.StopReason = messagesStop(finishStop)
	if source.Status == "incomplete" {
		target.StopReason = "max_tokens"
		if source.IncompleteDetails != nil && source.IncompleteDetails.Reason == "content_filter" {
			target.StopReason = "refusal"
		}
	} else {
		for _, item := range source.Output {
			if item.Type == "function_call" {
				target.StopReason = "tool_use"
			}
		}
	}
	if source.Usage.InputTokens < source.Usage.InputTokenDetails.CachedTokens {
		return conversionResult{}, upstreamResponseError(ProtocolResponses, "$.usage.input_tokens_details.cached_tokens", "cached tokens exceed input tokens")
	}
	target.Usage.InputTokens = source.Usage.InputTokens - source.Usage.InputTokenDetails.CachedTokens
	target.Usage.OutputTokens = source.Usage.OutputTokens
	target.Usage.CacheReadInputTokens = source.Usage.InputTokenDetails.CachedTokens
	if source.Usage.OutputTokenDetails.ReasoningTokens > 0 {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolResponses, "$.usage.output_tokens_details.reasoning_tokens", "Messages usage has no reasoning-token field")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "reasoning_usage_not_representable", "$.usage.output_tokens_details.reasoning_tokens", "reasoning-token usage was omitted")
	}
	body, err := marshal(ProtocolMessages, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *messagesToResponsesConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return &responsesToMessagesStreamConverter{
		clientModel:    options.Exchange.ClientModel,
		LossPolicy:     options.LossPolicy,
		openBlocks:     make(map[int]bool),
		blockIndexes:   make(map[string]int),
		itemBlocks:     make(map[string][]int),
		itemTypes:      make(map[string]string),
		textByBlock:    make(map[int]string),
		toolArgs:       make(map[string]string),
		toolIdentities: make(map[string]streamToolIdentity),
		completedItems: make(map[string]bool),
	}, nil
}
