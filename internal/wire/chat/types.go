// Package chat owns the JSON wire types for OpenAI Chat Completions.
package chat

import "encoding/json"

type Request struct {
	Model                string            `json:"model"`
	Messages             []Message         `json:"messages"`
	Tools                []Tool            `json:"tools,omitempty"`
	ToolChoice           json.RawMessage   `json:"tool_choice,omitempty"`
	MaxTokens            *int              `json:"max_tokens,omitempty"`
	MaxCompletion        *int              `json:"max_completion_tokens,omitempty"`
	Temperature          *float64          `json:"temperature,omitempty"`
	TopP                 *float64          `json:"top_p,omitempty"`
	Stop                 json.RawMessage   `json:"stop,omitempty"`
	Stream               bool              `json:"stream,omitempty"`
	ParallelToolCalls    *bool             `json:"parallel_tool_calls,omitempty"`
	ResponseFormat       json.RawMessage   `json:"response_format,omitempty"`
	ReasoningEffort      string            `json:"reasoning_effort,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	N                    *int              `json:"n,omitempty"`
	FrequencyPenalty     *float64          `json:"frequency_penalty,omitempty"`
	PresencePenalty      *float64          `json:"presence_penalty,omitempty"`
	Logprobs             *bool             `json:"logprobs,omitempty"`
	TopLogprobs          *int              `json:"top_logprobs,omitempty"`
	Verbosity            json.RawMessage   `json:"verbosity,omitempty"`
	User                 json.RawMessage   `json:"user,omitempty"`
	ServiceTier          json.RawMessage   `json:"service_tier,omitempty"`
	Store                json.RawMessage   `json:"store,omitempty"`
	PromptCacheKey       json.RawMessage   `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention json.RawMessage   `json:"prompt_cache_retention,omitempty"`
	SafetyIdentifier     json.RawMessage   `json:"safety_identifier,omitempty"`
	StreamOptions        *StreamOptions    `json:"stream_options,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type Message struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content,omitempty"`
	Name             string          `json:"name,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	FunctionCall     json.RawMessage `json:"function_call,omitempty"`
	Audio            json.RawMessage `json:"audio,omitempty"`
	Refusal          string          `json:"refusal,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
}

type ContentPart struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ImageURL   json.RawMessage `json:"image_url,omitempty"`
	InputAudio *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio,omitempty"`
	File *struct {
		FileID   string `json:"file_id,omitempty"`
		FileData string `json:"file_data,omitempty"`
		Filename string `json:"filename,omitempty"`
	} `json:"file,omitempty"`
}

type Tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
		Strict      *bool           `json:"strict,omitempty"`
	} `json:"function"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type Response struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []ResponseChoice `json:"choices"`
	Usage   Usage            `json:"usage"`
	Error   *Error           `json:"error,omitempty"`
}

type ResponseChoice struct {
	Index        int             `json:"index"`
	Message      Message         `json:"message"`
	FinishReason string          `json:"finish_reason"`
	Logprobs     json.RawMessage `json:"logprobs,omitempty"`
}

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	PromptDetails    struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type Error struct {
	Message string          `json:"message"`
	Type    string          `json:"type,omitempty"`
	Code    json.RawMessage `json:"code,omitempty"`
}
