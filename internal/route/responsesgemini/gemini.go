package responsesgemini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	geminischema "github.com/2218342221/RouteMorphSDK/internal/geminischema"
	geminiwire "github.com/2218342221/RouteMorphSDK/internal/wire/gemini"
)

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
			"responseModalities", "thinkingConfig", "speechConfig", "imageConfig", "enableAffectiveDialog",
			"responseFormat", "translationConfig", "audioTranscriptionConfig", "_responseJsonSchema"); err != nil {
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
		if err := rejectGeminiObjectFields(raw, path, "functionDeclarations", "codeExecution", "googleSearch", "googleSearchRetrieval", "urlContext", "computerUse", "fileSearch", "googleMaps", "mcpServers"); err != nil {
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
			if err := rejectGeminiObjectFields(declaration, fmt.Sprintf("%s.functionDeclarations[%d]", path, j), "name", "description", "parameters", "parametersJsonSchema", "response", "responseJsonSchema", "behavior"); err != nil {
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
		if err := rejectGeminiObjectFields(part, partPath, "text", "inlineData", "fileData", "functionCall", "functionResponse", "thought", "thoughtSignature", "mediaResolution", "videoMetadata", "executableCode", "codeExecutionResult", "toolCall", "toolResponse", "partMetadata", "audioTranscription", "mediaProcessing"); err != nil {
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

func validateGeminiPortableRequest(source *geminiRequest) error {
	if source == nil || len(source.Contents) == 0 {
		return invalid(ProtocolGenerateContent, "$.contents", "at least one content is required")
	}
	if source.SystemInstruction != nil {
		if source.SystemInstruction.Role != "" && source.SystemInstruction.Role != "user" {
			return unsupported(ProtocolGenerateContent, "$.systemInstruction.role", "role %q is not portable", source.SystemInstruction.Role)
		}
		if len(source.SystemInstruction.Parts) == 0 {
			return invalid(ProtocolGenerateContent, "$.systemInstruction.parts", "at least one part is required")
		}
	}
	for i, content := range source.Contents {
		path := fmt.Sprintf("$.contents[%d]", i)
		if content.Role != "" && content.Role != "user" && content.Role != "model" {
			return unsupported(ProtocolGenerateContent, path+".role", "role %q is not portable", content.Role)
		}
		if len(content.Parts) == 0 {
			return invalid(ProtocolGenerateContent, path+".parts", "at least one part is required")
		}
	}
	declared := make(map[string]struct{})
	for i, tool := range source.Tools {
		path := fmt.Sprintf("$.tools[%d]", i)
		for _, field := range []struct {
			name string
			raw  json.RawMessage
		}{
			{"codeExecution", tool.CodeExecution}, {"googleSearch", tool.GoogleSearch}, {"googleSearchRetrieval", tool.GoogleSearchRetrieval},
			{"urlContext", tool.URLContext}, {"computerUse", tool.ComputerUse}, {"fileSearch", tool.FileSearch}, {"googleMaps", tool.GoogleMaps}, {"mcpServers", tool.MCPServers},
		} {
			if jsonValuePresent(field.raw) {
				return unsupported(ProtocolGenerateContent, path+"."+field.name, "built-in Gemini tools require a native provider")
			}
		}
		for j, function := range tool.FunctionDeclarations {
			functionPath := fmt.Sprintf("%s.functionDeclarations[%d]", path, j)
			if strings.TrimSpace(function.Name) == "" {
				return invalid(ProtocolGenerateContent, functionPath+".name", "name is required")
			}
			if _, exists := declared[function.Name]; exists {
				return invalid(ProtocolGenerateContent, functionPath+".name", "duplicate function name %q", function.Name)
			}
			declared[function.Name] = struct{}{}
			if nonNullJSON(function.Parameters) && nonNullJSON(function.ParametersJSONSchema) {
				return invalid(ProtocolGenerateContent, functionPath, "parameters and parametersJsonSchema are mutually exclusive")
			}
			if nonNullJSON(function.Parameters) {
				var schema map[string]any
				if err := json.Unmarshal(function.Parameters, &schema); err != nil || schema == nil {
					return invalid(ProtocolGenerateContent, functionPath+".parameters", "must be a JSON object")
				}
			}
			if nonNullJSON(function.ParametersJSONSchema) {
				if _, err := normalizeGeminiJSONSchema(ProtocolGenerateContent, functionPath+".parametersJsonSchema", function.ParametersJSONSchema); err != nil {
					return err
				}
			}
			if nonNullJSON(function.Response) && nonNullJSON(function.ResponseJSONSchema) {
				return invalid(ProtocolGenerateContent, functionPath, "response and responseJsonSchema are mutually exclusive")
			}
			if nonNullJSON(function.Response) {
				return unsupported(ProtocolGenerateContent, functionPath+".response", "function response schemas have no portable target-protocol equivalent")
			}
			if nonNullJSON(function.ResponseJSONSchema) {
				if _, err := normalizeGeminiJSONSchema(ProtocolGenerateContent, functionPath+".responseJsonSchema", function.ResponseJSONSchema); err != nil {
					return err
				}
			}
			if function.Behavior != "" {
				return unsupported(ProtocolGenerateContent, functionPath+".behavior", "function behavior has no portable target-protocol equivalent")
			}
		}
	}
	if source.ToolConfig != nil {
		if jsonValuePresent(source.ToolConfig.RetrievalConfig) || source.ToolConfig.IncludeServerSideToolInvocations != nil {
			return unsupported(ProtocolGenerateContent, "$.toolConfig", "retrieval and server-side tool invocation settings require a native provider")
		}
		for i, name := range source.ToolConfig.FunctionCallingConfig.AllowedFunctionNames {
			if _, ok := declared[name]; !ok {
				return invalid(ProtocolGenerateContent, fmt.Sprintf("$.toolConfig.functionCallingConfig.allowedFunctionNames[%d]", i), "function %q is not declared", name)
			}
		}
	}
	return validateGeminiPortableGenerationConfig(source.GenerationConfig)
}

func validateGeminiPortableGenerationConfig(config *geminiGenerationConfig) error {
	if config == nil {
		return nil
	}
	unsupportedFields := []struct {
		path string
		set  bool
	}{
		{"$.generationConfig.topK", config.TopK != nil},
		{"$.generationConfig.candidateCount", config.CandidateCount != nil && *config.CandidateCount != 1},
		{"$.generationConfig.presencePenalty", config.PresencePenalty != nil},
		{"$.generationConfig.frequencyPenalty", config.FrequencyPenalty != nil},
		{"$.generationConfig.responseLogprobs", config.ResponseLogprobs != nil},
		{"$.generationConfig.logprobs", config.Logprobs != nil},
		{"$.generationConfig.enableEnhancedCivicAnswers", config.EnableEnhancedCivicAnswers != nil},
		{"$.generationConfig.mediaResolution", jsonValuePresent(config.MediaResolution)},
		{"$.generationConfig.seed", config.Seed != nil},
		{"$.generationConfig.responseModalities", len(config.ResponseModalities) > 0},
		{"$.generationConfig.speechConfig", jsonValuePresent(config.SpeechConfig)},
		{"$.generationConfig.imageConfig", jsonValuePresent(config.ImageConfig)},
		{"$.generationConfig.enableAffectiveDialog", config.EnableAffectiveDialog != nil},
		{"$.generationConfig.responseFormat", jsonValuePresent(config.ResponseFormat)},
		{"$.generationConfig.translationConfig", jsonValuePresent(config.TranslationConfig)},
		{"$.generationConfig.audioTranscriptionConfig", jsonValuePresent(config.AudioTranscriptionConfig)},
		{"$.generationConfig._responseJsonSchema", jsonValuePresent(config.InternalResponseJSONSchema)},
	}
	for _, field := range unsupportedFields {
		if field.set {
			return unsupported(ProtocolGenerateContent, field.path, "generation setting has no portable equivalent")
		}
	}
	if nonNullJSON(config.ResponseSchema) && nonNullJSON(config.ResponseJSONSchema) {
		return invalid(ProtocolGenerateContent, "$.generationConfig", "responseSchema and responseJsonSchema are mutually exclusive")
	}
	if (nonNullJSON(config.ResponseSchema) || nonNullJSON(config.ResponseJSONSchema)) && config.ResponseMIMEType != "application/json" {
		return invalid(ProtocolGenerateContent, "$.generationConfig.responseMimeType", "application/json is required when a response schema is set")
	}
	if config.ResponseMIMEType != "" && config.ResponseMIMEType != "text/plain" && config.ResponseMIMEType != "application/json" {
		return unsupported(ProtocolGenerateContent, "$.generationConfig.responseMimeType", "MIME type %q is not portable", config.ResponseMIMEType)
	}
	if config.ThinkingConfig != nil {
		if _, err := normalizeGeminiThinkingLevel(ProtocolGenerateContent, "$.generationConfig.thinkingConfig.thinkingLevel", config.ThinkingConfig.ThinkingLevel); err != nil {
			return err
		}
	}
	return nil
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
	if !nonNullJSON(raw) {
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

func validateGeminiPart(part geminiPart, path string) error {
	for _, field := range []struct {
		path string
		set  bool
	}{
		{path + ".toolCall", jsonValuePresent(part.ToolCall)},
		{path + ".toolResponse", jsonValuePresent(part.ToolResponse)},
		{path + ".partMetadata", jsonValuePresent(part.PartMetadata)},
		{path + ".audioTranscription", jsonValuePresent(part.AudioTranscription)},
		{path + ".mediaProcessing", part.MediaProcessing != ""},
	} {
		if field.set {
			return unsupported(ProtocolGenerateContent, field.path, "agent tool and media-processing parts require a native Gemini provider")
		}
	}
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
		if part.InlineData.DisplayName != "" {
			return unsupported(ProtocolGenerateContent, path+".inlineData.displayName", "displayName is not supported by Gemini generateContent")
		}
		if part.InlineData.MIMEType == "" || part.InlineData.Data == "" {
			return invalid(ProtocolGenerateContent, path+".inlineData", "mimeType and data are required")
		}
		if !validMIMEType(part.InlineData.MIMEType) {
			return invalid(ProtocolGenerateContent, path+".inlineData.mimeType", "must be a bare IANA media type")
		}
		if !validBase64(part.InlineData.Data) {
			return invalid(ProtocolGenerateContent, path+".inlineData.data", "must be valid base64")
		}
	}
	if part.FileData != nil {
		if part.FileData.DisplayName != "" {
			return unsupported(ProtocolGenerateContent, path+".fileData.displayName", "displayName is not supported by Gemini generateContent")
		}
		if part.FileData.FileURI == "" || part.FileData.MIMEType == "" {
			return invalid(ProtocolGenerateContent, path+".fileData", "fileUri and mimeType are required")
		}
		if !validMIMEType(part.FileData.MIMEType) {
			return invalid(ProtocolGenerateContent, path+".fileData.mimeType", "must be a bare IANA media type")
		}
	}
	if part.FunctionResponse != nil && (jsonValuePresent(part.FunctionResponse.WillContinue) || jsonValuePresent(part.FunctionResponse.Scheduling) || jsonValuePresent(part.FunctionResponse.Parts)) {
		return unsupported(ProtocolGenerateContent, path+".functionResponse", "streaming or multimodal function responses require a native Gemini provider")
	}
	if part.FunctionCall != nil {
		if strings.TrimSpace(part.FunctionCall.Name) == "" {
			return invalid(ProtocolGenerateContent, path+".functionCall.name", "name is required")
		}
		if _, err := normalizeGeminiToolArguments(ProtocolGenerateContent, path+".functionCall.args", part.FunctionCall.Args); err != nil {
			return err
		}
	}
	if part.FunctionResponse != nil {
		if strings.TrimSpace(part.FunctionResponse.Name) == "" {
			return invalid(ProtocolGenerateContent, path+".functionResponse.name", "name is required")
		}
		var response map[string]any
		if err := json.Unmarshal(part.FunctionResponse.Response, &response); err != nil || response == nil {
			return invalid(ProtocolGenerateContent, path+".functionResponse.response", "must be a JSON object")
		}
	}
	return nil
}

func decodeGeminiParts(source []geminiPart, path string) ([]portablePart, error) {
	parts := make([]portablePart, 0, len(source))
	for i, part := range source {
		partPath := fmt.Sprintf("%s[%d]", path, i)
		if err := validateGeminiPart(part, partPath); err != nil {
			return nil, err
		}
		if part.ThoughtSignature != "" {
			return nil, unsupported(ProtocolGenerateContent, partPath+".thoughtSignature", "provider-issued Gemini thought signatures cannot be represented by the target protocol and must not be dropped")
		}
		switch {
		case part.FunctionCall != nil:
			if part.FunctionCall.Name == "" {
				return nil, invalid(ProtocolGenerateContent, fmt.Sprintf("%s[%d].functionCall.name", path, i), "name is required")
			}
			arguments, err := normalizeGeminiToolArguments(ProtocolGenerateContent, partPath+".functionCall.args", part.FunctionCall.Args)
			if err != nil {
				return nil, err
			}
			parts = append(parts, portablePart{Kind: partToolCall, ToolCall: &portableToolCall{ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Arguments: arguments}})
		case part.FunctionResponse != nil:
			if part.FunctionResponse.Name == "" {
				return nil, invalid(ProtocolGenerateContent, fmt.Sprintf("%s[%d].functionResponse.name", path, i), "name is required")
			}
			parts = append(parts, portablePart{Kind: partToolResult, ToolResult: &portableToolResult{CallID: part.FunctionResponse.ID, Name: part.FunctionResponse.Name, Content: textParts(rawString(part.FunctionResponse.Response))}})
		case part.InlineData != nil:
			kind := partImage
			mimeType := strings.ToLower(part.InlineData.MIMEType)
			if strings.HasPrefix(mimeType, "audio/") {
				kind = partAudio
			} else if strings.HasPrefix(mimeType, "image/") {
				if !validImageMIMEType(mimeType) {
					return nil, unsupported(ProtocolGenerateContent, partPath+".inlineData.mimeType", "Responses images require JPEG, PNG, GIF, or WebP")
				}
				if part.InlineData.DisplayName != "" {
					return nil, unsupported(ProtocolGenerateContent, partPath+".inlineData.displayName", "Responses image content cannot preserve a display name")
				}
			} else {
				kind = partFile
			}
			parts = append(parts, portablePart{Kind: kind, Media: &portableMedia{MIMEType: part.InlineData.MIMEType, Data: part.InlineData.Data}})
		case part.FileData != nil:
			if !portableGeminiFileURI(part.FileData.FileURI) {
				return nil, unsupported(ProtocolGenerateContent, partPath+".fileData.fileUri", "provider-scoped Gemini file URIs cannot be represented by Responses")
			}
			mimeType := strings.ToLower(part.FileData.MIMEType)
			kind := partFile
			switch {
			case strings.HasPrefix(mimeType, "image/"):
				if !validImageMIMEType(mimeType) {
					return nil, unsupported(ProtocolGenerateContent, partPath+".fileData.mimeType", "Responses images require JPEG, PNG, GIF, or WebP")
				}
				kind = partImage
			case strings.HasPrefix(mimeType, "audio/"):
				return nil, unsupported(ProtocolGenerateContent, partPath+".fileData.mimeType", "Responses Create message content has no input_audio part")
			}
			parts = append(parts, portablePart{Kind: kind, Media: &portableMedia{MIMEType: part.FileData.MIMEType, URL: part.FileData.FileURI}})
		case part.Thought:
			parts = append(parts, portablePart{Kind: partReasoning, Text: part.Text, Opaque: part.ThoughtSignature})
		case part.Text != "":
			parts = append(parts, portablePart{Kind: partText, Text: part.Text})
		default:
			return nil, unsupported(ProtocolGenerateContent, fmt.Sprintf("%s[%d]", path, i), "unknown or empty Part")
		}
	}
	return parts, nil
}

func encodeGeminiParts(parts []portablePart) ([]geminiPart, error) {
	converted := make([]geminiPart, 0, len(parts))
	for _, part := range parts {
		switch part.Kind {
		case partText, partRefusal:
			converted = append(converted, geminiPart{Text: part.Text})
		case partReasoning:
			if part.Opaque != "" {
				return nil, unsupported(ProtocolGenerateContent, "$.contents.parts.thoughtSignature", "only provider-issued Gemini thought signatures may be sent to Gemini")
			}
			converted = append(converted, geminiPart{Text: part.Text, Thought: true})
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
				return nil, unsupported(ProtocolGenerateContent, "$.contents.parts", "Gemini generateContent cannot preserve file names")
			}
			mimeType := part.Media.MIMEType
			if mimeType == "" && part.Media.URL != "" {
				mimeType = mimeTypeFromURL(part.Media.URL)
			}
			if !validMIMEType(mimeType) {
				return nil, unsupported(ProtocolGenerateContent, "$.contents.parts", "Gemini media requires a known bare IANA media type")
			}
			if part.Kind == partImage && !validImageMIMEType(mimeType) {
				return nil, unsupported(ProtocolGenerateContent, "$.contents.parts", "Gemini image conversion requires JPEG, PNG, GIF, or WebP")
			}
			if part.Media.Data != "" {
				if !validBase64(part.Media.Data) {
					return nil, invalid(ProtocolGenerateContent, "$.contents.parts.inlineData.data", "must be valid base64")
				}
				converted = append(converted, geminiPart{InlineData: &geminiBlob{MIMEType: mimeType, Data: part.Media.Data}})
			} else {
				if !geminiFileURI(part.Media.URL) {
					return nil, unsupported(ProtocolGenerateContent, "$.contents.parts.fileData.fileUri", "Gemini fileData requires a Gemini Files or Google Cloud Storage URI")
				}
				converted = append(converted, geminiPart{FileData: &geminiFileData{MIMEType: mimeType, FileURI: part.Media.URL}})
			}
		case partToolCall:
			if part.ToolCall == nil {
				return nil, invalid(ProtocolGenerateContent, "$.contents.parts", "nil tool call")
			}
			converted = append(converted, geminiPart{FunctionCall: &geminiFunctionCall{ID: part.ToolCall.ID, Name: part.ToolCall.Name, Args: part.ToolCall.Arguments}})
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

func normalizeGeminiFunctionSchema(protocol Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := geminischema.PreserveParametersJSONSchema(raw, geminischema.Limits{})
	if err == nil {
		return normalized, nil
	}
	reason := err.Error()
	var violation *geminischema.Violation
	if errors.As(err, &violation) {
		path += strings.TrimPrefix(violation.Path, "$")
		reason = violation.Reason
	}
	if errors.Is(err, geminischema.ErrUnsupportedKeyword) {
		return nil, unsupported(protocol, path, "%s", reason)
	}
	return nil, invalid(protocol, path, "%s", reason)
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
	if candidate.Content.Role != "" && candidate.Content.Role != "model" {
		return nil, upstreamResponseError(ProtocolGenerateContent, "$.candidates[0].content.role", "expected model role, got %q", candidate.Content.Role)
	}
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
		{"$.candidates[0].groundingAttributions", candidate.GroundingAttributions},
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
	if candidate.TokenCount != nil {
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_candidate_token_count_not_representable", "$.candidates[0].tokenCount", "per-candidate Gemini token count is not represented by the target protocol")
	}
	if candidate.FinishMessage != "" {
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_finish_message_not_representable", "$.candidates[0].finishMessage", "Gemini finish message is not represented by the target protocol")
	}
	if jsonValuePresent(source.ModelStatus) {
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_model_status_not_representable", "$.modelStatus", "Gemini model status is not represented by the target protocol")
	}
	if source.UsageMetadata.ServiceTier != "" {
		diagnostics = appendDiagnostic(diagnostics, "warning", "gemini_usage_service_tier_not_representable", "$.usageMetadata.serviceTier", "Gemini usage service tier is not represented by the target protocol")
	}
	if jsonValuePresent(source.UsageMetadata.PromptTokensDetails) || jsonValuePresent(source.UsageMetadata.ToolUsePromptTokensDetails) || jsonValuePresent(source.UsageMetadata.CandidatesTokensDetails) || jsonValuePresent(source.UsageMetadata.CacheTokensDetails) {
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
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "RECITATION", "LANGUAGE", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_OTHER", "NO_IMAGE", "IMAGE_RECITATION", "ESCALATION", "PUP_LIMITED_DISABLED":
		return finishContentFilter, nil
	case "MALFORMED_FUNCTION_CALL", "MALFORMED_RESPONSE", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "MISSING_THOUGHT_SIGNATURE", "OTHER":
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
