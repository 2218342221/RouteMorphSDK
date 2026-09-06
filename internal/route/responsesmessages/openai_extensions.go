package responsesmessages

import openaicompat "github.com/2218342221/RouteMorphSDK/internal/openaicompat"

func responsesResponseExtensionDiagnostics(source responsesResponse, policy lossPolicy, base string) ([]Diagnostic, error) {
	for path, value := range map[string]int64{
		base + ".usage.input_tokens":                            source.Usage.InputTokens,
		base + ".usage.output_tokens":                           source.Usage.OutputTokens,
		base + ".usage.total_tokens":                            source.Usage.TotalTokens,
		base + ".usage.input_tokens_details.cached_tokens":      source.Usage.InputTokenDetails.CachedTokens,
		base + ".usage.input_tokens_details.cache_write_tokens": source.Usage.InputTokenDetails.CacheWriteTokens,
		base + ".usage.output_tokens_details.reasoning_tokens":  source.Usage.OutputTokenDetails.ReasoningTokens,
	} {
		if value < 0 {
			return nil, upstreamResponseError(ProtocolResponses, path, "must be non-negative")
		}
	}
	var diagnostics []Diagnostic
	serviceTierPresent := messagesRawNonNull(source.ServiceTier)
	if serviceTierPresent {
		implicit, err := openaicompat.IsImplicitResponsesServiceTier(source.ServiceTier, base+".service_tier", true)
		if err != nil {
			return nil, err
		}
		if implicit {
			diagnostics = appendDiagnostic(diagnostics, "warning", "responses_service_tier_not_representable", base+".service_tier", "provider-selected Responses service tier was omitted from the Messages response")
			serviceTierPresent = false
		}
	}
	for _, field := range []struct {
		present bool
		path    string
		code    string
		message string
	}{
		{len(source.Metadata) > 0, base + ".metadata", "responses_metadata_not_representable", "Responses metadata was omitted from the Messages response"},
		{messagesRawNonNull(source.Moderation), base + ".moderation", "responses_moderation_not_representable", "Responses moderation results were omitted from the Messages response"},
		{serviceTierPresent, base + ".service_tier", "responses_service_tier_not_representable", "Responses service tier was omitted from the Messages response"},
		{messagesRawNonNull(source.PromptCacheOptions), base + ".prompt_cache_options", "responses_prompt_cache_options_not_representable", "Responses prompt cache options were omitted from the Messages response"},
	} {
		if !field.present {
			continue
		}
		if policy == rejectSemanticLoss {
			return diagnostics, unsupported(ProtocolResponses, field.path, "%s", field.message)
		}
		diagnostics = appendDiagnostic(diagnostics, "warning", field.code, field.path, field.message)
	}
	return diagnostics, nil
}

func (c *responsesToMessagesStreamConverter) responseExtensionDiagnostics(source responsesResponse, base string) ([]Diagnostic, error) {
	diagnostics, err := responsesResponseExtensionDiagnostics(source, c.LossPolicy, base)
	if err != nil {
		return nil, err
	}
	if c.reportedLosses == nil {
		c.reportedLosses = make(map[string]bool)
	}
	filtered := diagnostics[:0]
	for _, diagnostic := range diagnostics {
		if c.reportedLosses[diagnostic.Code] {
			continue
		}
		c.reportedLosses[diagnostic.Code] = true
		filtered = append(filtered, diagnostic)
	}
	return filtered, nil
}
