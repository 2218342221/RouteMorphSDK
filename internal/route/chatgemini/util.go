package chatgemini

import routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"

var (
	decodeJSON                       = routekit.DecodeJSON
	marshal                          = routekit.Marshal
	mustJSON                         = routekit.MustJSON
	normalizeArguments               = routekit.NormalizeArguments
	textParts                        = routekit.TextParts
	dataURL                          = routekit.DataURL
	parseDataURL                     = routekit.ParseDataURL
	parseFileData                    = routekit.ParseFileData
	openAIFileData                   = routekit.OpenAIFileData
	mimeTypeFromURL                  = routekit.MIMETypeFromURL
	validMIMEType                    = routekit.ValidMIMEType
	chatAudioMIMEType                = routekit.ChatAudioMIMEType
	chatAudioFormat                  = routekit.ChatAudioFormat
	validBase64                      = routekit.ValidBase64
	validImageMIMEType               = routekit.ValidOpenAIImageMIMEType
	geminiFileURI                    = routekit.GeminiFileURICompatible
	portableGeminiFileURI            = routekit.PortableGeminiFileURI
	appendDiagnostic                 = routekit.AppendDiagnostic
	rejectUnknownTopLevel            = routekit.RejectUnknownTopLevel
	rejectUnknownObjectFields        = routekit.RejectUnknownObjectFields
	validateChatResponseFormatFields = routekit.ValidateChatResponseFormatFields
	validateChatMessageContentFields = routekit.ValidateChatMessageContentFields
)
