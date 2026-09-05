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
	Container    json.RawMessage      `json:"container"`
	StopReason   json.RawMessage      `json:"stop_reason"`
	StopSequence json.RawMessage      `json:"stop_sequence"`
	StopDetails  json.RawMessage      `json:"stop_details"`
	Usage        *nativeMessagesUsage `json:"usage"`
}

type nativeMessagesContentBlock struct {
	Type            string            `json:"type"`
	Text            json.RawMessage   `json:"text"`
	Thinking        json.RawMessage   `json:"thinking"`
	Signature       json.RawMessage   `json:"signature"`
	Data            json.RawMessage   `json:"data"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Input           json.RawMessage   `json:"input"`
	ToolUseID       string            `json:"tool_use_id"`
	Content         json.RawMessage   `json:"content"`
	IsError         bool              `json:"is_error"`
	Citations       []json.RawMessage `json:"citations"`
	Caller          json.RawMessage   `json:"caller"`
	ToolsetName     string            `json:"toolset_name"`
	Title           json.RawMessage   `json:"title"`
	Context         json.RawMessage   `json:"context"`
	Source          json.RawMessage   `json:"source"`
	Transformations json.RawMessage   `json:"transformations"`
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
	Container    json.RawMessage `json:"container"`
	StopDetails  json.RawMessage `json:"stop_details"`
}

type nativeMessagesUsage struct {
	InputTokens              json.RawMessage `json:"input_tokens"`
	OutputTokens             json.RawMessage `json:"output_tokens"`
	CacheCreationInputTokens json.RawMessage `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     json.RawMessage `json:"cache_read_input_tokens"`
	CacheCreation            json.RawMessage `json:"cache_creation"`
	InferenceGeo             json.RawMessage `json:"inference_geo"`
	OutputTokensDetails      json.RawMessage `json:"output_tokens_details"`
	ServerToolUse            json.RawMessage `json:"server_tool_use"`
	ServiceTier              json.RawMessage `json:"service_tier"`
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
	CommandIndex   *int            `json:"command_index"`
	Delta          json.RawMessage `json:"delta"`
	Text           json.RawMessage `json:"text"`
	Refusal        json.RawMessage `json:"refusal"`
	Arguments      json.RawMessage `json:"arguments"`
	Input          json.RawMessage `json:"input"`
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
	Arguments        json.RawMessage               `json:"arguments"`
	Input            json.RawMessage               `json:"input"`
	Output           json.RawMessage               `json:"output"`
	Caller           json.RawMessage               `json:"caller"`
	Action           json.RawMessage               `json:"action"`
	Tools            json.RawMessage               `json:"tools"`
	Summary          []*nativeResponsesContentPart `json:"summary"`
	Status           string                        `json:"status"`
	Async            *bool                         `json:"async"`
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

func validateOptionalNullableObject(protocol core.Protocol, path string, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if !rawJSONObject(trimmed) {
		return core.Invalid(protocol, path, "must be an object or null")
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
	for path, raw := range map[string]json.RawMessage{
		"$.content_block.title": block.Title, "$.content_block.context": block.Context,
	} {
		if err := validateString(path, raw, false); err != nil {
			return err
		}
	}
	if rawJSONPresent(block.Source) && !rawJSONObject(block.Source) {
		return core.Invalid(protocol, "$.content_block.source", "must be an object")
	}
	if rawJSONPresent(block.Transformations) && !rawJSONArray(block.Transformations) {
		return core.Invalid(protocol, "$.content_block.transformations", "must be an array")
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
	for field, raw := range map[string]json.RawMessage{
		"cache_creation": usage.CacheCreation, "output_tokens_details": usage.OutputTokensDetails,
		"server_tool_use": usage.ServerToolUse,
	} {
		if len(bytes.TrimSpace(raw)) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && !rawJSONObject(raw) {
			return core.Invalid(protocol, path+"."+field, "must be an object or null")
		}
	}
	for field, raw := range map[string]json.RawMessage{
		"inference_geo": usage.InferenceGeo, "service_tier": usage.ServiceTier,
	} {
		if len(bytes.TrimSpace(raw)) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if _, err := requireJSONString(protocol, path+"."+field, raw); err != nil {
				return err
			}
		}
	}
	for field, raw := range map[string]json.RawMessage{
		"cache_creation": usage.CacheCreation, "output_tokens_details": usage.OutputTokensDetails,
		"server_tool_use": usage.ServerToolUse,
	} {
		if !rawJSONObject(raw) {
			continue
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return core.Invalid(protocol, path+"."+field, "must be an object")
		}
		for name, valueRaw := range values {
			var known bool
			switch field {
			case "cache_creation":
				known = name == "ephemeral_1h_input_tokens" || name == "ephemeral_5m_input_tokens"
			case "output_tokens_details":
				known = name == "thinking_tokens"
			case "server_tool_use":
				known = name == "web_search_requests" || name == "web_fetch_requests"
			}
			if !known {
				continue
			}
			var count int64
			if json.Unmarshal(valueRaw, &count) != nil || count < 0 {
				return core.Invalid(protocol, path+"."+field+"."+name, "must be a non-negative integer")
			}
		}
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
	for _, field := range []string{"sequence_number", "output_index", "content_index", "summary_index", "command_index", "partial_image_index", "annotation_index"} {
		if raw, exists := fields[field]; exists {
			var value int64
			if !rawJSONPresent(raw) || json.Unmarshal(raw, &value) != nil || value < 0 {
				return core.Invalid(core.ProtocolResponses, "$."+field, "must be a non-negative integer")
			}
		}
	}
	if err := validateRequiredResponsesEventFields(eventType, fields); err != nil {
		return err
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
		if !rawJSONObject(event.Response) {
			return core.Invalid(core.ProtocolResponses, "$.response", "response must be an object")
		}
		if err := validateKnownResponsesResponse(event.Response); err != nil {
			return err
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
	case "response.output_text.delta":
		if _, err := requireJSONString(core.ProtocolResponses, "$.delta", event.Delta); err != nil {
			return err
		}
		if err := requireResponsesEventArray(fields, "logprobs"); err != nil {
			return err
		}
	case "response.refusal.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta",
		"response.audio.delta", "response.audio.transcript.delta", "response.code_interpreter_call_code.delta", "response.mcp_call_arguments.delta", "response.shell_call_command.delta":
		if _, err := requireJSONString(core.ProtocolResponses, "$.delta", event.Delta); err != nil {
			return err
		}
	case "response.output_text.done":
		if _, err := requireJSONString(core.ProtocolResponses, "$.text", event.Text); err != nil {
			return err
		}
		if err := requireResponsesEventArray(fields, "logprobs"); err != nil {
			return err
		}
	case "response.reasoning_summary_text.done", "response.reasoning_text.done":
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
		if err := requireResponsesEventString(fields, "name", true); err != nil {
			return err
		}
	case "response.custom_tool_call_input.done":
		if _, err := requireJSONString(core.ProtocolResponses, "$.input", event.Input); err != nil {
			return err
		}
	case "response.code_interpreter_call_code.done":
		if err := requireResponsesEventString(fields, "code", false); err != nil {
			return err
		}
	case "response.mcp_call_arguments.done":
		if err := requireResponsesEventString(fields, "arguments", false); err != nil {
			return err
		}
	case "response.shell_call_command.added", "response.shell_call_command.done":
		if err := requireResponsesEventString(fields, "command", false); err != nil {
			return err
		}
	case "response.shell_call_output_content.delta":
		if err := requireResponsesEventObject(fields, "delta"); err != nil {
			return err
		}
	case "response.shell_call_output_content.done":
		if err := requireResponsesEventArray(fields, "output"); err != nil {
			return err
		}
	case "response.image_generation_call.partial_image":
		if err := requireResponsesEventString(fields, "partial_image_b64", false); err != nil {
			return err
		}
	case "response.output_text.annotation.added":
		if err := requireResponsesEventObject(fields, "annotation"); err != nil {
			return err
		}
	case "response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed":
		if event.ItemID == "" {
			return core.Invalid(core.ProtocolResponses, "$.item_id", "item id is required")
		}
	case "error":
		for _, field := range []string{"code", "message", "param"} {
			if err := requireResponsesEventString(fields, field, false); err != nil {
				return err
			}
		}
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

type nativeResponsesEventRequirements uint16

const (
	nativeResponsesRequireSequence nativeResponsesEventRequirements = 1 << iota
	nativeResponsesRequireItemID
	nativeResponsesRequireOutputIndex
	nativeResponsesRequireContentIndex
	nativeResponsesRequireSummaryIndex
	nativeResponsesRequireCommandIndex
	nativeResponsesRequirePartialImageIndex
	nativeResponsesRequireAnnotationIndex
)

func knownResponsesEventRequirements(eventType string) (nativeResponsesEventRequirements, bool) {
	const sequence = nativeResponsesRequireSequence
	const itemOutput = sequence | nativeResponsesRequireItemID | nativeResponsesRequireOutputIndex
	switch eventType {
	case "response.audio.delta", "response.audio.done", "response.audio.transcript.delta", "response.audio.transcript.done",
		"response.created", "response.queued", "response.in_progress", "response.completed", "response.incomplete", "response.failed", "response.cancelled", "error":
		return sequence, true
	case "response.output_item.added", "response.output_item.done":
		return sequence | nativeResponsesRequireOutputIndex, true
	case "response.content_part.added", "response.content_part.done":
		return itemOutput | nativeResponsesRequireContentIndex, true
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done",
		"response.reasoning_summary_text.delta", "response.reasoning_summary_text.done":
		return itemOutput | nativeResponsesRequireSummaryIndex, true
	case "response.output_text.delta", "response.output_text.done", "response.refusal.delta", "response.refusal.done",
		"response.reasoning_text.delta", "response.reasoning_text.done":
		return itemOutput | nativeResponsesRequireContentIndex, true
	case "response.function_call_arguments.delta", "response.function_call_arguments.done",
		"response.custom_tool_call_input.delta", "response.custom_tool_call_input.done",
		"response.code_interpreter_call_code.delta", "response.code_interpreter_call_code.done",
		"response.code_interpreter_call.in_progress", "response.code_interpreter_call.interpreting", "response.code_interpreter_call.completed",
		"response.file_search_call.in_progress", "response.file_search_call.searching", "response.file_search_call.completed",
		"response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed",
		"response.image_generation_call.in_progress", "response.image_generation_call.generating", "response.image_generation_call.completed",
		"response.mcp_call_arguments.delta", "response.mcp_call_arguments.done",
		"response.mcp_call.in_progress", "response.mcp_call.completed", "response.mcp_call.failed",
		"response.mcp_list_tools.in_progress", "response.mcp_list_tools.completed", "response.mcp_list_tools.failed":
		return itemOutput, true
	case "response.image_generation_call.partial_image":
		return itemOutput | nativeResponsesRequirePartialImageIndex, true
	case "response.shell_call_command.added", "response.shell_call_command.delta", "response.shell_call_command.done":
		return sequence | nativeResponsesRequireOutputIndex | nativeResponsesRequireCommandIndex, true
	case "response.shell_call_output_content.delta", "response.shell_call_output_content.done":
		return itemOutput | nativeResponsesRequireCommandIndex, true
	case "response.output_text.annotation.added":
		return itemOutput | nativeResponsesRequireContentIndex | nativeResponsesRequireAnnotationIndex, true
	default:
		return 0, false
	}
}

func validateRequiredResponsesEventFields(eventType string, fields map[string]json.RawMessage) error {
	requirements, known := knownResponsesEventRequirements(eventType)
	if !known {
		return nil
	}
	for _, field := range []struct {
		name string
		flag nativeResponsesEventRequirements
	}{
		{"sequence_number", nativeResponsesRequireSequence},
		{"output_index", nativeResponsesRequireOutputIndex},
		{"content_index", nativeResponsesRequireContentIndex},
		{"summary_index", nativeResponsesRequireSummaryIndex},
		{"command_index", nativeResponsesRequireCommandIndex},
		{"partial_image_index", nativeResponsesRequirePartialImageIndex},
		{"annotation_index", nativeResponsesRequireAnnotationIndex},
	} {
		if requirements&field.flag == 0 {
			continue
		}
		raw, exists := fields[field.name]
		var value int64
		if !exists || !rawJSONPresent(raw) || json.Unmarshal(raw, &value) != nil || value < 0 {
			return core.Invalid(core.ProtocolResponses, "$."+field.name, "required non-negative integer is missing or invalid")
		}
	}
	if requirements&nativeResponsesRequireItemID != 0 {
		return requireResponsesEventString(fields, "item_id", true)
	}
	return nil
}

func requireResponsesEventString(fields map[string]json.RawMessage, field string, nonEmpty bool) error {
	raw, exists := fields[field]
	if !exists {
		return core.Invalid(core.ProtocolResponses, "$."+field, "required string is missing")
	}
	value, err := requireJSONString(core.ProtocolResponses, "$."+field, raw)
	if err != nil {
		return err
	}
	if nonEmpty && value == "" {
		return core.Invalid(core.ProtocolResponses, "$."+field, "must not be empty")
	}
	return nil
}

func requireResponsesEventArray(fields map[string]json.RawMessage, field string) error {
	raw, exists := fields[field]
	if !exists || !rawJSONArray(raw) {
		return core.Invalid(core.ProtocolResponses, "$."+field, "required array is missing or invalid")
	}
	return nil
}

func requireResponsesEventObject(fields map[string]json.RawMessage, field string) error {
	raw, exists := fields[field]
	if !exists || !rawJSONObject(raw) {
		return core.Invalid(core.ProtocolResponses, "$."+field, "required object is missing or invalid")
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
	switch item.Type {
	case "function_call":
		if err := validateOptionalNullableString(core.ProtocolResponses, "$.item.arguments", object["arguments"]); err != nil {
			return err
		}
	case "custom_tool_call":
		if _, exists := object["input"]; exists {
			if _, err := requireJSONString(core.ProtocolResponses, "$.item.input", object["input"]); err != nil {
				return err
			}
		}
	case "tool_search_call":
		// Tool-search arguments are arbitrary JSON rather than the JSON-encoded
		// string used by function calls.
	}
	if value, exists := object["caller"]; exists && rawJSONPresent(value) && !rawJSONObject(value) {
		return core.Invalid(core.ProtocolResponses, "$.item.caller", "must be an object or null")
	}
	if value, exists := object["action"]; exists && !rawJSONObject(value) {
		return core.Invalid(core.ProtocolResponses, "$.item.action", "must be an object")
	}
	if value, exists := object["tools"]; exists && !rawJSONArray(value) {
		return core.Invalid(core.ProtocolResponses, "$.item.tools", "must be an array")
	}
	if value, exists := object["output"]; exists && rawJSONPresent(value) {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || (trimmed[0] != '"' && !rawJSONArray(value)) {
			return core.Invalid(core.ProtocolResponses, "$.item.output", "must be a string or content array")
		}
	}
	return nil
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
