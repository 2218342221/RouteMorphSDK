package responsesmessages

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// normalizeClaudeCodeRequest removes only the coding-client controls enabled
// by the narrow compatibility profile. Unknown cache policies and
// state-changing context edits remain fail-closed.
func normalizeClaudeCodeRequest(input []byte, options conversionOptions) ([]byte, []Diagnostic, error) {
	compatible := codingAgentCompatibility(options)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(input, &envelope); err != nil {
		return nil, nil, invalid(ProtocolMessages, "$", "invalid JSON: %v", err)
	}
	if raw, present := envelope["context_management"]; present && jsonValuePresent(raw) {
		if !compatible {
			return nil, nil, unsupported(ProtocolMessages, "$.context_management", "field is not supported by this cross-protocol route")
		}
		if err := validateNoopClaudeContextManagement(raw); err != nil {
			return nil, nil, err
		}
		delete(envelope, "context_management")
	}
	if !compatible {
		return input, nil, nil
	}

	var diagnostics []Diagnostic
	var err error
	if raw, present := envelope["cache_control"]; present && jsonValuePresent(raw) {
		if err := validateEphemeralCacheControl(raw, "$.cache_control"); err != nil {
			return nil, nil, err
		}
		delete(envelope, "cache_control")
		diagnostics = appendDiagnostic(diagnostics, "warning", "cache_control_not_representable", "$.cache_control", "Messages ephemeral cache control was omitted")
	}
	if envelope["system"], diagnostics, err = stripCacheControlFromBlocks(envelope["system"], "$.system", diagnostics); err != nil {
		return nil, nil, err
	}
	if envelope["messages"], diagnostics, err = stripCacheControlFromMessages(envelope["messages"], diagnostics); err != nil {
		return nil, nil, err
	}
	if envelope["tools"], diagnostics, err = stripCacheControlFromObjects(envelope["tools"], "$.tools", diagnostics); err != nil {
		return nil, nil, err
	}
	normalized, err := json.Marshal(envelope)
	if err != nil {
		return nil, nil, invalid(ProtocolMessages, "$", "cannot normalize request: %v", err)
	}
	return normalized, diagnostics, nil
}

func validateNoopClaudeContextManagement(raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return invalid(ProtocolMessages, "$.context_management", "must be an object")
	}
	if len(object) != 1 || !jsonValuePresent(object["edits"]) {
		return unsupported(ProtocolMessages, "$.context_management", "only clear_thinking keep=all is a no-op across protocols")
	}
	var edits []map[string]json.RawMessage
	if err := json.Unmarshal(object["edits"], &edits); err != nil || len(edits) == 0 {
		return invalid(ProtocolMessages, "$.context_management.edits", "must be a non-empty array")
	}
	for index, edit := range edits {
		path := fmt.Sprintf("$.context_management.edits[%d]", index)
		if len(edit) != 2 || rawString(edit["type"]) != "clear_thinking_20251015" || rawString(edit["keep"]) != "all" {
			return unsupported(ProtocolMessages, path, "only clear_thinking_20251015 with keep=all is a no-op across protocols")
		}
	}
	return nil
}

func validateClaudeCodeThinkingCompatibility(thinking *messagesThinking, options conversionOptions) error {
	if thinking == nil || thinking.Type == "disabled" {
		return nil
	}
	if !options.CodingAgentCompatibility {
		if options.LossPolicy != rejectSemanticLoss {
			return nil
		}
		return unsupported(ProtocolMessages, "$.thinking", "Messages thinking configuration is not semantically equivalent to Responses reasoning")
	}
	if thinking.Type != "enabled" && thinking.Type != "adaptive" {
		return unsupported(ProtocolMessages, "$.thinking.type", "only Claude Code enabled or adaptive thinking is supported in coding-agent compatibility mode")
	}
	if thinking.Display != "" && thinking.Display != "omitted" {
		return unsupported(ProtocolMessages, "$.thinking.display", "only omitted thinking display is supported in coding-agent compatibility mode")
	}
	if thinking.Type == "adaptive" && thinking.Display != "omitted" {
		return unsupported(ProtocolMessages, "$.thinking.display", "Claude Code adaptive thinking requires omitted display in coding-agent compatibility mode")
	}
	return nil
}

func stripCacheControlFromMessages(raw json.RawMessage, diagnostics []Diagnostic) (json.RawMessage, []Diagnostic, error) {
	if !jsonValuePresent(raw) {
		return raw, diagnostics, nil
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return raw, diagnostics, nil
	}
	for index := range messages {
		content, next, err := stripCacheControlFromBlocks(messages[index]["content"], fmt.Sprintf("$.messages[%d].content", index), diagnostics)
		if err != nil {
			return nil, nil, err
		}
		messages[index]["content"] = content
		diagnostics = next
	}
	return mustJSON(messages), diagnostics, nil
}

func stripCacheControlFromBlocks(raw json.RawMessage, path string, diagnostics []Diagnostic) (json.RawMessage, []Diagnostic, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] == '"' {
		return raw, diagnostics, nil
	}
	return stripCacheControlFromObjects(raw, path, diagnostics)
}

func stripCacheControlFromObjects(raw json.RawMessage, path string, diagnostics []Diagnostic) (json.RawMessage, []Diagnostic, error) {
	if !jsonValuePresent(raw) {
		return raw, diagnostics, nil
	}
	var objects []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &objects); err != nil {
		return raw, diagnostics, nil
	}
	for index := range objects {
		objectPath := fmt.Sprintf("%s[%d]", path, index)
		cache, present := objects[index]["cache_control"]
		if present && jsonValuePresent(cache) {
			cachePath := objectPath + ".cache_control"
			if err := validateEphemeralCacheControl(cache, cachePath); err != nil {
				return nil, nil, err
			}
			delete(objects[index], "cache_control")
			diagnostics = appendDiagnostic(diagnostics, "warning", "cache_control_not_representable", cachePath, "Messages ephemeral cache control was omitted")
		}
		if rawString(objects[index]["type"]) != "tool_result" || !jsonValuePresent(objects[index]["content"]) {
			continue
		}
		content, next, err := stripCacheControlFromBlocks(objects[index]["content"], objectPath+".content", diagnostics)
		if err != nil {
			return nil, nil, err
		}
		objects[index]["content"] = content
		diagnostics = next
	}
	return mustJSON(objects), diagnostics, nil
}

func validateEphemeralCacheControl(raw json.RawMessage, path string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return invalid(ProtocolMessages, path, "must be an object")
	}
	if len(object) != 1 || rawString(object["type"]) != "ephemeral" {
		return unsupported(ProtocolMessages, path, "only ephemeral cache control can be omitted in coding-agent compatibility mode")
	}
	return nil
}

func normalizedMessagesSafetyIdentifier(metadata map[string]string, path string, options conversionOptions) (string, []Diagnostic, error) {
	identifier, err := messagesSafetyIdentifier(metadata, path)
	if err == nil || !codingAgentCompatibility(options) {
		return identifier, nil, err
	}
	for key := range metadata {
		if key != "user_id" {
			return "", nil, err
		}
	}
	userID := metadata["user_id"]
	if utf8.RuneCountInString(userID) <= 64 {
		return "", nil, err
	}
	digest := sha256.Sum256([]byte("routemorph:messages-user-id:v1\x00" + userID))
	identifier = hex.EncodeToString(digest[:])
	diagnostics := appendDiagnostic(nil, "warning", "metadata_user_id_hashed", path+".user_id", "Messages user_id exceeded the Responses limit and was replaced by a stable SHA-256 pseudonym")
	return identifier, diagnostics, nil
}
