package messagesgemini

import routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"

var (
	decodeJSON            = routekit.DecodeJSON
	marshal               = routekit.Marshal
	mustJSON              = routekit.MustJSON
	normalizeArguments    = routekit.NormalizeArguments
	appendDiagnostic      = routekit.AppendDiagnostic
	rejectUnknownTopLevel = routekit.RejectUnknownTopLevel
)
