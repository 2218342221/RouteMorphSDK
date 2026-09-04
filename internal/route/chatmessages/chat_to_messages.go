package chatmessages

import (
	"context"
	"fmt"
	"time"
)

// chatToMessagesConverter and messagesToChatConverter intentionally bypass
// Responses. Both APIs can preserve stop sequences and their shared portable
// message/tool subset, while a Responses hop cannot represent stop controls.
type chatToMessagesConverter struct {
	spec     routeSpec
	buffered BufferedFactory
}
type messagesToChatConverter struct {
	spec     routeSpec
	buffered BufferedFactory
}

func (c *chatToMessagesConverter) Specification() routeSpec { return c.spec }
func (c *messagesToChatConverter) Specification() routeSpec { return c.spec }

func (c *chatToMessagesConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolChat, input,
		"model", "messages", "tools", "tool_choice", "max_tokens", "max_completion_tokens",
		"temperature", "top_p", "stop", "stream", "parallel_tool_calls", "response_format",
		"reasoning_effort", "metadata", "n", "frequency_penalty", "presence_penalty",
		"logprobs", "top_logprobs", "verbosity", "user", "service_tier", "store",
		"prompt_cache_key", "prompt_cache_retention", "safety_identifier", "stream_options"); err != nil {
		return conversionResult{}, err
	}
	extraDiagnostics, err := validateChatToMessagesRequest(input, options.LossPolicy)
	if err != nil {
		return conversionResult{}, err
	}
	body, diagnostics, err := mapChatRequestToMessages(input, options)
	diagnostics = append(extraDiagnostics, diagnostics...)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *chatToMessagesConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source messagesResponse
	if err := decodeJSON(ProtocolMessages, input, &source); err != nil {
		return conversionResult{}, err
	}
	if err := validateMessagesResponse(source); err != nil {
		return conversionResult{}, err
	}
	parts, err := decodeMessagesContent(source.Content, "$.content")
	if err != nil {
		return conversionResult{}, err
	}
	var diagnostics []Diagnostic
	if source.StopReason == "stop_sequence" {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolMessages, "$.stop_sequence", "Chat responses cannot preserve the matched stop sequence")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "stop_sequence_not_representable", "$.stop_sequence", "the matched Messages stop sequence was omitted from the Chat response")
	}
	if source.Usage.CacheCreationInputTokens > 0 {
		if options.LossPolicy == rejectSemanticLoss {
			return conversionResult{}, unsupported(ProtocolMessages, "$.usage.cache_creation_input_tokens", "Chat usage has no cache-creation token field")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "cache_creation_usage_not_representable", "$.usage.cache_creation_input_tokens", "cache-creation tokens were omitted from Chat usage")
	}
	for index, part := range parts {
		if part.Kind == partReasoning && part.Opaque != "" {
			if options.LossPolicy == rejectSemanticLoss {
				return conversionResult{}, unsupported(ProtocolMessages, fmt.Sprintf("$.content[%d]", index), "signed Messages thinking cannot be represented by Chat")
			}
			diagnostics = appendDiagnostic(diagnostics, "warning", "thinking_signature_not_representable", fmt.Sprintf("$.content[%d]", index), "Messages thinking signature was omitted")
		}
	}
	message, err := encodeChatMessage(portableMessage{Role: roleAssistant, Parts: parts}, 0)
	if err != nil {
		return conversionResult{}, err
	}
	finish, err := parseMessagesFinish(source.StopReason)
	if err != nil {
		return conversionResult{}, err
	}
	model := source.Model
	if options.Exchange.ClientModel != "" {
		model = options.Exchange.ClientModel
	}
	target := chatResponse{ID: source.ID, Object: "chat.completion", Created: time.Now().Unix(), Model: model}
	target.Choices = []chatResponseChoice{{Message: message[0], FinishReason: string(finish)}}
	target.Usage.PromptTokens = source.Usage.InputTokens + source.Usage.CacheReadInputTokens + source.Usage.CacheCreationInputTokens
	target.Usage.CompletionTokens = source.Usage.OutputTokens
	target.Usage.TotalTokens = target.Usage.PromptTokens + target.Usage.CompletionTokens
	target.Usage.PromptDetails.CachedTokens = source.Usage.CacheReadInputTokens
	body, err := marshal(ProtocolChat, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *chatToMessagesConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return c.buffered(c.spec, options, c.ToClientResponse), nil
}
