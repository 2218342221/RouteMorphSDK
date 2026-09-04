package stream

import core "github.com/2218342221/RouteMorphSDK/internal/core"

type finishReason = core.FinishReason

const (
	finishStop          = core.FinishStop
	finishLength        = core.FinishLength
	finishToolCalls     = core.FinishToolCalls
	finishContentFilter = core.FinishContentFilter
	finishError         = core.FinishError
)

type Diagnostic = core.Diagnostic
