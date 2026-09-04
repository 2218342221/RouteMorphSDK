package chatgemini

import routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"

var (
	decodeJSON            = routekit.DecodeJSON
	marshal               = routekit.Marshal
	mustJSON              = routekit.MustJSON
	normalizeArguments    = routekit.NormalizeArguments
	textParts             = routekit.TextParts
	dataURL               = routekit.DataURL
	parseDataURL          = routekit.ParseDataURL
	appendDiagnostic      = routekit.AppendDiagnostic
	rejectUnknownTopLevel = routekit.RejectUnknownTopLevel
)
