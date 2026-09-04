// Package responses owns the JSON wire types for the OpenAI Responses API.
package responses

import "encoding/json"

type ReasoningConfig struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type Request struct {
	Model                string            `json:"model"`
	Input                json.RawMessage   `json:"input"`
	Instructions         json.RawMessage   `json:"instructions,omitempty"`
	Tools                []Tool            `json:"tools,omitempty"`
	ToolChoice           json.RawMessage   `json:"tool_choice,omitempty"`
	MaxOutputTokens      *int              `json:"max_output_tokens,omitempty"`
	Temperature          *float64          `json:"temperature,omitempty"`
	TopP                 *float64          `json:"top_p,omitempty"`
	Stream               bool              `json:"stream,omitempty"`
	ParallelToolCalls    *bool             `json:"parallel_tool_calls,omitempty"`
	Reasoning            *ReasoningConfig  `json:"reasoning,omitempty"`
	Text                 json.RawMessage   `json:"text,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	Conversation         json.RawMessage   `json:"conversation,omitempty"`
	PreviousResponse     string            `json:"previous_response_id,omitempty"`
	Prompt               json.RawMessage   `json:"prompt,omitempty"`
	ContextManagement    json.RawMessage   `json:"context_management,omitempty"`
	Background           bool              `json:"background,omitempty"`
	Include              json.RawMessage   `json:"include,omitempty"`
	TopLogprobs          *int              `json:"top_logprobs,omitempty"`
	FrequencyPenalty     *float64          `json:"frequency_penalty,omitempty"`
	PresencePenalty      *float64          `json:"presence_penalty,omitempty"`
	ServiceTier          json.RawMessage   `json:"service_tier,omitempty"`
	Store                json.RawMessage   `json:"store,omitempty"`
	PromptCacheKey       json.RawMessage   `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions   json.RawMessage   `json:"prompt_cache_options,omitempty"`
	PromptCacheRetention json.RawMessage   `json:"prompt_cache_retention,omitempty"`
	SafetyIdentifier     json.RawMessage   `json:"safety_identifier,omitempty"`
	StreamOptions        json.RawMessage   `json:"stream_options,omitempty"`
	Truncation           json.RawMessage   `json:"truncation,omitempty"`
	User                 json.RawMessage   `json:"user,omitempty"`
	MaxToolCalls         *int              `json:"max_tool_calls,omitempty"`
	ClientMetadata       json.RawMessage   `json:"client_metadata,omitempty"`
	Moderation           json.RawMessage   `json:"moderation,omitempty"`
}

type Tool struct {
	Type                  string          `json:"type"`
	Name                  string          `json:"name,omitempty"`
	Description           string          `json:"description,omitempty"`
	Parameters            json.RawMessage `json:"parameters,omitempty"`
	Strict                *bool           `json:"strict,omitempty"`
	DeferLoading          bool            `json:"defer_loading,omitempty"`
	PromptCacheBreakpoint json.RawMessage `json:"prompt_cache_breakpoint,omitempty"`
}

type Item struct {
	Type             string          `json:"type"`
	Role             string          `json:"role,omitempty"`
	Content          json.RawMessage `json:"content,omitempty"`
	ID               string          `json:"id,omitempty"`
	CallID           string          `json:"call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	Output           json.RawMessage `json:"output,omitempty"`
	Summary          json.RawMessage `json:"summary,omitempty"`
	Status           string          `json:"status,omitempty"`
	Phase            string          `json:"phase,omitempty"`
	EncryptedContent json.RawMessage `json:"encrypted_content,omitempty"`
}

type ContentPart struct {
	Type                  string          `json:"type"`
	Text                  string          `json:"text,omitempty"`
	Refusal               string          `json:"refusal,omitempty"`
	ImageURL              string          `json:"image_url,omitempty"`
	FileID                string          `json:"file_id,omitempty"`
	FileURL               string          `json:"file_url,omitempty"`
	FileData              string          `json:"file_data,omitempty"`
	Filename              string          `json:"filename,omitempty"`
	Detail                string          `json:"detail,omitempty"`
	Annotations           json.RawMessage `json:"annotations,omitempty"`
	Logprobs              json.RawMessage `json:"logprobs,omitempty"`
	PromptCacheBreakpoint json.RawMessage `json:"prompt_cache_breakpoint,omitempty"`
	InputAudio            json.RawMessage `json:"input_audio,omitempty"`
}

type Response struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	CreatedAt int64  `json:"created_at"`
	Model     string `json:"model"`
	Status    string `json:"status"`
	Error     *Error `json:"error,omitempty"`
	Output    []Item `json:"output"`
	Usage     struct {
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
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
