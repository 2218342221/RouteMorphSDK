package chatresponses

import "encoding/json"

func validatePromptCacheOptions(protocol Protocol, raw json.RawMessage) error {
	if !jsonValuePresent(raw) {
		return nil
	}
	fields, err := rejectUnknownObjectFields(protocol, "$.prompt_cache_options", raw, "mode", "ttl")
	if err != nil {
		return err
	}
	if jsonValuePresent(fields["mode"]) {
		mode := rawString(fields["mode"])
		if mode != "implicit" && mode != "explicit" {
			return invalid(protocol, "$.prompt_cache_options.mode", "must be implicit or explicit")
		}
	}
	if jsonValuePresent(fields["ttl"]) && rawString(fields["ttl"]) != "30m" {
		return invalid(protocol, "$.prompt_cache_options.ttl", "must be 30m")
	}
	return nil
}

func validateModeration(protocol Protocol, raw json.RawMessage) error {
	if !nonNullJSON(raw) {
		return nil
	}
	fields, err := rejectUnknownObjectFields(protocol, "$.moderation", raw, "model", "policy")
	if err != nil {
		return err
	}
	if rawString(fields["model"]) == "" {
		return invalid(protocol, "$.moderation.model", "is required")
	}
	if !jsonValuePresent(fields["policy"]) {
		return nil
	}
	policy, err := rejectUnknownObjectFields(protocol, "$.moderation.policy", fields["policy"], "input", "output")
	if err != nil {
		return err
	}
	for _, side := range []string{"input", "output"} {
		if !jsonValuePresent(policy[side]) {
			continue
		}
		settings, err := rejectUnknownObjectFields(protocol, "$.moderation.policy."+side, policy[side], "mode")
		if err != nil {
			return err
		}
		mode := rawString(settings["mode"])
		if mode != "score" && mode != "block" {
			return invalid(protocol, "$.moderation.policy."+side+".mode", "must be score or block")
		}
	}
	return nil
}
