package chatgemini

import (
	core "github.com/2218342221/RouteMorphSDK/internal/core"
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

type toolChoiceMode = core.ToolChoiceMode

const (
	toolChoiceAuto     = core.ToolChoiceAuto
	toolChoiceNone     = core.ToolChoiceNone
	toolChoiceRequired = core.ToolChoiceRequired
	toolChoiceNamed    = core.ToolChoiceNamed
)

type toolChoice = core.ToolChoice
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
