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
	parseFileData         = routekit.ParseFileData
	openAIFileData        = routekit.OpenAIFileData
	chatAudioMIMEType     = routekit.ChatAudioMIMEType
	chatAudioFormat       = routekit.ChatAudioFormat
	validBase64           = routekit.ValidBase64
	validImageMIMEType    = routekit.ValidOpenAIImageMIMEType
	validHTTPURL          = routekit.ValidHTTPURL
	mimeTypeFromURL       = routekit.MIMETypeFromURL
	appendDiagnostic      = routekit.AppendDiagnostic
	rejectUnknownTopLevel = routekit.RejectUnknownTopLevel
	jsonValuePresent      = routekit.ValuePresent
	rawJSONValuePresent   = routekit.ValuePresent
)
