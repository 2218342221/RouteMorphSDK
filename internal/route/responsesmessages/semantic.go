package responsesmessages

import (
	core "github.com/2218342221/RouteMorphSDK/internal/core"
	responseswire "github.com/2218342221/RouteMorphSDK/internal/wire/responses"
)

const (
	partText       = core.PartText
	partImage      = core.PartImage
	partAudio      = core.PartAudio
	partFile       = core.PartFile
	partToolCall   = core.PartToolCall
	partToolResult = core.PartToolResult
	partReasoning  = core.PartReasoning
	partRefusal    = core.PartRefusal
)

type portablePart = core.Part
type portableMedia = core.Media
type portableToolCall = core.ToolCall
type portableToolResult = core.ToolResult

type toolChoiceMode = core.ToolChoiceMode

const (
	toolChoiceAuto     = core.ToolChoiceAuto
	toolChoiceNone     = core.ToolChoiceNone
	toolChoiceRequired = core.ToolChoiceRequired
	toolChoiceNamed    = core.ToolChoiceNamed
)

type toolChoice = core.ToolChoice
type reasoningConfig = responseswire.ReasoningConfig
type jsonSchemaFormat = core.JSONSchemaFormat
type finishReason = core.FinishReason

const (
	finishStop          = core.FinishStop
	finishLength        = core.FinishLength
	finishToolCalls     = core.FinishToolCalls
	finishContentFilter = core.FinishContentFilter
	finishError         = core.FinishError
)

type Diagnostic = core.Diagnostic
