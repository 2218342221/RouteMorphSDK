package messagesgemini

import core "github.com/2218342221/RouteMorphSDK/internal/core"

const (
	toolChoiceAuto     = core.ToolChoiceAuto
	toolChoiceNone     = core.ToolChoiceNone
	toolChoiceRequired = core.ToolChoiceRequired
	toolChoiceNamed    = core.ToolChoiceNamed
)

type toolChoice = core.ToolChoice
type finishReason = core.FinishReason

const (
	finishStop          = core.FinishStop
	finishLength        = core.FinishLength
	finishToolCalls     = core.FinishToolCalls
	finishContentFilter = core.FinishContentFilter
	finishError         = core.FinishError
)

type Diagnostic = core.Diagnostic
