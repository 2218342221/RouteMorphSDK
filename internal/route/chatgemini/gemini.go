package chatgemini

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	geminischema "github.com/2218342221/RouteMorphSDK/internal/geminischema"
	geminiwire "github.com/2218342221/RouteMorphSDK/internal/wire/gemini"
)

const geminiThoughtSignatureBypass = geminiwire.ExternalFunctionCallSignature

type geminiRequest = geminiwire.Request
type geminiContent = geminiwire.Content
type geminiPart = geminiwire.Part
type geminiBlob = geminiwire.Blob
type geminiFileData = geminiwire.FileData
type geminiFunctionCall = geminiwire.FunctionCall
type geminiFunctionResponse = geminiwire.FunctionResponse
type geminiTool = geminiwire.Tool
type geminiFunctionDeclaration = geminiwire.FunctionDeclaration
type geminiToolConfig = geminiwire.ToolConfig
type geminiThinkingConfig = geminiwire.ThinkingConfig
type geminiGenerationConfig = geminiwire.GenerationConfig

func validateGeminiNestedFields(data []byte) error {
	object, err := rawObject(ProtocolGenerateContent, data)
	if err != nil {
		return err
	}
	if raw := object["generationConfig"]; jsonValuePresent(raw) {
		if err := rejectGeminiObjectFields(raw, "$.generationConfig",
			"maxOutputTokens", "temperature", "topP", "topK", "candidateCount", "stopSequences",
			"responseMimeType", "responseSchema", "responseJsonSchema", "presencePenalty", "frequencyPenalty",
			"responseLogprobs", "logprobs", "enableEnhancedCivicAnswers", "mediaResolution", "seed",
			"responseModalities", "thinkingConfig", "speechConfig", "imageConfig"); err != nil {
			return err
		}
		var config map[string]json.RawMessage
		_ = json.Unmarshal(raw, &config)
		if jsonValuePresent(config["thinkingConfig"]) {
			if err := rejectGeminiObjectFields(config["thinkingConfig"], "$.generationConfig.thinkingConfig", "includeThoughts", "thinkingBudget", "thinkingLevel"); err != nil {
				return err
			}
		}
	}
	if raw := object["toolConfig"]; jsonValuePresent(raw) {
		if err := rejectGeminiObjectFields(raw, "$.toolConfig", "functionCallingConfig", "retrievalConfig", "includeServerSideToolInvocations"); err != nil {
			return err
		}
		var config map[string]json.RawMessage
		_ = json.Unmarshal(raw, &config)
		if jsonValuePresent(config["functionCallingConfig"]) {
			if err := rejectGeminiObjectFields(config["functionCallingConfig"], "$.toolConfig.functionCallingConfig", "mode", "allowedFunctionNames"); err != nil {
				return err
			}
		}
	}
	if err := validateGeminiContentJSON(object["systemInstruction"], "$.systemInstruction"); err != nil {
		return err
	}
	var contents []json.RawMessage
	if raw := object["contents"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &contents); err != nil {
			return invalid(ProtocolGenerateContent, "$.contents", "must be an array")
		}
	}
	for i, content := range contents {
		if err := validateGeminiContentJSON(content, fmt.Sprintf("$.contents[%d]", i)); err != nil {
			return err
		}
	}
	var tools []json.RawMessage
	if raw := object["tools"]; jsonValuePresent(raw) {
		if err := json.Unmarshal(raw, &tools); err != nil {
			return invalid(ProtocolGenerateContent, "$.tools", "must be an array")
		}
	}
	for i, raw := range tools {
		path := fmt.Sprintf("$.tools[%d]", i)
		if err := rejectGeminiObjectFields(raw, path, "functionDeclarations", "codeExecution", "googleSearch", "googleSearchRetrieval", "urlContext"); err != nil {
			return err
		}
		var tool map[string]json.RawMessage
		_ = json.Unmarshal(raw, &tool)
		var declarations []json.RawMessage
		if declarationRaw := tool["functionDeclarations"]; jsonValuePresent(declarationRaw) {
			if err := json.Unmarshal(declarationRaw, &declarations); err != nil {
				return invalid(ProtocolGenerateContent, path+".functionDeclarations", "must be an array")
			}
		}
		for j, declaration := range declarations {
			if err := rejectGeminiObjectFields(declaration, fmt.Sprintf("%s.functionDeclarations[%d]", path, j), "name", "description", "parameters"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateGeminiContentJSON(raw json.RawMessage, path string) error {
	if !jsonValuePresent(raw) {
		return nil
	}
	if err := rejectGeminiObjectFields(raw, path, "role", "parts"); err != nil {
		return err
	}
	var content map[string]json.RawMessage
	_ = json.Unmarshal(raw, &content)
	var parts []json.RawMessage
	if err := json.Unmarshal(content["parts"], &parts); err != nil {
		return invalid(ProtocolGenerateContent, path+".parts", "must be an array")
	}
	for i, part := range parts {
		partPath := fmt.Sprintf("%s.parts[%d]", path, i)
		if err := rejectGeminiObjectFields(part, partPath, "text", "inlineData", "fileData", "functionCall", "functionResponse", "thought", "thoughtSignature", "mediaResolution", "videoMetadata", "executableCode", "codeExecutionResult"); err != nil {
			return err
		}
		var value map[string]json.RawMessage
		_ = json.Unmarshal(part, &value)
		for field, allowed := range map[string][]string{
			"inlineData":       {"mimeType", "data"},
			"fileData":         {"mimeType", "fileUri"},
			"functionCall":     {"id", "name", "args"},
			"functionResponse": {"id", "name", "response", "willContinue", "scheduling", "parts"},
		} {
			if jsonValuePresent(value[field]) {
				if err := rejectGeminiObjectFields(value[field], partPath+"."+field, allowed...); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func rejectGeminiObjectFields(raw json.RawMessage, path string, allowed ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return invalid(ProtocolGenerateContent, path, "must be an object")
	}
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range object {
		if _, ok := known[field]; !ok {
			return unsupported(ProtocolGenerateContent, path+"."+field, "field is not supported by this cross-protocol route")
		}
	}
	return nil
}

func normalizeGeminiToolArguments(protocol Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return json.RawMessage(`{}`), nil
	}
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
		var value string
		if err := json.Unmarshal(raw, &value); err == nil && strings.TrimSpace(value) == "" {
			return json.RawMessage(`{}`), nil
		}
	}
	return normalizeArguments(protocol, path, raw)
}

func normalizeGeminiThinkingLevel(protocol Protocol, path, value string) (string, error) {
	level := strings.ToUpper(strings.TrimSpace(value))
	if level == "" {
		return "", nil
	}
	switch level {
	case "MINIMAL", "LOW", "MEDIUM", "HIGH":
		return level, nil
	default:
		return "", unsupported(protocol, path, "reasoning effort %q has no official Gemini thinkingLevel mapping", value)
	}
}

// normalizeGeminiJSONSchema validates the JSON Schema envelope without
// rewriting its dialect. responseJsonSchema uses JSON Schema (lower-case type
// names and JSON Schema keywords), unlike responseSchema's OpenAPI subset.
func normalizeGeminiJSONSchema(protocol Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	if !jsonValuePresent(raw) {
		return nil, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return nil, invalid(protocol, path, "schema must be a JSON object")
	}
	return append(json.RawMessage(nil), raw...), nil
}

// normalizeGeminiTerminalEmptyTextParts accepts the terminal empty-text Part
// emitted by the Gemini streaming API while retaining fail-closed validation
// for an actually empty object. The empty text is only a stream terminator and
// carries no canonical content, so it is removed before ordinary Part checks.
func validateGeminiPart(part geminiPart, path string) error {
	payloads := 0
	if part.Text != "" || part.Thought {
		payloads++
	}
	for _, present := range []bool{part.InlineData != nil, part.FileData != nil, part.FunctionCall != nil, part.FunctionResponse != nil, jsonValuePresent(part.ExecutableCode), jsonValuePresent(part.CodeExecutionResult)} {
		if present {
			payloads++
		}
	}
	if payloads == 0 {
		return unsupported(ProtocolGenerateContent, path, "unknown or empty Part")
	}
	if payloads != 1 {
		return invalid(ProtocolGenerateContent, path, "Part must contain exactly one data field")
	}
	if jsonValuePresent(part.MediaResolution) || jsonValuePresent(part.VideoMetadata) {
		return unsupported(ProtocolGenerateContent, path, "per-part media metadata is not portable")
	}
	if jsonValuePresent(part.ExecutableCode) || jsonValuePresent(part.CodeExecutionResult) {
		return unsupported(ProtocolGenerateContent, path, "code execution parts require a native Gemini provider")
	}
	if part.InlineData != nil {
		if part.InlineData.MIMEType == "" || part.InlineData.Data == "" {
			return invalid(ProtocolGenerateContent, path+".inlineData", "mimeType and data are required")
		}
		if _, err := base64.StdEncoding.DecodeString(part.InlineData.Data); err != nil {
			return invalid(ProtocolGenerateContent, path+".inlineData.data", "must be valid base64")
		}
	}
	if part.FileData != nil && part.FileData.FileURI == "" {
		return invalid(ProtocolGenerateContent, path+".fileData.fileUri", "fileUri is required")
	}
	if part.FunctionResponse != nil && (jsonValuePresent(part.FunctionResponse.WillContinue) || jsonValuePresent(part.FunctionResponse.Scheduling) || jsonValuePresent(part.FunctionResponse.Parts)) {
		return unsupported(ProtocolGenerateContent, path+".functionResponse", "streaming or multimodal function responses require a native Gemini provider")
	}
	return nil
}

func encodeGeminiParts(parts []portablePart) ([]geminiPart, error) {
	converted := make([]geminiPart, 0, len(parts))
	for _, part := range parts {
		switch part.Kind {
		case partText, partRefusal:
			converted = append(converted, geminiPart{Text: part.Text})
		case partReasoning:
			converted = append(converted, geminiPart{Text: part.Text, Thought: true, ThoughtSignature: part.Opaque})
		case partImage, partAudio, partFile:
			if part.Media == nil {
				return nil, invalid(ProtocolGenerateContent, "$.contents.parts", "nil media")
			}
			if part.Media.Detail != "" {
				return nil, unsupported(ProtocolGenerateContent, "$.contents.parts", "image detail cannot be represented by generateContent")
			}
			if part.Media.URL == "" && part.Media.Data == "" {
				return nil, unsupported(ProtocolGenerateContent, "$.contents.parts", "file-id-only media cannot be represented by generateContent")
			}
			if part.Media.Filename != "" {
				return nil, unsupported(ProtocolGenerateContent, "$.contents.parts", "file names cannot be represented by generateContent parts")
			}
			if part.Media.Data != "" {
				converted = append(converted, geminiPart{InlineData: &geminiBlob{MIMEType: part.Media.MIMEType, Data: part.Media.Data}})
			} else {
				converted = append(converted, geminiPart{FileData: &geminiFileData{MIMEType: part.Media.MIMEType, FileURI: part.Media.URL}})
			}
		case partToolCall:
			if part.ToolCall == nil {
				return nil, invalid(ProtocolGenerateContent, "$.contents.parts", "nil tool call")
			}
			converted = append(converted, geminiPart{FunctionCall: &geminiFunctionCall{ID: part.ToolCall.ID, Name: part.ToolCall.Name, Args: part.ToolCall.Arguments}, ThoughtSignature: geminiThoughtSignatureBypass})
		case partToolResult:
			if part.ToolResult == nil {
				return nil, invalid(ProtocolGenerateContent, "$.contents.parts", "nil tool result")
			}
			var value any
			text := joinText(part.ToolResult.Content)
			if json.Unmarshal([]byte(text), &value) != nil {
				value = map[string]any{"result": text}
			}
			raw, _ := json.Marshal(value)
			name := part.ToolResult.Name
			if name == "" {
				return nil, unsupported(ProtocolGenerateContent, "$.contents.parts.functionResponse.name", "tool result name cannot be inferred")
			}
			converted = append(converted, geminiPart{FunctionResponse: &geminiFunctionResponse{ID: part.ToolResult.CallID, Name: name, Response: raw}})
		default:
			return nil, unsupported(ProtocolGenerateContent, "$.contents.parts", "part %q cannot be encoded", part.Kind)
		}
	}
	return converted, nil
}

func normalizeGeminiSchema(raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := geminischema.NormalizeParameters(raw, geminischema.Limits{})
	if err == nil {
		return normalized, nil
	}
	path, reason := "$.tools.parameters", err.Error()
	var violation *geminischema.Violation
	if errors.As(err, &violation) {
		path += strings.TrimPrefix(violation.Path, "$")
		reason = violation.Reason
	}
	if errors.Is(err, geminischema.ErrUnsupportedKeyword) {
		return nil, unsupported(ProtocolGenerateContent, path, "%s", reason)
	}
	return nil, invalid(ProtocolGenerateContent, path, "%s", reason)
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

func geminiStop(value finishReason) string {
	switch value {
	case finishLength:
		return "MAX_TOKENS"
	case finishContentFilter:
		return "SAFETY"
	case finishError:
		return "OTHER"
	default:
		return "STOP"
	}
}
