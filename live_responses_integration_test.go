//go:build integration

package routemorph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
	streamx "github.com/2218342221/RouteMorphSDK/internal/stream"
)

type liveStreamKind string

const (
	liveStreamText liveStreamKind = "text"
	liveStreamTool liveStreamKind = "tool"
)

type liveResponsesConfig struct {
	baseURL string
	apiKey  string
	model   string
	source  string
}

type liveResponseSummary struct {
	protocol      Protocol
	model         string
	text          string
	toolID        string
	toolName      string
	toolArguments map[string]any
	toolCalls     int
	terminal      string
	inputTokens   int64
	outputTokens  int64
	totalTokens   int64
}

func TestLiveResponsesProviderTextMatrix(t *testing.T) {
	config := requireLiveResponsesConfig(t)
	adapter := newLiveResponsesAdapter(t, config)
	for _, protocol := range liveIngressProtocols() {
		for _, streaming := range []bool{false, true} {
			name := string(protocol) + "/non_stream"
			if streaming {
				name = string(protocol) + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				marker := "RM_TEXT_" + strings.ToUpper(strings.ReplaceAll(name, "/", "_"))
				request := liveRequestFromFixture(t, config, liveCoreFixtureID(t, "text", protocol, streaming), protocol, streaming, nil)
				summary := invokeLiveResponses(t, adapter, protocol, request, streaming, liveStreamText)
				if strings.TrimSpace(summary.text) != marker {
					t.Fatalf("converted text mismatch: %s", liveBodyFingerprint([]byte(summary.text)))
				}
				assertLiveResponseSummary(t, summary, false)
			})
		}
	}
}

func TestLiveResponsesProviderToolCallMatrix(t *testing.T) {
	config := requireLiveResponsesConfig(t)
	adapter := newLiveResponsesAdapter(t, config)
	for _, protocol := range liveIngressProtocols() {
		for _, streaming := range []bool{false, true} {
			name := string(protocol) + "/non_stream"
			if streaming {
				name = string(protocol) + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				marker := "RM_TOOL_" + strings.ToUpper(strings.ReplaceAll(name, "/", "_"))
				request := liveRequestFromFixture(t, config, liveCoreFixtureID(t, "tool_call", protocol, streaming), protocol, streaming, nil)
				summary := invokeLiveResponses(t, adapter, protocol, request, streaming, liveStreamTool)
				if summary.toolName != "record_marker" {
					t.Fatalf("tool name = %q, want record_marker", summary.toolName)
				}
				if summary.toolArguments["marker"] != marker {
					t.Fatalf("tool marker mismatch; argument_fields=%d", len(summary.toolArguments))
				}
				if len(summary.toolArguments) != 1 {
					t.Fatalf("tool arguments contain unexpected fields: count=%d", len(summary.toolArguments))
				}
				assertLiveResponseSummary(t, summary, true)
			})
		}
	}
}

func TestLiveResponsesProviderToolResultMatrix(t *testing.T) {
	config := requireLiveResponsesConfig(t)
	adapter := newLiveResponsesAdapter(t, config)
	for _, protocol := range liveIngressProtocols() {
		for _, streaming := range []bool{false, true} {
			name := string(protocol) + "/non_stream"
			if streaming {
				name = string(protocol) + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				marker := "RM_RESULT_" + strings.ToUpper(strings.ReplaceAll(name, "/", "_"))
				request := liveRequestFromFixture(t, config, liveCoreFixtureID(t, "tool_result", protocol, streaming), protocol, streaming, nil)
				summary := invokeLiveResponses(t, adapter, protocol, request, streaming, liveStreamText)
				if strings.TrimSpace(summary.text) != marker {
					t.Fatalf("tool-result continuation text mismatch: %s", liveBodyFingerprint([]byte(summary.text)))
				}
				assertLiveResponseSummary(t, summary, false)
			})
		}
	}
}

func TestLiveResponsesProviderStructuredOutputMatrix(t *testing.T) {
	config := requireLiveResponsesConfig(t)
	adapter := newLiveResponsesAdapter(t, config)
	for _, protocol := range liveIngressProtocols() {
		for _, streaming := range []bool{false, true} {
			name := string(protocol) + "/non_stream"
			if streaming {
				name = string(protocol) + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				marker := "RM_JSON_" + strings.ToUpper(strings.ReplaceAll(name, "/", "_"))
				request := liveRequestFromFixture(t, config, liveCoreFixtureID(t, "structured_output", protocol, streaming), protocol, streaming, nil)
				summary := invokeLiveResponses(t, adapter, protocol, request, streaming, liveStreamText)
				var value map[string]any
				if err := json.Unmarshal([]byte(summary.text), &value); err != nil {
					t.Fatalf("structured output is not JSON: %v; body=%s", err, liveBodyFingerprint([]byte(summary.text)))
				}
				if value["marker"] != marker || len(value) != 1 {
					t.Fatalf("structured output mismatch: fields=%d marker_matches=%v", len(value), value["marker"] == marker)
				}
				assertLiveResponseSummary(t, summary, false)
			})
		}
	}
}

func requireLiveResponsesConfig(t *testing.T) liveResponsesConfig {
	t.Helper()
	if os.Getenv("ROUTEMORPH_LIVE_RESPONSES") != "1" {
		t.Skip("set ROUTEMORPH_LIVE_RESPONSES=1 to run billable provider tests")
	}
	config := liveResponsesConfig{
		baseURL: strings.TrimSpace(os.Getenv("ROUTEMORPH_LIVE_BASE_URL")),
		apiKey:  os.Getenv("ROUTEMORPH_LIVE_API_KEY"),
		model:   strings.TrimSpace(os.Getenv("ROUTEMORPH_LIVE_MODEL")),
		source:  strings.TrimSpace(os.Getenv("ROUTEMORPH_LIVE_X_SOURCE")),
	}
	if config.baseURL == "" || config.apiKey == "" {
		t.Fatal("ROUTEMORPH_LIVE_BASE_URL and ROUTEMORPH_LIVE_API_KEY are required")
	}
	endpoint, err := url.Parse(config.baseURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		t.Fatal("ROUTEMORPH_LIVE_BASE_URL must be an HTTPS URL without user info, query, or fragment")
	}
	if config.model == "" {
		config.model = "gpt-5.4"
	}
	return config
}

func newLiveResponsesAdapter(t *testing.T, config liveResponsesConfig) *Adapter {
	t.Helper()
	adapter, err := NewOpenAIResponsesAdapter(config.baseURL, config.apiKey, WithModel(config.model))
	if err != nil {
		t.Fatalf("create live adapter failed (%T)", err)
	}
	return adapter
}

func liveRequestFromFixture(t *testing.T, config liveResponsesConfig, id string, protocol Protocol, streaming bool, replacements map[string]any) *Request {
	t.Helper()
	fixture := e2eFixtureByID(t, id)
	if fixture.Protocol != protocol || fixture.Stream != streaming {
		t.Fatalf("fixture %q metadata protocol=%q stream=%v, want protocol=%q stream=%v", id, fixture.Protocol, fixture.Stream, protocol, streaming)
	}
	body := renderE2ERequestFixture(t, fixture, replacements)
	return newE2EFixtureRequest(t, fixture, body, config.source)
}

func liveIngressProtocols() []Protocol {
	return []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
}

func liveCoreFixtureID(t *testing.T, capability string, protocol Protocol, streaming bool) string {
	t.Helper()
	protocolSlug := ""
	switch protocol {
	case ProtocolChat:
		protocolSlug = "chat"
	case ProtocolResponses:
		protocolSlug = "responses"
	case ProtocolMessages:
		protocolSlug = "messages"
	case ProtocolGenerateContent:
		protocolSlug = "generate_content"
	default:
		t.Fatalf("unsupported protocol %q", protocol)
	}
	mode := "non_stream"
	if streaming {
		mode = "stream"
	}
	return "core/" + capability + "/" + protocolSlug + "/" + mode
}

func invokeLiveResponses(t *testing.T, adapter *Adapter, protocol Protocol, request *Request, streaming bool, streamKind liveStreamKind) liveResponseSummary {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	response, err := invokeAdapter(ctx, adapter, protocol, request)
	if err != nil {
		t.Fatalf("invoke %s failed (%T)", protocol, err)
	}
	if response == nil || response.Body == nil {
		t.Fatal("provider returned a nil response or body")
	}
	if response.StatusCode != http.StatusOK {
		detail := liveProviderErrorSummary(response.Body)
		_ = response.Body.Close()
		t.Fatalf("%s status=%d %s", protocol, response.StatusCode, detail)
	}
	wantMode := RouteModeIncremental
	if protocol == ProtocolResponses {
		wantMode = RouteModeNative
	}
	if response.Meta.IngressProtocol != protocol || response.Meta.UpstreamProtocol != ProtocolResponses || response.Meta.Stream != streaming || response.Meta.RouteMode != wantMode {
		t.Fatalf("unexpected response metadata: %#v", response.Meta)
	}
	var terminal []byte
	transferred := 0
	if streaming {
		terminal, transferred = collectLiveStream(t, ctx, protocol, streamKind, response.Body)
	} else {
		terminal, err = io.ReadAll(response.Body)
		transferred = len(terminal)
		if err != nil {
			_ = response.Body.Close()
			t.Fatalf("read %s response failed (%T)", protocol, err)
		}
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close %s response failed (%T)", protocol, err)
	}
	if err := codec.New(core.Protocol(protocol)).ValidateResponse(context.Background(), terminal); err != nil {
		t.Fatalf("invalid converted %s response (%T): %s", protocol, err, liveBodyFingerprint(terminal))
	}
	diagnosticCodes := validateLiveDiagnostics(t, response.Meta.Diagnostics())
	summary := decodeLiveResponse(t, protocol, terminal)
	t.Logf("protocol=%s stream=%v bytes=%d input_tokens=%d output_tokens=%d diagnostics=%v", protocol, streaming, transferred, summary.inputTokens, summary.outputTokens, diagnosticCodes)
	return summary
}

func liveProviderErrorSummary(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return fmt.Sprintf("error_body_read=%T", err)
	}
	var envelope struct {
		Error struct {
			Type  string `json:"type"`
			Code  string `json:"code"`
			Param string `json:"param"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &envelope)
	return fmt.Sprintf("error_type=%q error_code=%q error_param=%q %s", envelope.Error.Type, envelope.Error.Code, envelope.Error.Param, liveBodyFingerprint(raw))
}

func collectLiveStream(t *testing.T, ctx context.Context, protocol Protocol, streamKind liveStreamKind, body io.Reader) ([]byte, int) {
	t.Helper()
	decoder, err := codec.New(core.Protocol(protocol)).NewStreamDecoder(body, core.StreamOptions{MaxFrameBytes: 32 << 20})
	if err != nil {
		t.Fatalf("create %s stream decoder failed (%T)", protocol, err)
	}
	frames := make([]core.Frame, 0, 16)
	transferred := 0
	for {
		frame, err := decoder.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode %s stream failed after %d payload bytes (%T)", protocol, transferred, err)
		}
		frames = append(frames, frame)
		transferred += len(frame.Data)
	}
	validateLiveStreamFrames(t, protocol, streamKind, frames)
	terminal, diagnostics, err := streamx.CollectNativeResponse(core.Protocol(protocol), frames, core.RejectSemanticLoss)
	if err != nil {
		t.Fatalf("collect %s stream failed (%T)", protocol, err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("collect %s stream returned unexpected diagnostics: %#v", protocol, diagnostics)
	}
	return terminal, transferred
}

func validateLiveStreamFrames(t *testing.T, protocol Protocol, streamKind liveStreamKind, frames []core.Frame) {
	t.Helper()
	if len(frames) == 0 {
		t.Fatal("stream contained no frames")
	}
	switch protocol {
	case ProtocolChat:
		var sawSemanticDelta, sawTerminal, sawDone bool
		for _, frame := range frames {
			if frame.Done {
				sawDone = true
				continue
			}
			var payload struct {
				Choices []struct {
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Function struct {
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason *string `json:"finish_reason"`
				} `json:"choices"`
			}
			unmarshalLiveResponse(t, frame.Data, &payload)
			for _, choice := range payload.Choices {
				if choice.FinishReason == nil {
					switch streamKind {
					case liveStreamText:
						sawSemanticDelta = sawSemanticDelta || choice.Delta.Content != ""
					case liveStreamTool:
						for _, toolCall := range choice.Delta.ToolCalls {
							sawSemanticDelta = sawSemanticDelta || toolCall.Function.Arguments != ""
						}
					}
				}
				sawTerminal = sawTerminal || choice.FinishReason != nil && *choice.FinishReason != ""
			}
		}
		if !sawSemanticDelta || !sawTerminal || !sawDone {
			t.Fatalf("incomplete Chat stream lifecycle: semantic_delta=%v terminal=%v done=%v", sawSemanticDelta, sawTerminal, sawDone)
		}
	case ProtocolResponses:
		events := make([]string, 0, len(frames))
		var previous *int64
		var sawSemanticDelta bool
		for _, frame := range frames {
			if frame.Done {
				t.Fatal("Responses stream unexpectedly contained a [DONE] frame")
			}
			var payload struct {
				Type           string `json:"type"`
				SequenceNumber *int64 `json:"sequence_number"`
			}
			unmarshalLiveResponse(t, frame.Data, &payload)
			if payload.Type == "" || payload.SequenceNumber == nil {
				t.Fatal("Responses stream event omitted type or sequence_number")
			}
			if previous != nil && *payload.SequenceNumber != *previous+1 {
				t.Fatalf("Responses sequence_number=%d after %d", *payload.SequenceNumber, *previous)
			}
			value := *payload.SequenceNumber
			previous = &value
			events = append(events, payload.Type)
			if payload.Type != "response.completed" {
				sawSemanticDelta = sawSemanticDelta || streamKind == liveStreamText && payload.Type == "response.output_text.delta"
				sawSemanticDelta = sawSemanticDelta || streamKind == liveStreamTool && payload.Type == "response.function_call_arguments.delta"
			}
		}
		if events[0] != "response.created" || events[len(events)-1] != "response.completed" || !sawSemanticDelta {
			t.Fatalf("Responses stream lifecycle first=%q last=%q semantic_delta=%v", events[0], events[len(events)-1], sawSemanticDelta)
		}
	case ProtocolMessages:
		events := make([]string, 0, len(frames))
		var sawSemanticDelta bool
		for _, frame := range frames {
			if frame.Done {
				t.Fatal("Messages stream unexpectedly contained a [DONE] frame")
			}
			var payload struct {
				Type  string `json:"type"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			unmarshalLiveResponse(t, frame.Data, &payload)
			events = append(events, payload.Type)
			if payload.Type == "content_block_delta" {
				sawSemanticDelta = sawSemanticDelta || streamKind == liveStreamText && payload.Delta.Type == "text_delta" && payload.Delta.Text != ""
				sawSemanticDelta = sawSemanticDelta || streamKind == liveStreamTool && payload.Delta.Type == "input_json_delta" && payload.Delta.PartialJSON != ""
			}
		}
		if events[0] != "message_start" || events[len(events)-1] != "message_stop" || !sawSemanticDelta {
			t.Fatalf("incomplete Messages stream lifecycle: first=%q last=%q semantic_delta=%v", events[0], events[len(events)-1], sawSemanticDelta)
		}
	case ProtocolGenerateContent:
		var sawSemanticDelta, sawTerminal bool
		for _, frame := range frames {
			if frame.Done {
				t.Fatal("Gemini stream unexpectedly contained a [DONE] frame")
			}
			var payload struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text         string `json:"text"`
							FunctionCall *struct {
								Args json.RawMessage `json:"args"`
							} `json:"functionCall"`
						} `json:"parts"`
					} `json:"content"`
					FinishReason string `json:"finishReason"`
				} `json:"candidates"`
			}
			unmarshalLiveResponse(t, frame.Data, &payload)
			for _, candidate := range payload.Candidates {
				if candidate.FinishReason == "" {
					for _, part := range candidate.Content.Parts {
						sawSemanticDelta = sawSemanticDelta || streamKind == liveStreamText && part.Text != ""
						sawSemanticDelta = sawSemanticDelta || streamKind == liveStreamTool && part.FunctionCall != nil && len(bytes.TrimSpace(part.FunctionCall.Args)) > 0
					}
				}
				sawTerminal = sawTerminal || candidate.FinishReason != ""
			}
		}
		if !sawSemanticDelta || !sawTerminal {
			t.Fatalf("incomplete Gemini stream lifecycle: semantic_delta=%v terminal=%v", sawSemanticDelta, sawTerminal)
		}
	default:
		t.Fatalf("unsupported protocol %q", protocol)
	}
}

func validateLiveDiagnostics(t *testing.T, diagnostics []Diagnostic) []string {
	t.Helper()
	allowed := map[string]bool{
		"gemini_thought_signature_unavailable":           true,
		"responses_item_id_not_representable":            true,
		"responses_output_phase_not_representable":       true,
		"responses_service_tier_not_representable":       true,
		"responses_stream_obfuscation_not_representable": true,
		"responses_web_search_call_not_representable":    true,
		"stream_input_usage_deferred":                    true,
	}
	codes := make([]string, 0, len(diagnostics))
	seen := make(map[string]bool, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != "warning" || !allowed[diagnostic.Code] {
			t.Fatalf("unexpected diagnostic severity=%q code=%q path=%q", diagnostic.Severity, diagnostic.Code, diagnostic.Path)
		}
		if seen[diagnostic.Code] {
			t.Fatalf("duplicate diagnostic code=%q", diagnostic.Code)
		}
		seen[diagnostic.Code] = true
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

func decodeLiveResponse(t *testing.T, protocol Protocol, body []byte) liveResponseSummary {
	t.Helper()
	switch protocol {
	case ProtocolChat:
		var response struct {
			Model   string `json:"model"`
			Choices []struct {
				Message struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				Input  int64 `json:"prompt_tokens"`
				Output int64 `json:"completion_tokens"`
				Total  int64 `json:"total_tokens"`
			} `json:"usage"`
		}
		unmarshalLiveResponse(t, body, &response)
		summary := liveResponseSummary{protocol: protocol, model: response.Model, inputTokens: response.Usage.Input, outputTokens: response.Usage.Output, totalTokens: response.Usage.Total}
		if len(response.Choices) > 0 {
			summary.text = response.Choices[0].Message.Content
			summary.terminal = response.Choices[0].FinishReason
			for _, toolCall := range response.Choices[0].Message.ToolCalls {
				summary.toolCalls++
				summary.toolID = toolCall.ID
				call := toolCall.Function
				summary.toolName = call.Name
				summary.toolArguments = decodeLiveArguments(t, []byte(call.Arguments))
			}
		}
		return summary
	case ProtocolResponses:
		var response struct {
			Model  string `json:"model"`
			Status string `json:"status"`
			Output []struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
			Usage struct {
				Input  int64 `json:"input_tokens"`
				Output int64 `json:"output_tokens"`
				Total  int64 `json:"total_tokens"`
			} `json:"usage"`
		}
		unmarshalLiveResponse(t, body, &response)
		summary := liveResponseSummary{protocol: protocol, model: response.Model, terminal: response.Status, inputTokens: response.Usage.Input, outputTokens: response.Usage.Output, totalTokens: response.Usage.Total}
		for _, item := range response.Output {
			if item.Type == "function_call" {
				summary.toolCalls++
				summary.toolID = item.CallID
				summary.toolName = item.Name
				summary.toolArguments = decodeLiveArguments(t, []byte(item.Arguments))
			}
			for _, part := range item.Content {
				if part.Type == "output_text" {
					summary.text += part.Text
				}
			}
		}
		return summary
	case ProtocolMessages:
		var response struct {
			Model      string `json:"model"`
			StopReason string `json:"stop_reason"`
			Content    []struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
			Usage struct {
				Input  int64 `json:"input_tokens"`
				Output int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		unmarshalLiveResponse(t, body, &response)
		summary := liveResponseSummary{protocol: protocol, model: response.Model, terminal: response.StopReason, inputTokens: response.Usage.Input, outputTokens: response.Usage.Output, totalTokens: response.Usage.Input + response.Usage.Output}
		for _, block := range response.Content {
			if block.Type == "text" {
				summary.text += block.Text
			}
			if block.Type == "tool_use" {
				summary.toolCalls++
				summary.toolID = block.ID
				summary.toolName = block.Name
				summary.toolArguments = decodeLiveArguments(t, block.Input)
			}
		}
		return summary
	case ProtocolGenerateContent:
		var response struct {
			Model      string `json:"modelVersion"`
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text         string `json:"text"`
						FunctionCall *struct {
							ID   string          `json:"id"`
							Name string          `json:"name"`
							Args json.RawMessage `json:"args"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			Usage struct {
				Input  int64 `json:"promptTokenCount"`
				Output int64 `json:"candidatesTokenCount"`
				Total  int64 `json:"totalTokenCount"`
			} `json:"usageMetadata"`
		}
		unmarshalLiveResponse(t, body, &response)
		summary := liveResponseSummary{protocol: protocol, model: response.Model, inputTokens: response.Usage.Input, outputTokens: response.Usage.Output, totalTokens: response.Usage.Total}
		for _, candidate := range response.Candidates {
			if summary.terminal == "" {
				summary.terminal = candidate.FinishReason
			}
			for _, part := range candidate.Content.Parts {
				summary.text += part.Text
				if part.FunctionCall != nil {
					summary.toolCalls++
					summary.toolID = part.FunctionCall.ID
					summary.toolName = part.FunctionCall.Name
					summary.toolArguments = decodeLiveArguments(t, part.FunctionCall.Args)
				}
			}
		}
		return summary
	default:
		t.Fatalf("unsupported protocol %q", protocol)
		return liveResponseSummary{}
	}
}

func unmarshalLiveResponse(t *testing.T, body []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatalf("decode response: %v; %s", err, liveBodyFingerprint(body))
	}
}

func decodeLiveArguments(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err != nil {
		t.Fatalf("tool arguments are not a JSON object: %v; %s", err, liveBodyFingerprint(raw))
	}
	return arguments
}

func assertLiveResponseSummary(t *testing.T, summary liveResponseSummary, toolCall bool) {
	t.Helper()
	if summary.model != liveClientModel {
		t.Fatalf("response model = %q, want client model alias %q", summary.model, liveClientModel)
	}
	if summary.inputTokens <= 0 || summary.outputTokens <= 0 || summary.totalTokens != summary.inputTokens+summary.outputTokens {
		t.Fatalf("invalid usage: input=%d output=%d total=%d", summary.inputTokens, summary.outputTokens, summary.totalTokens)
	}
	wantTerminal := map[Protocol]string{
		ProtocolChat:            "stop",
		ProtocolResponses:       "completed",
		ProtocolMessages:        "end_turn",
		ProtocolGenerateContent: "STOP",
	}[summary.protocol]
	if toolCall {
		wantTerminal = map[Protocol]string{
			ProtocolChat:            "tool_calls",
			ProtocolResponses:       "completed",
			ProtocolMessages:        "tool_use",
			ProtocolGenerateContent: "STOP",
		}[summary.protocol]
	}
	if summary.terminal != wantTerminal {
		t.Fatalf("terminal state = %q, want %q", summary.terminal, wantTerminal)
	}
	if toolCall {
		missingRequiredID := summary.protocol != ProtocolGenerateContent && summary.toolID == ""
		if summary.toolCalls != 1 || summary.toolArguments == nil || strings.TrimSpace(summary.text) != "" || missingRequiredID {
			t.Fatalf("tool result calls=%d id_present=%v arguments_present=%v text_present=%v", summary.toolCalls, summary.toolID != "", summary.toolArguments != nil, strings.TrimSpace(summary.text) != "")
		}
	} else if summary.toolCalls != 0 {
		t.Fatalf("unexpected tool calls = %d", summary.toolCalls)
	}
}

func liveBodyFingerprint(body []byte) string {
	digest := sha256.Sum256(body)
	return fmt.Sprintf("bytes=%d sha256=%x", len(body), digest[:8])
}
