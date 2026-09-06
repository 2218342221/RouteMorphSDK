//go:build integration

package routemorph

import (
	"strings"
	"testing"
)

type liveExtendedProtocolCase struct {
	protocol Protocol
	slug     string
}

func TestLiveResponsesProviderExtendedIntegration(t *testing.T) {
	config := requireLiveResponsesConfig(t)
	adapter := newLiveResponsesAdapter(t, config)

	t.Run("conversation", func(t *testing.T) {
		for _, test := range liveExtendedProtocols() {
			for _, streaming := range []bool{false, true} {
				mode := "non_stream"
				if streaming {
					mode = "stream"
				}
				id := "extended/conversation/" + test.slug + "/" + mode
				marker := "RM_CONVERSATION_" + strings.ToUpper(test.slug) + "_" + strings.ToUpper(mode)
				t.Run(test.slug+"/"+mode, func(t *testing.T) {
					request := liveRequestFromFixture(t, config, id, test.protocol, streaming, nil)
					summary := invokeLiveResponses(t, adapter, test.protocol, request, streaming, liveStreamText)
					assertLiveExactText(t, summary, marker)
				})
			}
		}
	})

	t.Run("complex_tool_call", func(t *testing.T) {
		for _, test := range liveExtendedProtocols() {
			id := "extended/complex_tool_call/" + test.slug + "/non_stream"
			marker := "RM_COMPLEX_TOOL_" + strings.ToUpper(test.slug) + "_NON_STREAM"
			t.Run(test.slug+"/non_stream", func(t *testing.T) {
				request := liveRequestFromFixture(t, config, id, test.protocol, false, nil)
				summary := invokeLiveResponses(t, adapter, test.protocol, request, false, liveStreamTool)
				if summary.toolName != "record_complex" {
					t.Fatalf("tool name=%q, want record_complex", summary.toolName)
				}
				assertLiveComplexArguments(t, summary.toolArguments, marker)
				assertLiveResponseSummary(t, summary, true)
			})
		}
	})

	t.Run("parallel_tool_result", func(t *testing.T) {
		for _, test := range liveExtendedProtocols() {
			id := "extended/parallel_tool_result/" + test.slug + "/non_stream"
			marker := "RM_PARALLEL_RESULT_" + strings.ToUpper(test.slug) + "_NON_STREAM"
			t.Run(test.slug+"/non_stream", func(t *testing.T) {
				request := liveRequestFromFixture(t, config, id, test.protocol, false, nil)
				summary := invokeLiveResponses(t, adapter, test.protocol, request, false, liveStreamText)
				assertLiveExactText(t, summary, marker)
			})
		}
	})

	t.Run("metadata_service_tier", func(t *testing.T) {
		tests := []struct {
			protocol Protocol
			slug     string
			marker   string
			caseName string
		}{
			{ProtocolChat, "chat", "RM_METADATA_TIER_CHAT_NON_STREAM", "metadata_service_tier_chat"},
			{ProtocolResponses, "responses", "RM_METADATA_TIER_RESPONSES_NON_STREAM", "metadata_service_tier_responses"},
		}
		for _, test := range tests {
			t.Run(test.slug+"/non_stream", func(t *testing.T) {
				id := "extended/metadata_service_tier/" + test.slug + "/non_stream"
				request := liveRequestFromFixture(t, config, id, test.protocol, false, nil)
				raw := invokeLiveToolsRequest(t, adapter, test.protocol, request, false)
				summary := decodeLiveResponse(t, test.protocol, raw)
				assertLiveExactText(t, summary, test.marker)

				var envelope struct {
					Metadata    map[string]string `json:"metadata"`
					ServiceTier string            `json:"service_tier"`
				}
				unmarshalLiveResponse(t, raw, &envelope)
				if len(envelope.Metadata) != 2 || envelope.Metadata["routemorph_suite"] != "extended" || envelope.Metadata["routemorph_case"] != test.caseName {
					t.Fatalf("metadata was not preserved: fields=%d suite_matches=%v case_matches=%v", len(envelope.Metadata), envelope.Metadata["routemorph_suite"] == "extended", envelope.Metadata["routemorph_case"] == test.caseName)
				}
				if envelope.ServiceTier != "auto" && envelope.ServiceTier != "default" {
					t.Fatalf("service_tier=%q, want auto or default", envelope.ServiceTier)
				}
			})
		}
	})
}

func liveExtendedProtocols() []liveExtendedProtocolCase {
	return []liveExtendedProtocolCase{
		{protocol: ProtocolChat, slug: "chat"},
		{protocol: ProtocolResponses, slug: "responses"},
		{protocol: ProtocolMessages, slug: "messages"},
		{protocol: ProtocolGenerateContent, slug: "generate_content"},
	}
}

func assertLiveExactText(t *testing.T, summary liveResponseSummary, marker string) {
	t.Helper()
	if strings.TrimSpace(summary.text) != marker {
		t.Fatalf("converted text mismatch: %s", liveBodyFingerprint([]byte(summary.text)))
	}
	assertLiveResponseSummary(t, summary, false)
}

func assertLiveComplexArguments(t *testing.T, arguments map[string]any, marker string) {
	t.Helper()
	if len(arguments) != 6 {
		t.Fatalf("complex tool arguments fields=%d, want 6", len(arguments))
	}
	if arguments["marker"] != marker || arguments["count"] != float64(7) || arguments["enabled"] != true || arguments["mode"] != "audit" {
		t.Fatalf("complex scalar arguments mismatch: marker=%v count=%v enabled=%v mode=%v", arguments["marker"] == marker, arguments["count"], arguments["enabled"], arguments["mode"])
	}
	tags, ok := arguments["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("complex tags mismatch: type=%T count=%d", arguments["tags"], len(tags))
	}
	payload, ok := arguments["payload"].(map[string]any)
	if !ok || len(payload) != 2 || payload["nested"] != "value" || payload["score"] != float64(3) {
		t.Fatalf("complex payload mismatch: type=%T fields=%d", arguments["payload"], len(payload))
	}
}
