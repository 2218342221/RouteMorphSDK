// Package openaicompat contains compatibility rules shared by the OpenAI
// Chat Completions and Responses converters.
package openaicompat

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/2218342221/RouteMorphSDK/internal/core"
)

var commonServiceTiers = map[string]struct{}{
	"auto": {}, "default": {}, "flex": {}, "scale": {}, "priority": {}, "fast": {},
}

var responsesServiceTiers = func() map[string]struct{} {
	result := make(map[string]struct{}, len(commonServiceTiers)+1)
	for tier := range commonServiceTiers {
		result[tier] = struct{}{}
	}
	result["ultrafast"] = struct{}{}
	return result
}()

// ValidateChatServiceTier validates a Chat service tier before it is copied to
// Responses. Every currently documented Chat tier is also valid in Responses.
func ValidateChatServiceTier(raw json.RawMessage, path string, upstream bool) error {
	value, present, err := serviceTier(raw, core.ProtocolChat, path, upstream)
	if err != nil || !present {
		return err
	}
	if _, ok := commonServiceTiers[value]; !ok {
		return sourceError(core.ProtocolChat, path, upstream, "unknown service tier %q", value)
	}
	return nil
}

// ValidateResponsesServiceTierForChat validates a Responses service tier and
// rejects the Responses-only ultrafast tier instead of emitting invalid Chat
// JSON.
func ValidateResponsesServiceTierForChat(raw json.RawMessage, path string, upstream bool) error {
	value, present, err := responsesServiceTier(raw, path, upstream)
	if err != nil || !present {
		return err
	}
	if value == "ultrafast" {
		return core.Unsupported(core.ProtocolResponses, path, "service tier %q has no Chat equivalent", value)
	}
	if _, ok := commonServiceTiers[value]; !ok {
		return sourceError(core.ProtocolResponses, path, upstream, "unknown service tier %q", value)
	}
	return nil
}

// IsImplicitResponsesServiceTier reports whether a Responses service tier is
// provider-selected default metadata. These values may appear even when a
// cross-protocol request did not select a tier, so targets without a service
// tier field can omit them with a diagnostic instead of failing the response.
func IsImplicitResponsesServiceTier(raw json.RawMessage, path string, upstream bool) (bool, error) {
	value, present, err := responsesServiceTier(raw, path, upstream)
	if err != nil || !present {
		return false, err
	}
	return value == "auto" || value == "default", nil
}

// MergeResponsesServiceTierForChat validates stream snapshots and permits the
// documented lifecycle in which an automatic tier selection is resolved to
// the concrete tier used by the terminal response. Other changes still fail
// closed as invalid upstream output.
func MergeResponsesServiceTierForChat(current, next json.RawMessage, path string) (json.RawMessage, error) {
	if err := ValidateResponsesServiceTierForChat(next, path, true); err != nil {
		return nil, err
	}
	nextValue, nextPresent, err := responsesServiceTier(next, path, true)
	if err != nil || !nextPresent {
		return append(json.RawMessage(nil), current...), err
	}
	currentValue, currentPresent, err := responsesServiceTier(current, path, true)
	if err != nil {
		return nil, err
	}
	if !currentPresent || currentValue == nextValue || currentValue == "auto" {
		return append(json.RawMessage(nil), next...), nil
	}
	return nil, sourceError(core.ProtocolResponses, path, true, "changed during stream")
}

func responsesServiceTier(raw json.RawMessage, path string, upstream bool) (string, bool, error) {
	value, present, err := serviceTier(raw, core.ProtocolResponses, path, upstream)
	if err != nil || !present {
		return value, present, err
	}
	if _, ok := responsesServiceTiers[value]; !ok {
		return "", false, sourceError(core.ProtocolResponses, path, upstream, "unknown service tier %q", value)
	}
	return value, true, nil
}

func serviceTier(raw json.RawMessage, protocol core.Protocol, path string, upstream bool) (string, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil || value == "" {
		return "", false, sourceError(protocol, path, upstream, "service tier must be a non-empty string")
	}
	return value, true, nil
}

// ChatModerationToResponses unwraps Chat's one-element moderation_results
// collection into the Responses moderation_result union. Multiple successful
// results have no lossless Responses representation.
func ChatModerationToResponses(raw json.RawMessage, path string, upstream bool) (json.RawMessage, error) {
	return convertModeration(raw, core.ProtocolChat, path, upstream, chatModerationSideToResponses)
}

// ResponsesModerationToChat wraps each Responses moderation_result in Chat's
// moderation_results collection. Error variants have the same wire shape.
func ResponsesModerationToChat(raw json.RawMessage, path string, upstream bool) (json.RawMessage, error) {
	return convertModeration(raw, core.ProtocolResponses, path, upstream, responsesModerationSideToChat)
}

// ValidateCompleteModeration enforces the final-response contract shared by
// both OpenAI APIs. Stream snapshots may arrive one side at a time, but the
// terminal moderation object requires both input and output.
func ValidateCompleteModeration(raw json.RawMessage, protocol core.Protocol, path string, upstream bool) error {
	if !valuePresent(raw) {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return sourceError(protocol, path, upstream, "moderation must be an object")
	}
	for _, name := range []string{"input", "output"} {
		if !valuePresent(fields[name]) {
			return sourceError(protocol, path+"."+name, upstream, "is required in a terminal moderation result")
		}
	}
	return nil
}

// MergeModeration combines input/output moderation observations from separate
// stream snapshots and rejects a side that changes after it was observed.
func MergeModeration(current, next json.RawMessage, protocol core.Protocol, path string, upstream bool) (json.RawMessage, error) {
	if !valuePresent(current) {
		return append(json.RawMessage(nil), next...), nil
	}
	if !valuePresent(next) {
		return append(json.RawMessage(nil), current...), nil
	}
	var left, right map[string]json.RawMessage
	if json.Unmarshal(current, &left) != nil || left == nil || json.Unmarshal(next, &right) != nil || right == nil {
		return nil, sourceError(protocol, path, upstream, "moderation must be an object")
	}
	for name, value := range right {
		if previous, ok := left[name]; ok && valuePresent(previous) && valuePresent(value) && !jsonEqual(previous, value) {
			return nil, sourceError(protocol, path+"."+name, upstream, "moderation result changed during stream")
		}
		if valuePresent(value) {
			left[name] = append(json.RawMessage(nil), value...)
		}
	}
	merged, err := json.Marshal(left)
	if err != nil {
		return nil, sourceError(protocol, path, upstream, "encode moderation: %v", err)
	}
	return merged, nil
}

type moderationSideConverter func(json.RawMessage, string, bool) (json.RawMessage, error)

func convertModeration(raw json.RawMessage, protocol core.Protocol, path string, upstream bool, convert moderationSideConverter) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil || fields == nil {
		return nil, sourceError(protocol, path, upstream, "moderation must be an object")
	}
	for name, value := range fields {
		if name != "input" && name != "output" && valuePresent(value) {
			return nil, core.Unsupported(protocol, path+"."+name, "moderation field has no portable equivalent")
		}
	}
	converted := make(map[string]json.RawMessage, 2)
	for _, name := range []string{"input", "output"} {
		if !valuePresent(fields[name]) {
			continue
		}
		value, err := convert(fields[name], path+"."+name, upstream)
		if err != nil {
			return nil, err
		}
		converted[name] = value
	}
	if len(converted) == 0 {
		return nil, sourceError(protocol, path, upstream, "moderation must contain input or output results")
	}
	result, err := json.Marshal(converted)
	if err != nil {
		return nil, sourceError(protocol, path, upstream, "encode moderation: %v", err)
	}
	return result, nil
}

func chatModerationSideToResponses(raw json.RawMessage, path string, upstream bool) (json.RawMessage, error) {
	fields, variant, err := moderationVariant(raw, core.ProtocolChat, path, upstream)
	if err != nil {
		return nil, err
	}
	switch variant {
	case "error":
		if err := validateModerationError(fields, core.ProtocolChat, path, upstream); err != nil {
			return nil, err
		}
		return append(json.RawMessage(nil), raw...), nil
	case "moderation_results":
		for name, value := range fields {
			switch name {
			case "type", "model", "results":
			default:
				if valuePresent(value) {
					return nil, core.Unsupported(core.ProtocolChat, path+"."+name, "moderation results field has no Responses equivalent")
				}
			}
		}
		model, err := requiredString(fields["model"], core.ProtocolChat, path+".model", upstream)
		if err != nil {
			return nil, err
		}
		var results []json.RawMessage
		if err := json.Unmarshal(fields["results"], &results); err != nil {
			return nil, sourceError(core.ProtocolChat, path+".results", upstream, "must be an array")
		}
		if len(results) != 1 {
			return nil, core.Unsupported(core.ProtocolChat, path+".results", "Responses requires exactly one moderation result, got %d", len(results))
		}
		resultFields, resultType, err := moderationVariant(results[0], core.ProtocolChat, path+".results[0]", upstream)
		if err != nil {
			return nil, err
		}
		if resultType != "moderation_result" {
			return nil, sourceError(core.ProtocolChat, path+".results[0].type", upstream, "must be moderation_result")
		}
		innerModel, err := validateModerationResult(resultFields, core.ProtocolChat, path+".results[0]", upstream)
		if err != nil {
			return nil, err
		}
		if innerModel != model {
			return nil, core.Unsupported(core.ProtocolChat, path+".model", "outer and result moderation models differ")
		}
		return append(json.RawMessage(nil), results[0]...), nil
	default:
		return nil, sourceError(core.ProtocolChat, path+".type", upstream, "unsupported moderation variant %q", variant)
	}
}

func responsesModerationSideToChat(raw json.RawMessage, path string, upstream bool) (json.RawMessage, error) {
	fields, variant, err := moderationVariant(raw, core.ProtocolResponses, path, upstream)
	if err != nil {
		return nil, err
	}
	switch variant {
	case "error":
		if err := validateModerationError(fields, core.ProtocolResponses, path, upstream); err != nil {
			return nil, err
		}
		return append(json.RawMessage(nil), raw...), nil
	case "moderation_result":
		model, err := validateModerationResult(fields, core.ProtocolResponses, path, upstream)
		if err != nil {
			return nil, err
		}
		result, err := json.Marshal(map[string]any{
			"type": "moderation_results", "model": model, "results": []json.RawMessage{raw},
		})
		if err != nil {
			return nil, sourceError(core.ProtocolResponses, path, upstream, "encode moderation: %v", err)
		}
		return result, nil
	default:
		return nil, sourceError(core.ProtocolResponses, path+".type", upstream, "unsupported moderation variant %q", variant)
	}
}

func moderationVariant(raw json.RawMessage, protocol core.Protocol, path string, upstream bool) (map[string]json.RawMessage, string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, "", sourceError(protocol, path, upstream, "moderation result must be an object")
	}
	variant, err := requiredString(fields["type"], protocol, path+".type", upstream)
	return fields, variant, err
}

func validateModerationResult(fields map[string]json.RawMessage, protocol core.Protocol, path string, upstream bool) (string, error) {
	allowed := map[string]struct{}{
		"type": {}, "categories": {}, "category_applied_input_types": {}, "category_scores": {}, "flagged": {}, "model": {},
	}
	for name, value := range fields {
		if _, ok := allowed[name]; !ok && valuePresent(value) {
			return "", core.Unsupported(protocol, path+"."+name, "moderation result field has no portable equivalent")
		}
	}
	for _, name := range []string{"categories", "category_applied_input_types", "category_scores", "flagged"} {
		if !valuePresent(fields[name]) {
			return "", sourceError(protocol, path+"."+name, upstream, "is required")
		}
	}
	var categories map[string]bool
	if err := json.Unmarshal(fields["categories"], &categories); err != nil || categories == nil {
		return "", sourceError(protocol, path+".categories", upstream, "must be an object of booleans")
	}
	var applied map[string][]string
	if err := json.Unmarshal(fields["category_applied_input_types"], &applied); err != nil || applied == nil {
		return "", sourceError(protocol, path+".category_applied_input_types", upstream, "must be an object of string arrays")
	}
	var scores map[string]float64
	if err := json.Unmarshal(fields["category_scores"], &scores); err != nil || scores == nil {
		return "", sourceError(protocol, path+".category_scores", upstream, "must be an object of numbers")
	}
	var flagged bool
	if err := json.Unmarshal(fields["flagged"], &flagged); err != nil {
		return "", sourceError(protocol, path+".flagged", upstream, "must be a boolean")
	}
	return requiredString(fields["model"], protocol, path+".model", upstream)
}

func validateModerationError(fields map[string]json.RawMessage, protocol core.Protocol, path string, upstream bool) error {
	for name, value := range fields {
		switch name {
		case "type", "code", "message":
		default:
			if valuePresent(value) {
				return core.Unsupported(protocol, path+"."+name, "moderation error field has no portable equivalent")
			}
		}
	}
	if _, err := requiredString(fields["code"], protocol, path+".code", upstream); err != nil {
		return err
	}
	_, err := requiredString(fields["message"], protocol, path+".message", upstream)
	return err
}

func requiredString(raw json.RawMessage, protocol core.Protocol, path string, upstream bool) (string, error) {
	var value string
	if !valuePresent(raw) || json.Unmarshal(raw, &value) != nil || value == "" {
		return "", sourceError(protocol, path, upstream, "must be a non-empty string")
	}
	return value, nil
}

func valuePresent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func jsonEqual(left, right json.RawMessage) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

func sourceError(protocol core.Protocol, path string, upstream bool, format string, args ...any) error {
	if upstream {
		return core.UpstreamResponseError(protocol, path, format, args...)
	}
	return core.Invalid(protocol, path, format, args...)
}
