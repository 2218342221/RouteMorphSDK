package responsesmessages

import (
	"fmt"
	"strings"

	routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"
)

var (
	decodeJSON                         = routekit.DecodeJSON
	marshal                            = routekit.Marshal
	mustJSON                           = routekit.MustJSON
	normalizeArguments                 = routekit.NormalizeArguments
	rawString                          = routekit.RawString
	textParts                          = routekit.TextParts
	dataURL                            = routekit.DataURL
	parseDataURL                       = routekit.ParseDataURL
	parseFileData                      = routekit.ParseFileData
	openAIFileData                     = routekit.OpenAIFileData
	validBase64                        = routekit.ValidBase64
	validImageMIMEType                 = routekit.ValidOpenAIImageMIMEType
	validHTTPURL                       = routekit.ValidHTTPURL
	mimeTypeFromURL                    = routekit.MIMETypeFromURL
	appendDiagnostic                   = routekit.AppendDiagnostic
	rejectUnknownTopLevel              = routekit.RejectUnknownTopLevel
	validateMessagesOutputConfigFields = routekit.ValidateMessagesOutputConfigFields
	validateMessagesThinkingFields     = routekit.ValidateMessagesThinkingFields
	validateMessagesContentBlockFields = routekit.ValidateMessagesContentBlockFields
)

func rejectUnknownCrossTopLevel(protocol Protocol, data []byte) error {
	switch protocol {
	case ProtocolChat:
		return rejectUnknownTopLevel(protocol, data, "model", "messages", "tools", "tool_choice", "max_tokens", "max_completion_tokens", "temperature", "top_p", "stop", "stream", "parallel_tool_calls", "response_format", "reasoning_effort", "metadata")
	case ProtocolResponses:
		return rejectUnknownTopLevel(protocol, data, "model", "input", "instructions", "tools", "tool_choice", "max_output_tokens", "temperature", "top_p", "stream", "parallel_tool_calls", "reasoning", "text", "metadata", "conversation", "previous_response_id", "prompt", "context_management", "background", "service_tier", "safety_identifier")
	case ProtocolMessages:
		return rejectUnknownTopLevel(protocol, data, "model", "max_tokens", "messages", "system", "tools", "tool_choice", "temperature", "top_k", "top_p", "stop_sequences", "stream", "thinking", "output_config", "metadata", "container", "cache_control", "inference_geo", "service_tier")
	case ProtocolGenerateContent:
		return rejectUnknownTopLevel(protocol, data, "contents", "systemInstruction", "tools", "toolConfig", "generationConfig", "safetySettings", "cachedContent")
	default:
		return invalid(protocol, "$", "unknown protocol")
	}
}

func portableTextOnly(protocol Protocol, parts []responsesContentPart, path string) (string, error) {
	var builder strings.Builder
	for index, part := range parts {
		if part.Type != "input_text" && part.Type != "text" {
			return "", unsupported(protocol, fmt.Sprintf("%s[%d]", path, index), "Responses instructions only support text")
		}
		builder.WriteString(part.Text)
	}
	return builder.String(), nil
}
