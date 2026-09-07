package chatresponses

import "encoding/json"

func rejectUnrepresentableChatUsage(raw []byte) error {
	var envelope struct {
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
			PromptDetails    struct {
				AudioTokens  int64 `json:"audio_tokens"`
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				AcceptedPredictionTokens int64 `json:"accepted_prediction_tokens"`
				AudioTokens              int64 `json:"audio_tokens"`
				ReasoningTokens          int64 `json:"reasoning_tokens"`
				RejectedPredictionTokens int64 `json:"rejected_prediction_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return invalid(ProtocolChat, "$.usage", "invalid usage: %v", err)
	}
	for path, value := range map[string]int64{
		"$.usage.prompt_tokens":                                        envelope.Usage.PromptTokens,
		"$.usage.completion_tokens":                                    envelope.Usage.CompletionTokens,
		"$.usage.total_tokens":                                         envelope.Usage.TotalTokens,
		"$.usage.prompt_tokens_details.cached_tokens":                  envelope.Usage.PromptDetails.CachedTokens,
		"$.usage.prompt_tokens_details.audio_tokens":                   envelope.Usage.PromptDetails.AudioTokens,
		"$.usage.completion_tokens_details.reasoning_tokens":           envelope.Usage.CompletionDetails.ReasoningTokens,
		"$.usage.completion_tokens_details.audio_tokens":               envelope.Usage.CompletionDetails.AudioTokens,
		"$.usage.completion_tokens_details.accepted_prediction_tokens": envelope.Usage.CompletionDetails.AcceptedPredictionTokens,
		"$.usage.completion_tokens_details.rejected_prediction_tokens": envelope.Usage.CompletionDetails.RejectedPredictionTokens,
	} {
		if value < 0 {
			return upstreamResponseError(ProtocolChat, path, "must be non-negative")
		}
	}
	if envelope.Usage.PromptDetails.CachedTokens > envelope.Usage.PromptTokens {
		return upstreamResponseError(ProtocolChat, "$.usage.prompt_tokens_details.cached_tokens", "cannot exceed prompt_tokens")
	}
	if envelope.Usage.CompletionDetails.ReasoningTokens > envelope.Usage.CompletionTokens {
		return upstreamResponseError(ProtocolChat, "$.usage.completion_tokens_details.reasoning_tokens", "cannot exceed completion_tokens")
	}
	if envelope.Usage.PromptDetails.AudioTokens != 0 {
		return unsupported(ProtocolChat, "$.usage.prompt_tokens_details.audio_tokens", "Responses usage has no input audio token field")
	}
	if envelope.Usage.CompletionDetails.AudioTokens != 0 {
		return unsupported(ProtocolChat, "$.usage.completion_tokens_details.audio_tokens", "Responses usage has no output audio token field")
	}
	if envelope.Usage.CompletionDetails.AcceptedPredictionTokens != 0 {
		return unsupported(ProtocolChat, "$.usage.completion_tokens_details.accepted_prediction_tokens", "Responses usage has no accepted prediction token field")
	}
	if envelope.Usage.CompletionDetails.RejectedPredictionTokens != 0 {
		return unsupported(ProtocolChat, "$.usage.completion_tokens_details.rejected_prediction_tokens", "Responses usage has no rejected prediction token field")
	}
	return nil
}

func rejectUnrepresentableResponsesUsage(raw []byte, base string) error {
	var envelope struct {
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			TotalTokens  int64 `json:"total_tokens"`
			InputDetails struct {
				CacheWriteTokens int64 `json:"cache_write_tokens"`
				CachedTokens     int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return invalid(ProtocolResponses, base+".usage", "invalid usage: %v", err)
	}
	for path, value := range map[string]int64{
		base + ".usage.input_tokens":                            envelope.Usage.InputTokens,
		base + ".usage.output_tokens":                           envelope.Usage.OutputTokens,
		base + ".usage.total_tokens":                            envelope.Usage.TotalTokens,
		base + ".usage.input_tokens_details.cached_tokens":      envelope.Usage.InputDetails.CachedTokens,
		base + ".usage.input_tokens_details.cache_write_tokens": envelope.Usage.InputDetails.CacheWriteTokens,
		base + ".usage.output_tokens_details.reasoning_tokens":  envelope.Usage.OutputDetails.ReasoningTokens,
	} {
		if value < 0 {
			return upstreamResponseError(ProtocolResponses, path, "must be non-negative")
		}
	}
	if envelope.Usage.InputDetails.CachedTokens > envelope.Usage.InputTokens {
		return upstreamResponseError(ProtocolResponses, base+".usage.input_tokens_details.cached_tokens", "cannot exceed input_tokens")
	}
	if envelope.Usage.InputDetails.CacheWriteTokens > envelope.Usage.InputTokens {
		return upstreamResponseError(ProtocolResponses, base+".usage.input_tokens_details.cache_write_tokens", "cannot exceed input_tokens")
	}
	if envelope.Usage.OutputDetails.ReasoningTokens > envelope.Usage.OutputTokens {
		return upstreamResponseError(ProtocolResponses, base+".usage.output_tokens_details.reasoning_tokens", "cannot exceed output_tokens")
	}
	if envelope.Usage.InputDetails.CacheWriteTokens != 0 {
		return unsupported(ProtocolResponses, base+".usage.input_tokens_details.cache_write_tokens", "Chat usage has no cache write token field")
	}
	return nil
}
