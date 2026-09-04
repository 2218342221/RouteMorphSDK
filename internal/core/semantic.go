package core

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type PartKind string

const (
	PartText       PartKind = "text"
	PartImage      PartKind = "image"
	PartAudio      PartKind = "audio"
	PartFile       PartKind = "file"
	PartToolCall   PartKind = "tool_call"
	PartToolResult PartKind = "tool_result"
	PartReasoning  PartKind = "reasoning"
	PartRefusal    PartKind = "refusal"
)

type Message struct {
	Role  Role
	Parts []Part
	Name  string
}
type Part struct {
	Kind       PartKind
	Text       string
	Media      *Media
	ToolCall   *ToolCall
	ToolResult *ToolResult
	Opaque     string
}
type Media struct{ URL, Data, MIMEType, FileID, Detail, Filename string }
type ToolCall struct {
	ID, Name  string
	Arguments json.RawMessage
}
type ToolResult struct {
	CallID, Name string
	Content      []Part
	IsError      bool
}

type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
	ToolChoiceNamed    ToolChoiceMode = "named"
)

type ToolChoice struct {
	Mode ToolChoiceMode
	Name string
}
type JSONSchemaFormat struct {
	Name, Description string
	Schema            json.RawMessage
	Strict            *bool
}

type FinishReason string

const (
	FinishStop          FinishReason = "stop"
	FinishLength        FinishReason = "length"
	FinishToolCalls     FinishReason = "tool_calls"
	FinishContentFilter FinishReason = "content_filter"
	FinishError         FinishReason = "error"
)

type TokenUsage struct {
	InputTokens, OutputTokens, TotalTokens, CachedInputTokens, CacheCreationTokens, ReasoningTokens int64
}
