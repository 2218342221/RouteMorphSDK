package messagesgemini

import (
	"context"
	"fmt"
)

// messagesToGeminiConverter converts Anthropic Messages requests directly to
// Gemini generateContent. Keeping this route direct is important: a Chat or
// Responses intermediary cannot reliably retain function-result names, media,
// stop sequences, or the two protocols' distinct cache/thinking semantics.
type messagesToGeminiConverter struct {
	spec     routeSpec
	buffered BufferedFactory
}

type geminiToMessagesConverter struct {
	spec     routeSpec
	buffered BufferedFactory
}

func (c *messagesToGeminiConverter) Specification() routeSpec { return c.spec }
func (c *geminiToMessagesConverter) Specification() routeSpec { return c.spec }

func (c *messagesToGeminiConverter) ToUpstreamRequest(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	if err := rejectUnknownTopLevel(ProtocolMessages, input, "model", "max_tokens", "messages", "system", "tools", "tool_choice", "temperature", "top_p", "stop_sequences", "stream", "thinking", "output_config", "metadata", "container"); err != nil {
		return conversionResult{}, err
	}
	var source messagesRequest
	if err := decodeJSON(ProtocolMessages, input, &source); err != nil {
		return conversionResult{}, err
	}
	if source.Model == "" {
		return conversionResult{}, invalid(ProtocolMessages, "$.model", "model is required")
	}
	if source.MaxTokens == 0 {
		return conversionResult{}, unsupported(ProtocolMessages, "$.max_tokens", "prompt-cache pre-warming with max_tokens=0 has no portable generateContent equivalent")
	}
	if source.MaxTokens < 0 {
		return conversionResult{}, invalid(ProtocolMessages, "$.max_tokens", "must not be negative")
	}
	if len(source.Messages) == 0 {
		return conversionResult{}, invalid(ProtocolMessages, "$.messages", "at least one message is required")
	}
	if jsonValuePresent(source.Container) {
		return conversionResult{}, unsupported(ProtocolMessages, "$.container", "container state requires a native Messages provider")
	}
	if len(source.Metadata) > 0 {
		return conversionResult{}, unsupported(ProtocolMessages, "$.metadata", "generateContent has no request metadata equivalent")
	}
	if source.Thinking != nil {
		if err := validateMessagesThinking(source.Thinking, "$.thinking"); err != nil {
			return conversionResult{}, err
		}
		return conversionResult{}, unsupported(ProtocolMessages, "$.thinking", "Messages thinking policy is not semantically equivalent to Gemini thinkingConfig")
	}

	target := geminiRequest{}
	var diagnostics []Diagnostic
	if source.OutputConfig != nil {
		if source.OutputConfig.Effort != "" {
			return conversionResult{}, unsupported(ProtocolMessages, "$.output_config.effort", "Messages reasoning effort is not semantically equivalent to Gemini thinkingConfig")
		}
		if source.OutputConfig.Format != nil {
			if source.OutputConfig.Format.Type != "json_schema" {
				return conversionResult{}, unsupported(ProtocolMessages, "$.output_config.format.type", "format %q is not portable", source.OutputConfig.Format.Type)
			}
			if !jsonValuePresent(source.OutputConfig.Format.Schema) {
				return conversionResult{}, invalid(ProtocolMessages, "$.output_config.format.schema", "schema is required")
			}
			schema, err := normalizeGeminiJSONSchema(ProtocolMessages, "$.output_config.format.schema", source.OutputConfig.Format.Schema)
			if err != nil {
				return conversionResult{}, err
			}
			target.GenerationConfig = &geminiGenerationConfig{ResponseMIMEType: "application/json", ResponseJSONSchema: schema}
		}
	}
	if target.GenerationConfig == nil {
		target.GenerationConfig = &geminiGenerationConfig{}
	}
	target.GenerationConfig.MaxOutputTokens = &source.MaxTokens
	target.GenerationConfig.Temperature = source.Temperature
	target.GenerationConfig.TopP = source.TopP
	target.GenerationConfig.StopSequences = append([]string(nil), source.StopSequences...)

	if jsonValuePresent(source.System) {
		blocks, err := decodeMessagesBlocks(source.System, "$.system")
		if err != nil {
			return conversionResult{}, err
		}
		parts, blockDiagnostics, err := messagesBlocksToGemini(blocks, "system", "$.system", nil)
		if err != nil {
			return conversionResult{}, err
		}
		for index, part := range parts {
			if part.Text == "" || part.Thought || part.FunctionCall != nil || part.FunctionResponse != nil || part.InlineData != nil || part.FileData != nil {
				return conversionResult{}, unsupported(ProtocolMessages, fmt.Sprintf("$.system[%d]", index), "Gemini systemInstruction only has a portable text mapping")
			}
		}
		diagnostics = append(diagnostics, blockDiagnostics...)
		target.SystemInstruction = &geminiContent{Parts: parts}
	}

	if len(source.Tools) > 0 {
		tool := geminiTool{}
		for index, function := range source.Tools {
			path := fmt.Sprintf("$.tools[%d]", index)
			if jsonValuePresent(function.CacheControl) {
				return conversionResult{}, unsupported(ProtocolMessages, path+".cache_control", "tool cache control has no Gemini equivalent")
			}
			if function.Type != "" && function.Type != "custom" {
				return conversionResult{}, unsupported(ProtocolMessages, path+".type", "server tool %q requires a native Messages provider", function.Type)
			}
			if function.Name == "" {
				return conversionResult{}, invalid(ProtocolMessages, path+".name", "name is required")
			}
			if function.Strict != nil {
				return conversionResult{}, unsupported(ProtocolMessages, path+".strict", "Gemini function declarations cannot preserve Anthropic strict-tool semantics")
			}
			schema, err := normalizeGeminiSchema(function.InputSchema)
			if err != nil {
				return conversionResult{}, err
			}
			tool.FunctionDeclarations = append(tool.FunctionDeclarations, geminiFunctionDeclaration{Name: function.Name, Description: function.Description, Parameters: schema})
		}
		target.Tools = []geminiTool{tool}
	}

	choice, parallel, err := decodeMessagesToolChoice(source.ToolChoice)
	if err != nil {
		return conversionResult{}, err
	}
	if parallel != nil {
		return conversionResult{}, unsupported(ProtocolMessages, "$.tool_choice.disable_parallel_tool_use", "Gemini cannot preserve the parallel-tool-use constraint")
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
		default:
			return conversionResult{}, unsupported(ProtocolMessages, "$.tool_choice", "tool choice %q is not portable", choice.Mode)
		}
	}

	callNames := make(map[string]string)
	for messageIndex, message := range source.Messages {
		path := fmt.Sprintf("$.messages[%d]", messageIndex)
		if message.Role != "user" && message.Role != "assistant" {
			return conversionResult{}, invalid(ProtocolMessages, path+".role", "role must be user or assistant")
		}
		blocks, err := decodeMessagesBlocks(message.Content, path+".content")
		if err != nil {
			return conversionResult{}, err
		}
		parts, blockDiagnostics, err := messagesBlocksToGemini(blocks, message.Role, path+".content", callNames)
		if err != nil {
			return conversionResult{}, err
		}
		diagnostics = append(diagnostics, blockDiagnostics...)
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		appendGeminiContent(&target, role, parts...)
	}
	if len(target.Contents) == 0 {
		return conversionResult{}, invalid(ProtocolMessages, "$.messages", "at least one portable content block is required")
	}
	body, err := marshal(ProtocolGenerateContent, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *messagesToGeminiConverter) ToClientResponse(_ context.Context, input []byte, options conversionOptions) (conversionResult, error) {
	var source geminiResponse
	if err := decodeJSON(ProtocolGenerateContent, input, &source); err != nil {
		return conversionResult{}, err
	}
	diagnostics, err := validateGeminiResponseEnvelope(&source, options.LossPolicy)
	if err != nil {
		return conversionResult{}, err
	}
	if source.ResponseID == "" {
		return conversionResult{}, upstreamResponseError(ProtocolGenerateContent, "$.responseId", "responseId is required for a Messages response")
	}
	model := source.ModelVersion
	if options.Exchange.ClientModel != "" {
		model = options.Exchange.ClientModel
	}
	if model == "" {
		return conversionResult{}, upstreamResponseError(ProtocolGenerateContent, "$.modelVersion", "modelVersion is required for a Messages response")
	}
	promptTokens := source.UsageMetadata.PromptTokenCount + source.UsageMetadata.ToolUsePromptTokenCount
	if source.UsageMetadata.PromptTokenCount < 0 || source.UsageMetadata.ToolUsePromptTokenCount < 0 || source.UsageMetadata.CandidatesTokenCount < 0 || source.UsageMetadata.ThoughtsTokenCount < 0 || source.UsageMetadata.CachedContentTokenCount < 0 || source.UsageMetadata.TotalTokenCount < 0 {
		return conversionResult{}, upstreamResponseError(ProtocolGenerateContent, "$.usageMetadata", "token counts must not be negative")
	}
	if source.UsageMetadata.CachedContentTokenCount > promptTokens {
		return conversionResult{}, upstreamResponseError(ProtocolGenerateContent, "$.usageMetadata.cachedContentTokenCount", "cached tokens cannot exceed inclusive prompt tokens")
	}
	candidate := source.Candidates[0]
	if candidate.Content.Role != "" && candidate.Content.Role != "model" {
		return conversionResult{}, upstreamResponseError(ProtocolGenerateContent, "$.candidates[0].content.role", "expected model role, got %q", candidate.Content.Role)
	}
	blocks, blockDiagnostics, hasToolCall, err := geminiPartsToMessages(candidate.Content.Parts, "assistant", "$.candidates[0].content.parts", nil)
	if err != nil {
		return conversionResult{}, err
	}
	diagnostics = append(diagnostics, blockDiagnostics...)
	finish, err := parseGeminiFinish(candidate.FinishReason)
	if err != nil {
		return conversionResult{}, err
	}
	if finish == finishStop && hasToolCall {
		finish = finishToolCalls
	}
	var target messagesResponse
	target.ID, target.Type, target.Role, target.Model = source.ResponseID, "message", "assistant", model
	target.Content = mustJSON(blocks)
	target.StopReason = messagesStop(finish)
	// Gemini prompt counts are inclusive of cached content. Anthropic reports
	// ordinary input and cache reads in disjoint fields.
	target.Usage.InputTokens = promptTokens - source.UsageMetadata.CachedContentTokenCount
	target.Usage.OutputTokens = source.UsageMetadata.CandidatesTokenCount + source.UsageMetadata.ThoughtsTokenCount
	target.Usage.CacheReadInputTokens = source.UsageMetadata.CachedContentTokenCount
	body, err := marshal(ProtocolMessages, target)
	return conversionResult{Body: body, Diagnostics: diagnostics}, err
}

func (c *messagesToGeminiConverter) NewClientStream(_ context.Context, options conversionOptions) (responseStreamConverter, error) {
	return c.buffered(c.spec, options, c.ToClientResponse), nil
}
