package openaicompat

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/2218342221/RouteMorphSDK/internal/core"
)

const moderationResult = `{"type":"moderation_result","model":"omni-moderation-latest","categories":{},"category_applied_input_types":{},"category_scores":{},"flagged":false}`

func TestModerationResultRoundTrip(t *testing.T) {
	responses := json.RawMessage(`{"input":` + moderationResult + `}`)
	chat, err := ResponsesModerationToChat(responses, "$.moderation", true)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Input struct {
			Type    string            `json:"type"`
			Model   string            `json:"model"`
			Results []json.RawMessage `json:"results"`
		} `json:"input"`
	}
	if err := json.Unmarshal(chat, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Input.Type != "moderation_results" || envelope.Input.Model != "omni-moderation-latest" || len(envelope.Input.Results) != 1 {
		t.Fatalf("wrapped moderation = %s", chat)
	}
	roundTrip, err := ChatModerationToResponses(chat, "$.moderation", true)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(responses, roundTrip) {
		t.Fatalf("round trip = %s, want %s", roundTrip, responses)
	}
}

func TestChatModerationMultipleResultsFailsClosed(t *testing.T) {
	raw := json.RawMessage(`{"output":{"type":"moderation_results","model":"omni-moderation-latest","results":[` + moderationResult + `,` + moderationResult + `]}}`)
	_, err := ChatModerationToResponses(raw, "$.moderation", true)
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestTerminalModerationRequiresBothSides(t *testing.T) {
	raw := json.RawMessage(`{"input":` + moderationResult + `}`)
	if err := ValidateCompleteModeration(raw, core.ProtocolResponses, "$.moderation", true); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("error = %v, want ErrUpstreamResponse", err)
	}
}

func TestServiceTierIntersection(t *testing.T) {
	if err := ValidateChatServiceTier(json.RawMessage(`"fast"`), "$.service_tier", false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResponsesServiceTierForChat(json.RawMessage(`"ultrafast"`), "$.service_tier", false); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("ultrafast error = %v, want ErrUnsupported", err)
	}
	if err := ValidateResponsesServiceTierForChat(json.RawMessage(`"future"`), "$.service_tier", true); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("unknown upstream tier error = %v, want ErrUpstreamResponse", err)
	}
	if err := ValidateChatServiceTier(json.RawMessage(`7`), "$.service_tier", false); !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("malformed request tier error = %v, want ErrInvalidPayload", err)
	}
}

func TestResponsesServiceTierStreamResolution(t *testing.T) {
	merged, err := MergeResponsesServiceTierForChat(nil, json.RawMessage(`"auto"`), "$.response.service_tier")
	if err != nil || string(merged) != `"auto"` {
		t.Fatalf("initial tier = %s, error = %v", merged, err)
	}
	merged, err = MergeResponsesServiceTierForChat(merged, json.RawMessage(`"default"`), "$.response.service_tier")
	if err != nil || string(merged) != `"default"` {
		t.Fatalf("resolved tier = %s, error = %v", merged, err)
	}
	if _, err := MergeResponsesServiceTierForChat(merged, json.RawMessage(`"priority"`), "$.response.service_tier"); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("concrete tier change error = %v, want ErrUpstreamResponse", err)
	}
	if _, err := MergeResponsesServiceTierForChat(json.RawMessage(`"auto"`), json.RawMessage(`"ultrafast"`), "$.response.service_tier"); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("ultrafast transition error = %v, want ErrUnsupported", err)
	}
}

func TestImplicitResponsesServiceTier(t *testing.T) {
	for _, tier := range []string{"auto", "default"} {
		implicit, err := IsImplicitResponsesServiceTier(json.RawMessage(`"`+tier+`"`), "$.service_tier", true)
		if err != nil || !implicit {
			t.Errorf("tier %q implicit=%v error=%v", tier, implicit, err)
		}
	}
	implicit, err := IsImplicitResponsesServiceTier(json.RawMessage(`"priority"`), "$.service_tier", true)
	if err != nil || implicit {
		t.Errorf("priority implicit=%v error=%v", implicit, err)
	}
	if _, err := IsImplicitResponsesServiceTier(json.RawMessage(`"future"`), "$.service_tier", true); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("unknown tier error = %v, want ErrUpstreamResponse", err)
	}
}

func TestMergeModerationAccumulatesSidesAndRejectsChanges(t *testing.T) {
	input := json.RawMessage(`{"input":` + moderationResult + `}`)
	output := json.RawMessage(`{"output":` + moderationResult + `}`)
	merged, err := MergeModeration(input, output, core.ProtocolResponses, "$.moderation", true)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(merged, &fields); err != nil || len(fields) != 2 {
		t.Fatalf("merged moderation = %s, error = %v", merged, err)
	}
	changed := json.RawMessage(`{"input":{"type":"error","code":"changed","message":"changed"}}`)
	if _, err := MergeModeration(input, changed, core.ProtocolResponses, "$.moderation", true); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("changed moderation error = %v, want ErrUpstreamResponse", err)
	}
}
