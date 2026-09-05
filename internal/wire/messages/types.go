// Package messages owns the JSON wire types for Anthropic Messages.
package messages

import "encoding/json"

type Request struct {
	Model         string            `json:"model"`
	MaxTokens     int               `json:"max_tokens"`
	Messages      []Message         `json:"messages"`
	System        json.RawMessage   `json:"system,omitempty"`
	Tools         []Tool            `json:"tools,omitempty"`
	ToolChoice    json.RawMessage   `json:"tool_choice,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	TopK          *int              `json:"top_k,omitempty"`
	TopP          *float64          `json:"top_p,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	Thinking      *Thinking         `json:"thinking,omitempty"`
	OutputConfig  *OutputConfig     `json:"output_config,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Container     json.RawMessage   `json:"container,omitempty"`
	CacheControl  json.RawMessage   `json:"cache_control,omitempty"`
	InferenceGeo  string            `json:"inference_geo,omitempty"`
	ServiceTier   string            `json:"service_tier,omitempty"`
}

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type Block struct {
	Type            string          `json:"type"`
	Text            string          `json:"text,omitempty"`
	Thinking        string          `json:"thinking,omitempty"`
	Signature       string          `json:"signature,omitempty"`
	Data            string          `json:"data,omitempty"`
	ID              string          `json:"id,omitempty"`
	Name            string          `json:"name,omitempty"`
	Input           json.RawMessage `json:"input,omitempty"`
	ToolUseID       string          `json:"tool_use_id,omitempty"`
	Content         json.RawMessage `json:"content,omitempty"`
	IsError         bool            `json:"is_error,omitempty"`
	CacheControl    json.RawMessage `json:"cache_control,omitempty"`
	Citations       json.RawMessage `json:"citations,omitempty"`
	Caller          json.RawMessage `json:"caller,omitempty"`
	ToolsetName     string          `json:"toolset_name,omitempty"`
	Title           string          `json:"title,omitempty"`
	Context         string          `json:"context,omitempty"`
	Transformations json.RawMessage `json:"transformations,omitempty"`
	Source          *struct {
		Type      string          `json:"type"`
		MediaType string          `json:"media_type,omitempty"`
		Data      string          `json:"data,omitempty"`
		URL       string          `json:"url,omitempty"`
		FileID    string          `json:"file_id,omitempty"`
		Content   json.RawMessage `json:"content,omitempty"`
	} `json:"source,omitempty"`
}

type Tool struct {
	Name                string          `json:"name"`
	Description         string          `json:"description,omitempty"`
	InputSchema         json.RawMessage `json:"input_schema"`
	Strict              *bool           `json:"strict,omitempty"`
	Type                string          `json:"type,omitempty"`
	CacheControl        json.RawMessage `json:"cache_control,omitempty"`
	EagerInputStreaming *bool           `json:"eager_input_streaming,omitempty"`
	DeferLoading        *bool           `json:"defer_loading,omitempty"`
	AllowedCallers      []string        `json:"allowed_callers,omitempty"`
	InputExamples       json.RawMessage `json:"input_examples,omitempty"`
}

type Thinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	Display      string `json:"display,omitempty"`
}

type OutputConfig struct {
	Effort string `json:"effort,omitempty"`
	Format *struct {
		Type   string          `json:"type"`
		Schema json.RawMessage `json:"schema"`
	} `json:"format,omitempty"`
}

type Response struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Model        string          `json:"model"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stop_reason"`
	StopSequence string          `json:"stop_sequence,omitempty"`
	StopDetails  json.RawMessage `json:"stop_details,omitempty"`
	Container    json.RawMessage `json:"container,omitempty"`
	Usage        Usage           `json:"usage"`
}

type Usage struct {
	InputTokens              int64                `json:"input_tokens"`
	OutputTokens             int64                `json:"output_tokens"`
	CacheCreationInputTokens int64                `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64                `json:"cache_read_input_tokens"`
	CacheCreation            *CacheCreation       `json:"cache_creation,omitempty"`
	InferenceGeo             string               `json:"inference_geo,omitempty"`
	OutputTokensDetails      *OutputTokensDetails `json:"output_tokens_details,omitempty"`
	ServerToolUse            json.RawMessage      `json:"server_tool_use,omitempty"`
	ServiceTier              string               `json:"service_tier,omitempty"`
}

type CacheCreation struct {
	Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
	Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
}

type OutputTokensDetails struct {
	ThinkingTokens int64 `json:"thinking_tokens"`
}
