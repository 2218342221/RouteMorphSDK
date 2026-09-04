package relay

import (
	"bytes"
	"encoding/json"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
	geminiwire "github.com/2218342221/RouteMorphSDK/internal/wire/gemini"
)

// These DTOs deliberately describe known fields only. json.Unmarshal rejects
// a wrong JSON type for a known field while continuing to ignore new fields,
// which keeps native pass-through forward compatible.
type nativeChatChunk struct {
	ID                string              `json:"id"`
	Object            string              `json:"object"`
	Created           int64               `json:"created"`
	Model             string              `json:"model"`
	SystemFingerprint string              `json:"system_fingerprint"`
	ServiceTier       string              `json:"service_tier"`
	Choices           []*nativeChatChoice `json:"choices"`
	Usage             *nativeChatUsage    `json:"usage"`
}

type nativeChatChoice struct {
	Index        *int             `json:"index"`
	Delta        *nativeChatDelta `json:"delta"`
	FinishReason string           `json:"finish_reason"`
	Logprobs     *struct {
		Content []nativeChatLogprob `json:"content"`
		Refusal []nativeChatLogprob `json:"refusal"`
	} `json:"logprobs"`
}

type nativeChatDelta struct {
	Role             string                `json:"role"`
	Content          *string               `json:"content"`
	ReasoningContent *string               `json:"reasoning_content"`
	Refusal          *string               `json:"refusal"`
	ToolCalls        []*nativeChatToolCall `json:"tool_calls"`
	FunctionCall     *nativeChatFunction   `json:"function_call"`
	Audio            *nativeChatAudioDelta `json:"audio"`
}

type nativeChatToolCall struct {
	Index    *int                `json:"index"`
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function *nativeChatFunction `json:"function"`
}

type nativeChatFunction struct {
	Name      string  `json:"name"`
	Arguments *string `json:"arguments"`
}

type nativeChatAudioDelta struct {
	ID         string `json:"id"`
	Data       string `json:"data"`
	Transcript string `json:"transcript"`
	ExpiresAt  int64  `json:"expires_at"`
}

type nativeChatLogprob struct {
	Token       string              `json:"token"`
	Logprob     float64             `json:"logprob"`
	Bytes       []int               `json:"bytes"`
	TopLogprobs []nativeChatLogprob `json:"top_logprobs"`
}

type nativeChatUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	PromptDetails    struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens          int64 `json:"reasoning_tokens"`
		AcceptedPredictionTokens int64 `json:"accepted_prediction_tokens"`
		RejectedPredictionTokens int64 `json:"rejected_prediction_tokens"`
		AudioTokens              int64 `json:"audio_tokens"`
	} `json:"completion_tokens_details"`
}

type nativeMessagesEvent struct {
	Type         string                      `json:"type"`
	Index        *int                        `json:"index"`
	Message      *nativeMessagesStartMessage `json:"message"`
	ContentBlock *nativeMessagesContentBlock `json:"content_block"`
	Delta        *nativeMessagesDelta        `json:"delta"`
	Usage        *nativeMessagesUsage        `json:"usage"`
	Error        *nativeMessagesError        `json:"error"`
}

type nativeMessagesStartMessage struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"`
	Role         string               `json:"role"`
	Model        string               `json:"model"`
	Content      *[]json.RawMessage   `json:"content"`
	StopReason   json.RawMessage      `json:"stop_reason"`
	StopSequence json.RawMessage      `json:"stop_sequence"`
	Usage        *nativeMessagesUsage `json:"usage"`
}

type nativeMessagesContentBlock struct {
	Type        string            `json:"type"`
	Text        json.RawMessage   `json:"text"`
	Thinking    json.RawMessage   `json:"thinking"`
	Signature   json.RawMessage   `json:"signature"`
	Data        json.RawMessage   `json:"data"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Input       json.RawMessage   `json:"input"`
	ToolUseID   string            `json:"tool_use_id"`
	Content     json.RawMessage   `json:"content"`
	IsError     bool              `json:"is_error"`
	Citations   []json.RawMessage `json:"citations"`
	Caller      json.RawMessage   `json:"caller"`
	ToolsetName string            `json:"toolset_name"`
}

type nativeMessagesDelta struct {
	Type         string          `json:"type"`
	Text         json.RawMessage `json:"text"`
	Thinking     json.RawMessage `json:"thinking"`
	Signature    json.RawMessage `json:"signature"`
	PartialJSON  json.RawMessage `json:"partial_json"`
	StopReason   json.RawMessage `json:"stop_reason"`
	StopSequence json.RawMessage `json:"stop_sequence"`
	Citation     json.RawMessage `json:"citation"`
}

type nativeMessagesUsage struct {
	InputTokens              json.RawMessage `json:"input_tokens"`
	OutputTokens             json.RawMessage `json:"output_tokens"`
	CacheCreationInputTokens json.RawMessage `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     json.RawMessage `json:"cache_read_input_tokens"`
	ServerToolUse            json.RawMessage `json:"server_tool_use"`
}

type nativeMessagesError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type nativeProtocolError struct {
	Type    string          `json:"type"`
	Status  string          `json:"status"`
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
}

type nativeResponsesEvent struct {
	Type           string          `json:"type"`
	SequenceNumber *int64          `json:"sequence_number"`
	ItemID         string          `json:"item_id"`
	OutputIndex    *int            `json:"output_index"`
	ContentIndex   *int            `json:"content_index"`
	SummaryIndex   *int            `json:"summary_index"`
	Delta          json.RawMessage `json:"delta"`
	Text           json.RawMessage `json:"text"`
	Refusal        json.RawMessage `json:"refusal"`
	Arguments      json.RawMessage `json:"arguments"`
	Response       json.RawMessage `json:"response"`
	Item           json.RawMessage `json:"item"`
	Part           json.RawMessage `json:"part"`
	Error          json.RawMessage `json:"error"`
	Code           string          `json:"code"`
	Message        string          `json:"message"`
	Param          json.RawMessage `json:"param"`
}

type nativeResponsesResponse struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"`
	CreatedAt         int64                  `json:"created_at"`
	Model             string                 `json:"model"`
	Status            string                 `json:"status"`
	Error             *nativeResponsesError  `json:"error"`
	Output            []*nativeResponsesItem `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Usage *struct {
		InputTokens       int64 `json:"input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
		TotalTokens       int64 `json:"total_tokens"`
		InputTokenDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokenDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

type nativeResponsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type nativeResponsesItem struct {
	Type             string                        `json:"type"`
	Role             string                        `json:"role"`
	Content          []*nativeResponsesContentPart `json:"content"`
	ID               string                        `json:"id"`
	CallID           string                        `json:"call_id"`
	Name             string                        `json:"name"`
	Arguments        *string                       `json:"arguments"`
	Summary          []*nativeResponsesContentPart `json:"summary"`
	Status           string                        `json:"status"`
	Phase            string                        `json:"phase"`
	EncryptedContent string                        `json:"encrypted_content"`
}

type nativeResponsesContentPart struct {
	Type        string            `json:"type"`
	Text        string            `json:"text"`
	Refusal     string            `json:"refusal"`
	ImageURL    string            `json:"image_url"`
	FileID      string            `json:"file_id"`
	FileURL     string            `json:"file_url"`
	FileData    string            `json:"file_data"`
	Filename    string            `json:"filename"`
	Detail      string            `json:"detail"`
	Annotations []json.RawMessage `json:"annotations"`
	Logprobs    []json.RawMessage `json:"logprobs"`
	InputAudio  *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio"`
}

func decodeKnownNativeFields(protocol core.Protocol, data []byte, destination any) error {
	if err := json.Unmarshal(data, destination); err != nil {
		return core.Invalid(protocol, "$", "invalid known stream field type: %v", err)
	}
	return nil
}

func validateNativeProtocolError(protocol core.Protocol, raw json.RawMessage) error {
	if !rawJSONObject(raw) {
		return core.Invalid(protocol, "$.error", "error must be an object")
	}
	var protocolError nativeProtocolError
	return decodeKnownNativeFields(protocol, raw, &protocolError)
}

func validateKnownChatContainers(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return core.Invalid(core.ProtocolChat, "$", "invalid stream chunk")
	}
	var choices []json.RawMessage
	if err := json.Unmarshal(object["choices"], &choices); err != nil {
		return core.Invalid(core.ProtocolChat, "$.choices", "choices must be an array")
	}
	for _, rawChoice := range choices {
		if !rawJSONObject(rawChoice) {
			return core.Invalid(core.ProtocolChat, "$.choices[]", "choice must be an object")
		}
		var choice map[string]json.RawMessage
		_ = json.Unmarshal(rawChoice, &choice)
		var index int
		if !rawJSONPresent(choice["index"]) || json.Unmarshal(choice["index"], &index) != nil {
			return core.Invalid(core.ProtocolChat, "$.choices[].index", "index must be an integer")
		}
		if !rawJSONObject(choice["delta"]) {
			return core.Invalid(core.ProtocolChat, "$.choices[].delta", "delta must be an object")
		}
		var delta map[string]json.RawMessage
		_ = json.Unmarshal(choice["delta"], &delta)
		for _, field := range []string{"function_call", "audio"} {
			if raw, exists := delta[field]; exists && !rawJSONObject(raw) {
				return core.Invalid(core.ProtocolChat, "$.choices[].delta."+field, "must be an object")
			}
		}
		toolCalls, exists := delta["tool_calls"]
		if !exists {
			continue
		}
		if !rawJSONArray(toolCalls) {
			return core.Invalid(core.ProtocolChat, "$.choices[].delta.tool_calls", "must be an array")
		}
		var calls []json.RawMessage
		_ = json.Unmarshal(toolCalls, &calls)
		for _, rawCall := range calls {
			if !rawJSONObject(rawCall) {
				return core.Invalid(core.ProtocolChat, "$.choices[].delta.tool_calls[]", "tool call must be an object")
			}
			var call map[string]json.RawMessage
			_ = json.Unmarshal(rawCall, &call)
			if !rawJSONPresent(call["index"]) || json.Unmarshal(call["index"], &index) != nil {
				return core.Invalid(core.ProtocolChat, "$.choices[].delta.tool_calls[].index", "index must be an integer")
			}
			if function, exists := call["function"]; exists && !rawJSONObject(function) {
				return core.Invalid(core.ProtocolChat, "$.choices[].delta.tool_calls[].function", "must be an object")
			}
		}
	}
	if usage, exists := object["usage"]; exists && rawJSONPresent(usage) {
		if err := validateIntegerObjectFields(core.ProtocolChat, "$.usage", usage, "prompt_tokens", "completion_tokens", "total_tokens"); err != nil {
			return err
		}
		var usageObject map[string]json.RawMessage
		_ = json.Unmarshal(usage, &usageObject)
		if details := usageObject["prompt_tokens_details"]; rawJSONPresent(details) {
			if err := validateIntegerObjectFields(core.ProtocolChat, "$.usage.prompt_tokens_details", details, "cached_tokens"); err != nil {
				return err
			}
		}
		if details := usageObject["completion_tokens_details"]; rawJSONPresent(details) {
			if err := validateIntegerObjectFields(core.ProtocolChat, "$.usage.completion_tokens_details", details, "reasoning_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens", "audio_tokens"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateIntegerObjectFields(protocol core.Protocol, path string, raw json.RawMessage, fields ...string) error {
	if !rawJSONObject(raw) {
		return core.Invalid(protocol, path, "must be an object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return core.Invalid(protocol, path, "invalid object")
	}
	for _, field := range fields {
		value, exists := object[field]
		if !exists {
			continue
		}
		var integer int64
		if !rawJSONPresent(value) || json.Unmarshal(value, &integer) != nil {
			return core.Invalid(protocol, path+"."+field, "must be an integer")
		}
	}
	return nil
}

func requireJSONString(protocol core.Protocol, path string, raw json.RawMessage) (string, error) {
	if !rawJSONPresent(raw) {
		return "", core.Invalid(protocol, path, "string value is required")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", core.Invalid(protocol, path, "must be a string")
	}
	return value, nil
}

func validateOptionalNullableString(protocol core.Protocol, path string, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return core.Invalid(protocol, path, "must be a string or null")
	}
	return nil
}

func validateOptionalJSONObject(protocol core.Protocol, path string, raw json.RawMessage) error {
	if !rawJSONPresent(raw) {
		return nil
	}
	if !rawJSONObject(raw) {
		return core.Invalid(protocol, path, "must be an object")
	}
	return nil
}

func validateMessagesBlockPayload(protocol core.Protocol, block *nativeMessagesContentBlock) error {
	validateString := func(path string, raw json.RawMessage, required bool) error {
		if !required && len(bytes.TrimSpace(raw)) == 0 {
			return nil
		}
		_, err := requireJSONString(protocol, path, raw)
		return err
	}
	switch block.Type {
	case "text":
		return validateString("$.content_block.text", block.Text, false)
	case "thinking":
		if err := validateString("$.content_block.thinking", block.Thinking, false); err != nil {
			return err
		}
		return validateString("$.content_block.signature", block.Signature, false)
	case "redacted_thinking":
		return validateString("$.content_block.data", block.Data, false)
	case "tool_use", "server_tool_use", "mcp_tool_use":
		if !rawJSONObject(block.Input) {
			return core.Invalid(protocol, "$.content_block.input", "tool input must be an object")
		}
		return nil
	default:
		for path, raw := range map[string]json.RawMessage{
			"$.content_block.text": block.Text, "$.content_block.thinking": block.Thinking,
			"$.content_block.signature": block.Signature, "$.content_block.data": block.Data,
		} {
			if err := validateString(path, raw, false); err != nil {
				return err
			}
		}
		return nil
	}
}

func validateMessagesDeltaPayload(protocol core.Protocol, delta *nativeMessagesDelta) error {
	switch delta.Type {
	case "text_delta":
		_, err := requireJSONString(protocol, "$.delta.text", delta.Text)
		return err
	case "thinking_delta":
		_, err := requireJSONString(protocol, "$.delta.thinking", delta.Thinking)
		return err
	case "signature_delta":
		_, err := requireJSONString(protocol, "$.delta.signature", delta.Signature)
		return err
	case "input_json_delta":
		_, err := requireJSONString(protocol, "$.delta.partial_json", delta.PartialJSON)
		return err
	case "citations_delta":
		if !rawJSONObject(delta.Citation) {
			return core.Invalid(protocol, "$.delta.citation", "citation must be an object")
		}
	}
	return nil
}

func validateMessagesUsage(protocol core.Protocol, path string, usage *nativeMessagesUsage) error {
	if usage == nil {
		return core.Invalid(protocol, path, "usage must be an object")
	}
	for field, raw := range map[string]json.RawMessage{
		"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens,
		"cache_creation_input_tokens": usage.CacheCreationInputTokens,
		"cache_read_input_tokens":     usage.CacheReadInputTokens,
	} {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var value int64
		if !rawJSONPresent(raw) || json.Unmarshal(raw, &value) != nil {
			return core.Invalid(protocol, path+"."+field, "token count must be an integer")
		}
		if value < 0 {
			return core.UpstreamResponseError(protocol, path+"."+field, "token count must not be negative")
		}
	}
	if len(bytes.TrimSpace(usage.ServerToolUse)) > 0 && !rawJSONObject(usage.ServerToolUse) {
		return core.Invalid(protocol, path+".server_tool_use", "must be an object")
	}
	return nil
}

func validateKnownResponsesEvent(data []byte, eventType string) error {
	var event nativeResponsesEvent
	if err := decodeKnownNativeFields(core.ProtocolResponses, data, &event); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return core.Invalid(core.ProtocolResponses, "$", "invalid stream event")
	}
	for _, field := range []string{"item_id", "code", "message"} {
		if raw, exists := fields[field]; exists {
			if _, err := requireJSONString(core.ProtocolResponses, "$."+field, raw); err != nil {
				return err
			}
		}
	}
	for _, field := range []string{"sequence_number", "output_index", "content_index", "summary_index"} {
		if raw, exists := fields[field]; exists {
			var value int64
			if !rawJSONPresent(raw) || json.Unmarshal(raw, &value) != nil || value < 0 {
				return core.Invalid(core.ProtocolResponses, "$."+field, "must be a non-negative integer")
			}
		}
	}
	switch eventType {
	case "response.created", "response.completed", "response.incomplete", "response.failed", "response.cancelled":
		if !rawJSONObject(event.Response) {
			return core.Invalid(core.ProtocolResponses, "$.response", "response object is required")
		}
		if err := validateKnownResponsesResponse(event.Response); err != nil {
			return err
		}
	case "response.queued", "response.in_progress":
		if rawJSONPresent(event.Response) {
			if !rawJSONObject(event.Response) {
				return core.Invalid(core.ProtocolResponses, "$.response", "response must be an object")
			}
			if err := validateKnownResponsesResponse(event.Response); err != nil {
				return err
			}
		}
	case "response.output_item.added", "response.output_item.done":
		if !rawJSONObject(event.Item) {
			return core.Invalid(core.ProtocolResponses, "$.item", "item object is required")
		}
		if err := validateKnownResponsesItem(event.Item); err != nil {
			return err
		}
	case "response.content_part.added", "response.content_part.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		if !rawJSONObject(event.Part) {
			return core.Invalid(core.ProtocolResponses, "$.part", "part object is required")
		}
		if err := validateKnownResponsesPart(event.Part); err != nil {
			return err
		}
	case "response.output_text.delta", "response.refusal.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.function_call_arguments.delta":
		if _, err := requireJSONString(core.ProtocolResponses, "$.delta", event.Delta); err != nil {
			return err
		}
	case "response.output_text.done", "response.reasoning_summary_text.done", "response.reasoning_text.done":
		if _, err := requireJSONString(core.ProtocolResponses, "$.text", event.Text); err != nil {
			return err
		}
	case "response.refusal.done":
		if _, err := requireJSONString(core.ProtocolResponses, "$.refusal", event.Refusal); err != nil {
			return err
		}
	case "response.function_call_arguments.done":
		if _, err := requireJSONString(core.ProtocolResponses, "$.arguments", event.Arguments); err != nil {
			return err
		}
	case "error":
		if rawJSONPresent(event.Error) {
			if !rawJSONObject(event.Error) {
				return core.Invalid(core.ProtocolResponses, "$.error", "error must be an object")
			}
			var streamError nativeResponsesError
			if err := decodeKnownNativeFields(core.ProtocolResponses, event.Error, &streamError); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateKnownResponsesResponse(raw json.RawMessage) error {
	var response nativeResponsesResponse
	if err := decodeKnownNativeFields(core.ProtocolResponses, raw, &response); err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return core.Invalid(core.ProtocolResponses, "$.response", "invalid response object")
	}
	for _, field := range []string{"id", "object", "model", "status"} {
		if value, exists := object[field]; exists {
			if _, err := requireJSONString(core.ProtocolResponses, "$.response."+field, value); err != nil {
				return err
			}
		}
	}
	if output, exists := object["output"]; exists {
		if !rawJSONArray(output) {
			return core.Invalid(core.ProtocolResponses, "$.response.output", "output must be an array")
		}
		var items []json.RawMessage
		if err := json.Unmarshal(output, &items); err != nil {
			return core.Invalid(core.ProtocolResponses, "$.response.output", "invalid output array")
		}
		for _, item := range items {
			if !rawJSONObject(item) {
				return core.Invalid(core.ProtocolResponses, "$.response.output[]", "output item must be an object")
			}
			if err := validateKnownResponsesItem(item); err != nil {
				return err
			}
		}
	}
	if usage, exists := object["usage"]; exists && rawJSONPresent(usage) {
		if err := validateIntegerObjectFields(core.ProtocolResponses, "$.response.usage", usage, "input_tokens", "output_tokens", "total_tokens"); err != nil {
			return err
		}
		var usageObject map[string]json.RawMessage
		_ = json.Unmarshal(usage, &usageObject)
		if details := usageObject["input_tokens_details"]; rawJSONPresent(details) {
			if err := validateIntegerObjectFields(core.ProtocolResponses, "$.response.usage.input_tokens_details", details, "cached_tokens"); err != nil {
				return err
			}
		}
		if details := usageObject["output_tokens_details"]; rawJSONPresent(details) {
			if err := validateIntegerObjectFields(core.ProtocolResponses, "$.response.usage.output_tokens_details", details, "reasoning_tokens"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateKnownResponsesItem(raw json.RawMessage) error {
	var item nativeResponsesItem
	if err := decodeKnownNativeFields(core.ProtocolResponses, raw, &item); err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return core.Invalid(core.ProtocolResponses, "$.item", "invalid item object")
	}
	for _, field := range []string{"type", "role", "id", "call_id", "name", "status", "phase", "encrypted_content"} {
		if value, exists := object[field]; exists {
			if _, err := requireJSONString(core.ProtocolResponses, "$.item."+field, value); err != nil {
				return err
			}
		}
	}
	for _, field := range []string{"content", "summary"} {
		if value, exists := object[field]; exists {
			if !rawJSONArray(value) {
				return core.Invalid(core.ProtocolResponses, "$.item."+field, "must be an array")
			}
			var parts []json.RawMessage
			if err := json.Unmarshal(value, &parts); err != nil {
				return core.Invalid(core.ProtocolResponses, "$.item."+field, "invalid part array")
			}
			for _, part := range parts {
				if !rawJSONObject(part) {
					return core.Invalid(core.ProtocolResponses, "$.item."+field+"[]", "part must be an object")
				}
				if err := validateKnownResponsesPart(part); err != nil {
					return err
				}
			}
		}
	}
	return validateOptionalNullableString(core.ProtocolResponses, "$.item.arguments", object["arguments"])
}

func validateKnownResponsesPart(raw json.RawMessage) error {
	var part nativeResponsesContentPart
	if err := decodeKnownNativeFields(core.ProtocolResponses, raw, &part); err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return core.Invalid(core.ProtocolResponses, "$.part", "invalid part object")
	}
	for _, field := range []string{"type", "text", "refusal", "image_url", "file_id", "file_url", "file_data", "filename", "detail"} {
		if value, exists := object[field]; exists {
			if _, err := requireJSONString(core.ProtocolResponses, "$.part."+field, value); err != nil {
				return err
			}
		}
	}
	for _, field := range []string{"annotations", "logprobs"} {
		if value, exists := object[field]; exists && !rawJSONArray(value) {
			return core.Invalid(core.ProtocolResponses, "$.part."+field, "must be an array")
		}
	}
	if value, exists := object["input_audio"]; exists && !rawJSONObject(value) {
		return core.Invalid(core.ProtocolResponses, "$.part.input_audio", "must be an object")
	}
	return nil
}

func validateKnownGeminiChunk(data []byte) error {
	var envelope struct {
		Candidates     json.RawMessage `json:"candidates"`
		UsageMetadata  json.RawMessage `json:"usageMetadata"`
		PromptFeedback json.RawMessage `json:"promptFeedback"`
		ModelVersion   json.RawMessage `json:"modelVersion"`
		ResponseID     json.RawMessage `json:"responseId"`
	}
	if err := decodeKnownNativeFields(core.ProtocolGenerateContent, data, &envelope); err != nil {
		return err
	}
	for path, raw := range map[string]json.RawMessage{
		"$.modelVersion": envelope.ModelVersion,
		"$.responseId":   envelope.ResponseID,
	} {
		if len(bytes.TrimSpace(raw)) > 0 {
			if _, err := requireJSONString(core.ProtocolGenerateContent, path, raw); err != nil {
				return err
			}
		}
	}
	if len(bytes.TrimSpace(envelope.UsageMetadata)) > 0 && !rawJSONObject(envelope.UsageMetadata) {
		return core.Invalid(core.ProtocolGenerateContent, "$.usageMetadata", "usageMetadata must be an object")
	}
	if len(bytes.TrimSpace(envelope.PromptFeedback)) > 0 && !rawJSONObject(envelope.PromptFeedback) {
		return core.Invalid(core.ProtocolGenerateContent, "$.promptFeedback", "promptFeedback must be an object")
	}
	if rawJSONPresent(envelope.UsageMetadata) {
		if err := validateIntegerObjectFields(core.ProtocolGenerateContent, "$.usageMetadata", envelope.UsageMetadata,
			"promptTokenCount", "toolUsePromptTokenCount", "candidatesTokenCount", "totalTokenCount", "cachedContentTokenCount", "thoughtsTokenCount"); err != nil {
			return err
		}
	}
	if rawJSONPresent(envelope.PromptFeedback) {
		var feedback map[string]json.RawMessage
		_ = json.Unmarshal(envelope.PromptFeedback, &feedback)
		if reason, exists := feedback["blockReason"]; exists {
			if _, err := requireJSONString(core.ProtocolGenerateContent, "$.promptFeedback.blockReason", reason); err != nil {
				return err
			}
		}
	}
	if len(bytes.TrimSpace(envelope.Candidates)) > 0 {
		if !rawJSONArray(envelope.Candidates) {
			return core.Invalid(core.ProtocolGenerateContent, "$.candidates", "candidates must be an array")
		}
		var candidates []json.RawMessage
		if err := json.Unmarshal(envelope.Candidates, &candidates); err != nil {
			return core.Invalid(core.ProtocolGenerateContent, "$.candidates", "invalid candidate array")
		}
		for _, rawCandidate := range candidates {
			if !rawJSONObject(rawCandidate) {
				return core.Invalid(core.ProtocolGenerateContent, "$.candidates[]", "candidate must be an object")
			}
			var candidateFields struct {
				Index   json.RawMessage `json:"index"`
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(rawCandidate, &candidateFields); err != nil {
				return core.Invalid(core.ProtocolGenerateContent, "$.candidates[]", "invalid candidate object")
			}
			if len(bytes.TrimSpace(candidateFields.Index)) > 0 {
				var index int64
				if !rawJSONPresent(candidateFields.Index) || json.Unmarshal(candidateFields.Index, &index) != nil {
					return core.Invalid(core.ProtocolGenerateContent, "$.candidates[].index", "index must be an integer")
				}
			}
			if len(bytes.TrimSpace(candidateFields.Content)) > 0 {
				if !rawJSONObject(candidateFields.Content) {
					return core.Invalid(core.ProtocolGenerateContent, "$.candidates[].content", "content must be an object")
				}
				if err := validateKnownGeminiContent(candidateFields.Content); err != nil {
					return err
				}
			}
		}
	}
	var chunk geminiwire.Response
	if err := decodeKnownNativeFields(core.ProtocolGenerateContent, data, &chunk); err != nil {
		return err
	}
	for _, candidate := range chunk.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil && rawJSONPresent(part.FunctionCall.Args) && !rawJSONObject(part.FunctionCall.Args) {
				return core.Invalid(core.ProtocolGenerateContent, "$.candidates[].content.parts[].functionCall.args", "function arguments must be an object")
			}
			if part.FunctionResponse != nil && rawJSONPresent(part.FunctionResponse.Response) && !rawJSONObject(part.FunctionResponse.Response) {
				return core.Invalid(core.ProtocolGenerateContent, "$.candidates[].content.parts[].functionResponse.response", "function response must be an object")
			}
		}
		if rawJSONPresent(candidate.SafetyRatings) && !rawJSONArray(candidate.SafetyRatings) {
			return core.Invalid(core.ProtocolGenerateContent, "$.candidates[].safetyRatings", "safetyRatings must be an array")
		}
		for _, field := range []struct {
			path string
			raw  json.RawMessage
		}{
			{"logprobsResult", candidate.LogprobsResult},
			{"citationMetadata", candidate.CitationMetadata},
			{"groundingMetadata", candidate.GroundingMetadata},
			{"urlContextMetadata", candidate.URLContextMetadata},
		} {
			if err := validateOptionalJSONObject(core.ProtocolGenerateContent, "$.candidates[]."+field.path, field.raw); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateKnownGeminiContent(raw json.RawMessage) error {
	var fields struct {
		Role  json.RawMessage `json:"role"`
		Parts json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return core.Invalid(core.ProtocolGenerateContent, "$.candidates[].content", "invalid content object")
	}
	if len(bytes.TrimSpace(fields.Role)) > 0 {
		if _, err := requireJSONString(core.ProtocolGenerateContent, "$.candidates[].content.role", fields.Role); err != nil {
			return err
		}
	}
	if len(bytes.TrimSpace(fields.Parts)) == 0 || !rawJSONArray(fields.Parts) {
		return core.Invalid(core.ProtocolGenerateContent, "$.candidates[].content.parts", "parts must be an array")
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(fields.Parts, &parts); err != nil {
		return core.Invalid(core.ProtocolGenerateContent, "$.candidates[].content.parts", "invalid parts array")
	}
	for _, part := range parts {
		if !rawJSONObject(part) {
			return core.Invalid(core.ProtocolGenerateContent, "$.candidates[].content.parts[]", "part must be an object")
		}
	}
	return nil
}
