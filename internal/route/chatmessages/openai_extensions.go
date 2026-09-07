package chatmessages

import (
	"bytes"
	"encoding/json"
)

func chatAnnotationsPresent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("[]")) {
		return false
	}
	return true
}

func chatResponseExtensionDiagnostics(source chatResponse, policy lossPolicy) ([]Diagnostic, error) {
	var diagnostics []Diagnostic
	choice := source.Choices[0]
	if chatAnnotationsPresent(choice.Message.Annotations) {
		var annotations []json.RawMessage
		if err := json.Unmarshal(choice.Message.Annotations, &annotations); err != nil {
			return nil, upstreamResponseError(ProtocolChat, "$.choices[0].message.annotations", "must be an array")
		}
		if len(annotations) > 0 {
			var err error
			diagnostics, err = rejectOrDiagnoseChatResponseLoss(diagnostics, policy, "$.choices[0].message.annotations", "chat_annotations_not_representable", "Chat URL citations were omitted from the Messages response")
			if err != nil {
				return nil, err
			}
		}
	}
	for _, field := range []struct {
		present bool
		path    string
		code    string
		message string
	}{
		{len(source.Metadata) > 0, "$.metadata", "chat_metadata_not_representable", "Chat response metadata was omitted from the Messages response"},
		{messagesRawNonNull(source.Moderation), "$.moderation", "chat_moderation_not_representable", "Chat moderation results were omitted from the Messages response"},
		{messagesRawNonNull(source.ServiceTier), "$.service_tier", "chat_service_tier_not_representable", "Chat service tier was omitted from the Messages response"},
		{source.SystemFingerprint != "", "$.system_fingerprint", "chat_system_fingerprint_not_representable", "Chat system fingerprint was omitted from the Messages response"},
	} {
		if !field.present {
			continue
		}
		var err error
		diagnostics, err = rejectOrDiagnoseChatResponseLoss(diagnostics, policy, field.path, field.code, field.message)
		if err != nil {
			return nil, err
		}
	}

	usage := []struct {
		value   int64
		path    string
		code    string
		message string
	}{
		{source.Usage.PromptDetails.AudioTokens, "$.usage.prompt_tokens_details.audio_tokens", "chat_input_audio_usage_not_representable", "Chat input audio token usage was omitted from the Messages response"},
		{source.Usage.CompletionDetails.AudioTokens, "$.usage.completion_tokens_details.audio_tokens", "chat_output_audio_usage_not_representable", "Chat output audio token usage was omitted from the Messages response"},
		{source.Usage.CompletionDetails.AcceptedPredictionTokens, "$.usage.completion_tokens_details.accepted_prediction_tokens", "chat_accepted_prediction_usage_not_representable", "Chat accepted prediction token usage was omitted from the Messages response"},
		{source.Usage.CompletionDetails.RejectedPredictionTokens, "$.usage.completion_tokens_details.rejected_prediction_tokens", "chat_rejected_prediction_usage_not_representable", "Chat rejected prediction token usage was omitted from the Messages response"},
	}
	for _, field := range usage {
		if field.value < 0 {
			return nil, upstreamResponseError(ProtocolChat, field.path, "must be non-negative")
		}
		if field.value == 0 {
			continue
		}
		var err error
		diagnostics, err = rejectOrDiagnoseChatResponseLoss(diagnostics, policy, field.path, field.code, field.message)
		if err != nil {
			return nil, err
		}
	}
	return diagnostics, nil
}

func rejectOrDiagnoseChatResponseLoss(diagnostics []Diagnostic, policy lossPolicy, path, code, message string) ([]Diagnostic, error) {
	if policy == rejectSemanticLoss {
		return diagnostics, unsupported(ProtocolChat, path, "%s", message)
	}
	return appendDiagnostic(diagnostics, "warning", code, path, message), nil
}

func validateChatStreamOptionsForMessages(input []byte, stream bool, policy lossPolicy) ([]Diagnostic, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(input, &envelope); err != nil {
		return nil, invalid(ProtocolChat, "$", "invalid JSON: %v", err)
	}
	raw, exists := envelope["stream_options"]
	if !exists || !messagesRawNonNull(raw) {
		return nil, nil
	}
	if !stream {
		return nil, invalid(ProtocolChat, "$.stream_options", "stream_options requires stream=true")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, invalid(ProtocolChat, "$.stream_options", "must be an object")
	}
	var diagnostics []Diagnostic
	for name, value := range fields {
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err != nil {
			return nil, invalid(ProtocolChat, "$.stream_options."+name, "must be a boolean")
		}
		switch name {
		case "include_usage":
			// Relay metadata controls the generated Chat stream's usage chunk.
		case "include_obfuscation":
			if !enabled {
				continue
			}
			var err error
			diagnostics, err = rejectOrDiagnoseChatResponseLoss(diagnostics, policy, "$.stream_options.include_obfuscation", "chat_stream_obfuscation_not_representable", "Messages streaming has no equivalent payload-obfuscation guarantee")
			if err != nil {
				return nil, err
			}
		default:
			return nil, unsupported(ProtocolChat, "$.stream_options."+name, "stream option %q has no Messages equivalent", name)
		}
	}
	return diagnostics, nil
}
