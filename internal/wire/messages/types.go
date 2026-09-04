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
	TopP          *float64          `json:"top_p,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	Thinking      *Thinking         `json:"thinking,omitempty"`
	OutputConfig  *OutputConfig     `json:"output_config,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Container     json.RawMessage   `json:"container,omitempty"`
}

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type Block struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	Data         string          `json:"data,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      json.RawMessage `json:"content,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
	Citations    json.RawMessage `json:"citations,omitempty"`
	Caller       json.RawMessage `json:"caller,omitempty"`
	ToolsetName  string          `json:"toolset_name,omitempty"`
	Source       *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type,omitempty"`
		Data      string `json:"data,omitempty"`
		URL       string `json:"url,omitempty"`
	} `json:"source,omitempty"`
}

type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	Strict       *bool           `json:"strict,omitempty"`
	Type         string          `json:"type,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
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
	Usage        Usage           `json:"usage"`
}

type Usage struct {
	InputTokens              int64           `json:"input_tokens"`
	OutputTokens             int64           `json:"output_tokens"`
	CacheCreationInputTokens int64           `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64           `json:"cache_read_input_tokens"`
	ServerToolUse            json.RawMessage `json:"server_tool_use,omitempty"`
}
