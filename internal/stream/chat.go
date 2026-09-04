package stream

import chatwire "github.com/2218342221/RouteMorphSDK/internal/wire/chat"

type chatMessage = chatwire.Message
type chatToolCall = chatwire.ToolCall
type chatResponse = chatwire.Response
type chatResponseChoice = chatwire.ResponseChoice
type chatUsage = chatwire.Usage
type chatError = chatwire.Error

func parseChatFinish(value string) (finishReason, error) {
	switch value {
	case "length", "max_tokens":
		return finishLength, nil
	case "tool_calls", "function_call":
		return finishToolCalls, nil
	case "content_filter":
		return finishContentFilter, nil
	case "stop":
		return finishStop, nil
	default:
		return "", upstreamResponseError(ProtocolChat, "$.choices[0].finish_reason", "unsupported finish reason %q", value)
	}
}
