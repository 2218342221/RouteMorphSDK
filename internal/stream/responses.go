package stream

import (
	"encoding/json"
	"fmt"

	responseswire "github.com/2218342221/RouteMorphSDK/internal/wire/responses"
)

type responsesItem = responseswire.Item

func validateResponsesItems(items []responsesItem, path string) error {
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if item.Phase != "" {
			if path != "$.output" || (item.Phase != "final_answer" && item.Phase != "commentary") {
				return unsupported(ProtocolResponses, itemPath+".phase", "message phase %q has no portable cross-protocol equivalent", item.Phase)
			}
		}
		if len(item.EncryptedContent) > 0 && string(item.EncryptedContent) != "null" {
			return unsupported(ProtocolResponses, itemPath+".encrypted_content", "encrypted reasoning content requires a native Responses provider")
		}
		switch item.Type {
		case "message", "":
			if path == "$.output" && item.Role != "assistant" {
				return upstreamResponseError(ProtocolResponses, itemPath+".role", "output message role must be assistant")
			}
			if item.Role != "user" && item.Role != "assistant" && item.Role != "system" && item.Role != "developer" {
				return invalid(ProtocolResponses, itemPath+".role", "unsupported message role %q", item.Role)
			}
		case "function_call":
			if item.CallID == "" || item.Name == "" {
				return invalid(ProtocolResponses, itemPath, "function_call requires call_id and name")
			}
		case "function_call_output":
			if item.CallID == "" {
				return invalid(ProtocolResponses, itemPath+".call_id", "call_id is required")
			}
		}
	}
	return nil
}

func mustJSONString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

type responsesResponse = responseswire.Response

func validateResponsesTerminal(source responsesResponse) error {
	if source.Status == "failed" || source.Status == "cancelled" || source.Error != nil {
		message := "Responses generation failed"
		if source.Error != nil && source.Error.Message != "" {
			message = source.Error.Message
		}
		return upstreamResponseError(ProtocolResponses, "$.error", "%s", message)
	}
	if source.Status != "completed" && source.Status != "incomplete" {
		return upstreamResponseError(ProtocolResponses, "$.status", "unexpected terminal status %q", source.Status)
	}
	if source.Status == "incomplete" && source.IncompleteDetails != nil && source.IncompleteDetails.Reason != "" && source.IncompleteDetails.Reason != "max_output_tokens" && source.IncompleteDetails.Reason != "content_filter" {
		return upstreamResponseError(ProtocolResponses, "$.incomplete_details.reason", "unsupported incomplete reason %q", source.IncompleteDetails.Reason)
	}
	return validateResponsesItems(source.Output, "$.output")
}
