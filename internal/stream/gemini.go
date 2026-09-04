package stream

import (
	"bytes"
	"encoding/json"

	geminiwire "github.com/2218342221/RouteMorphSDK/internal/wire/gemini"
)

type geminiContent = geminiwire.Content

func jsonValuePresent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func normalizeGeminiTerminalEmptyTextParts(data []byte, source *geminiResponse) error {
	if source == nil || len(source.Candidates) == 0 {
		return nil
	}
	var wire struct {
		Candidates []struct {
			Content struct {
				Parts []json.RawMessage `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return invalid(ProtocolGenerateContent, "$", "invalid stream chunk: %v", err)
	}
	for candidateIndex := range source.Candidates {
		candidate := &source.Candidates[candidateIndex]
		if candidate.FinishReason == "" || candidate.FinishReason == "FINISH_REASON_UNSPECIFIED" || candidateIndex >= len(wire.Candidates) {
			continue
		}
		rawParts := wire.Candidates[candidateIndex].Content.Parts
		if len(rawParts) != len(candidate.Content.Parts) {
			continue
		}
		kept := candidate.Content.Parts[:0]
		for partIndex, part := range candidate.Content.Parts {
			var object map[string]json.RawMessage
			if json.Unmarshal(rawParts[partIndex], &object) == nil && len(object) == 1 {
				if textRaw, ok := object["text"]; ok {
					var text string
					if json.Unmarshal(textRaw, &text) == nil && text == "" {
						continue
					}
				}
			}
			kept = append(kept, part)
		}
		candidate.Content.Parts = kept
	}
	return nil
}

type geminiCandidate = geminiwire.Candidate
type geminiResponse = geminiwire.Response

func validateGeminiResponseEnvelope(source *geminiResponse, policy lossPolicy) ([]Diagnostic, error) {
	if source == nil {
		return nil, invalid(ProtocolGenerateContent, "$", "response is required")
	}
	if len(source.Candidates) == 0 {
		if reason := geminiPromptBlockReason(source.PromptFeedback); reason != "" {
			return nil, upstreamResponseError(ProtocolGenerateContent, "$.promptFeedback.blockReason", "request blocked by Gemini API: %s", reason)
		}
		return nil, upstreamResponseError(ProtocolGenerateContent, "$.candidates", "Gemini returned no candidates")
	}
	if len(source.Candidates) != 1 {
		return nil, unsupported(ProtocolGenerateContent, "$.candidates", "cross-protocol conversion requires exactly one candidate")
	}
	var diagnostics []Diagnostic
	candidate := source.Candidates[0]
	if candidate.AvgLogprobs != nil || jsonValuePresent(candidate.LogprobsResult) {
		if policy == rejectSemanticLoss {
			return nil, unsupported(ProtocolGenerateContent, "$.candidates[0].logprobsResult", "Gemini token ids and average log probability have no lossless cross-protocol representation")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_logprobs_not_representable", "$.candidates[0].logprobsResult", "Gemini token ids and average log probability were omitted")
	}
	for _, field := range []struct {
		path string
		raw  json.RawMessage
	}{
		{"$.candidates[0].citationMetadata", candidate.CitationMetadata},
		{"$.candidates[0].groundingMetadata", candidate.GroundingMetadata},
		{"$.candidates[0].urlContextMetadata", candidate.URLContextMetadata},
	} {
		if !jsonValuePresent(field.raw) {
			continue
		}
		if policy == rejectSemanticLoss {
			return nil, unsupported(ProtocolGenerateContent, field.path, "grounding metadata cannot be represented by the target protocol")
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_grounding_metadata_not_representable", field.path, "Gemini grounding metadata was omitted")
	}
	if jsonValuePresent(candidate.SafetyRatings) {
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_safety_ratings_not_representable", "$.candidates[0].safetyRatings", "Gemini safety ratings are not represented by the target protocol")
	}
	if jsonValuePresent(source.PromptFeedback) {
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_prompt_feedback_not_representable", "$.promptFeedback", "Gemini prompt feedback is not represented by the target protocol")
	}
	if jsonValuePresent(source.UsageMetadata.PromptTokensDetails) || jsonValuePresent(source.UsageMetadata.ToolUsePromptTokensDetails) || jsonValuePresent(source.UsageMetadata.CandidatesTokensDetails) {
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_modality_usage_not_representable", "$.usageMetadata", "per-modality Gemini token details are not represented by the target protocol")
	}
	return diagnostics, nil
}

func geminiPromptBlockReason(raw json.RawMessage) string {
	if !jsonValuePresent(raw) {
		return ""
	}
	var feedback struct {
		BlockReason string `json:"blockReason"`
	}
	if json.Unmarshal(raw, &feedback) != nil {
		return ""
	}
	return feedback.BlockReason
}

func parseGeminiFinish(value string) (finishReason, error) {
	switch value {
	case "", "FINISH_REASON_UNSPECIFIED":
		return "", upstreamResponseError(ProtocolGenerateContent, "$.candidates[0].finishReason", "finish reason is missing")
	case "MAX_TOKENS":
		return finishLength, nil
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "RECITATION", "LANGUAGE", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_OTHER", "NO_IMAGE", "IMAGE_RECITATION":
		return finishContentFilter, nil
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "MISSING_THOUGHT_SIGNATURE", "OTHER":
		return "", upstreamResponseError(ProtocolGenerateContent, "$.candidates[0].finishReason", "generation failed with %q", value)
	case "STOP":
		return finishStop, nil
	default:
		return "", upstreamResponseError(ProtocolGenerateContent, "$.candidates[0].finishReason", "unsupported finish reason %q", value)
	}
}
