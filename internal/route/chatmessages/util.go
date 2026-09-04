package chatmessages

import routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"

var (
	decodeJSON            = routekit.DecodeJSON
	marshal               = routekit.Marshal
	mustJSON              = routekit.MustJSON
	normalizeArguments    = routekit.NormalizeArguments
	rawString             = routekit.RawString
	textParts             = routekit.TextParts
	dataURL               = routekit.DataURL
	parseDataURL          = routekit.ParseDataURL
	appendDiagnostic      = routekit.AppendDiagnostic
	rejectUnknownTopLevel = routekit.RejectUnknownTopLevel
	jsonValuePresent      = routekit.ValuePresent
	rawJSONValuePresent   = routekit.ValuePresent
)
