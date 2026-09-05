package messagesgemini

import routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"

var (
	decodeJSON                         = routekit.DecodeJSON
	marshal                            = routekit.Marshal
	mustJSON                           = routekit.MustJSON
	normalizeArguments                 = routekit.NormalizeArguments
	appendDiagnostic                   = routekit.AppendDiagnostic
	rejectUnknownTopLevel              = routekit.RejectUnknownTopLevel
	rejectUnknownObjectFields          = routekit.RejectUnknownObjectFields
	validateMessagesOutputConfigFields = routekit.ValidateMessagesOutputConfigFields
	validateMessagesThinkingFields     = routekit.ValidateMessagesThinkingFields
	validateMessagesContentBlockFields = routekit.ValidateMessagesContentBlockFields
	geminiFileURI                      = routekit.GeminiFileURICompatible
	portableGeminiFileURI              = routekit.PortableGeminiFileURI
	validBase64                        = routekit.ValidBase64
	mimeTypeFromURL                    = routekit.MIMETypeFromURL
	validMIMEType                      = routekit.ValidMIMEType
)

func validMessagesImageMediaType(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
