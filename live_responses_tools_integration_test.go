//go:build integration

package routemorph

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

const liveToolsMaxResponseBytes = 32 << 20

type liveToolsUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type liveToolsAnnotation struct {
	Type        string `json:"type"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	StartIndex  *int   `json:"start_index"`
	EndIndex    *int   `json:"end_index"`
	URLCitation *struct {
		URL        string `json:"url"`
		Title      string `json:"title"`
		StartIndex *int   `json:"start_index"`
		EndIndex   *int   `json:"end_index"`
	} `json:"url_citation"`
}

type liveToolsOutputItem struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	CallID    string            `json:"call_id"`
	Name      string            `json:"name"`
	Input     string            `json:"input"`
	Status    string            `json:"status"`
	Execution string            `json:"execution"`
	CreatedBy string            `json:"created_by"`
	Role      string            `json:"role"`
	Arguments json.RawMessage   `json:"arguments"`
	Tools     []json.RawMessage `json:"tools"`
	Action    *struct {
		Type    string   `json:"type"`
		Query   string   `json:"query"`
		Queries []string `json:"queries"`
	} `json:"action"`
	Content []struct {
		Type        string                `json:"type"`
		Text        string                `json:"text"`
		Annotations []liveToolsAnnotation `json:"annotations"`
	} `json:"content"`
	Raw json.RawMessage `json:"-"`
}

func (item *liveToolsOutputItem) UnmarshalJSON(data []byte) error {
	type wireItem liveToolsOutputItem
	var decoded wireItem
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*item = liveToolsOutputItem(decoded)
	item.Raw = append(item.Raw[:0], data...)
	return nil
}

type liveToolsResponsesEnvelope struct {
	ID     string                `json:"id"`
	Model  string                `json:"model"`
	Status string                `json:"status"`
	Output []liveToolsOutputItem `json:"output"`
	Usage  liveToolsUsage        `json:"usage"`
}

type liveToolsStreamEvent struct {
	Type           string                     `json:"type"`
	SequenceNumber *int64                     `json:"sequence_number"`
	OutputIndex    *int                       `json:"output_index"`
	ItemID         string                     `json:"item_id"`
	Delta          string                     `json:"delta"`
	Text           string                     `json:"text"`
	Arguments      json.RawMessage            `json:"arguments"`
	Input          string                     `json:"input"`
	Item           liveToolsOutputItem        `json:"item"`
	Response       liveToolsResponsesEnvelope `json:"response"`
	Annotation     *liveToolsAnnotation       `json:"annotation"`
}

type liveToolsChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				Custom struct {
					Name  string `json:"name"`
					Input string `json:"input"`
				} `json:"custom"`
			} `json:"tool_calls"`
			Annotations []liveToolsAnnotation `json:"annotations"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		InputTokens  int64 `json:"prompt_tokens"`
		OutputTokens int64 `json:"completion_tokens"`
		TotalTokens  int64 `json:"total_tokens"`
	} `json:"usage"`
}

func TestLiveResponsesProviderCustomToolIntegration(t *testing.T) {
	config := requireLiveResponsesConfig(t)
	adapter := newLiveResponsesAdapter(t, config)

	t.Run("native/non_stream", func(t *testing.T) {
		marker := "RM_CUSTOM_NATIVE_NONSTREAM"
		request := liveRequestFromFixture(t, config, "tools/custom_tool_call/responses_nonstream", ProtocolResponses, false, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, request, false)
		response := decodeLiveToolsResponses(t, raw)
		assertLiveToolsResponsesBase(t, response)
		assertLiveCustomToolItem(t, oneLiveToolsItem(t, response.Output, "custom_tool_call"), marker, true)
	})

	t.Run("native/stream", func(t *testing.T) {
		marker := "RM_CUSTOM_NATIVE_STREAM"
		request := liveRequestFromFixture(t, config, "tools/custom_tool_call/responses_stream", ProtocolResponses, true, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, request, true)
		events := decodeLiveToolsResponsesStream(t, raw)
		assertLiveCustomToolStream(t, events, marker)
	})

	t.Run("chat_to_responses/non_stream", func(t *testing.T) {
		marker := "RM_CUSTOM_CHAT_NONSTREAM"
		request := liveRequestFromFixture(t, config, "tools/custom_tool_call/chat_nonstream", ProtocolChat, false, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolChat, request, false)
		var response liveToolsChatResponse
		unmarshalLiveResponse(t, raw, &response)
		assertLiveToolsChatBase(t, response)
		if len(response.Choices) != 1 {
			t.Fatalf("Chat choices=%d, want 1", len(response.Choices))
		}
		choice := response.Choices[0]
		if choice.FinishReason != "tool_calls" || strings.TrimSpace(choice.Message.Content) != "" {
			t.Fatalf("Chat custom terminal=%q text_present=%v", choice.FinishReason, strings.TrimSpace(choice.Message.Content) != "")
		}
		if len(choice.Message.ToolCalls) != 1 {
			t.Fatalf("Chat custom calls=%d, want 1", len(choice.Message.ToolCalls))
		}
		call := choice.Message.ToolCalls[0]
		if call.ID == "" || call.Type != "custom" || call.Custom.Name != "record_marker_text" || strings.TrimSpace(call.Custom.Input) != marker {
			t.Fatalf("Chat custom call id_present=%v type=%q name=%q input_matches=%v", call.ID != "", call.Type, call.Custom.Name, strings.TrimSpace(call.Custom.Input) == marker)
		}
	})

	t.Run("output_continuation/native/non_stream", func(t *testing.T) {
		marker := "RM_CUSTOM_OUTPUT_RESPONSES_NONSTREAM"
		request := liveRequestFromFixture(t, config, "tools/custom_tool_call_output/responses_nonstream", ProtocolResponses, false, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, request, false)
		assertLiveCustomOutputResponse(t, decodeLiveToolsResponses(t, raw), marker)
	})

	t.Run("output_continuation/native/stream", func(t *testing.T) {
		marker := "RM_CUSTOM_OUTPUT_RESPONSES_STREAM"
		request := liveRequestFromFixture(t, config, "tools/custom_tool_call_output/responses_stream", ProtocolResponses, true, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, request, true)
		assertLiveCustomOutputStream(t, decodeLiveToolsResponsesStream(t, raw), marker)
	})

	t.Run("output_continuation/chat_to_responses/non_stream", func(t *testing.T) {
		marker := "RM_CUSTOM_OUTPUT_CHAT_NONSTREAM"
		request := liveRequestFromFixture(t, config, "tools/custom_tool_call_output/chat_nonstream", ProtocolChat, false, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolChat, request, false)
		var response liveToolsChatResponse
		unmarshalLiveResponse(t, raw, &response)
		assertLiveCustomOutputChatResponse(t, response, marker)
	})
}

func TestLiveResponsesProviderWebSearchIntegration(t *testing.T) {
	config := requireLiveResponsesConfig(t)
	adapter := newLiveResponsesAdapter(t, config)

	t.Run("native/non_stream", func(t *testing.T) {
		request := liveRequestFromFixture(t, config, "tools/web_search/responses_nonstream", ProtocolResponses, false, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, request, false)
		assertLiveWebSearchResponse(t, decodeLiveToolsResponses(t, raw))
	})

	t.Run("native/stream", func(t *testing.T) {
		request := liveRequestFromFixture(t, config, "tools/web_search/responses_stream", ProtocolResponses, true, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, request, true)
		assertLiveWebSearchStream(t, decodeLiveToolsResponsesStream(t, raw))
	})

	t.Run("chat_to_responses/non_stream", func(t *testing.T) {
		request := liveRequestFromFixture(t, config, "tools/web_search/chat_nonstream", ProtocolChat, false, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolChat, request, false)
		var response liveToolsChatResponse
		unmarshalLiveResponse(t, raw, &response)
		assertLiveToolsChatBase(t, response)
		if len(response.Choices) != 1 {
			t.Fatalf("Chat choices=%d, want 1", len(response.Choices))
		}
		choice := response.Choices[0]
		if choice.FinishReason != "stop" || strings.TrimSpace(choice.Message.Content) == "" || len(choice.Message.ToolCalls) != 0 {
			t.Fatalf("Chat web terminal=%q text_present=%v tool_calls=%d", choice.FinishReason, strings.TrimSpace(choice.Message.Content) != "", len(choice.Message.ToolCalls))
		}
		assertLiveChatURLCitation(t, choice.Message.Annotations)
	})
}

func TestLiveResponsesProviderToolSearchIntegration(t *testing.T) {
	config := requireLiveResponsesConfig(t)
	adapter := newLiveResponsesAdapter(t, config)

	t.Run("server/non_stream", func(t *testing.T) {
		marker := "RM_TOOL_SEARCH_SERVER_NONSTREAM"
		request := liveRequestFromFixture(t, config, "tools/tool_search/server_responses_nonstream", ProtocolResponses, false, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, request, false)
		assertLiveServerToolSearchResponse(t, decodeLiveToolsResponses(t, raw), marker, false)
	})

	t.Run("server/stream", func(t *testing.T) {
		marker := "RM_TOOL_SEARCH_SERVER_STREAM"
		request := liveRequestFromFixture(t, config, "tools/tool_search/server_responses_stream", ProtocolResponses, true, nil)
		raw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, request, true)
		assertLiveServerToolSearchStream(t, decodeLiveToolsResponsesStream(t, raw), marker)
	})

	t.Run("client/two_turn_continuation", func(t *testing.T) {
		marker := "RM_TOOL_SEARCH_CLIENT_CONTINUATION"
		firstRequest := liveRequestFromFixture(t, config, "tools/tool_search/client_responses_turn1", ProtocolResponses, false, nil)
		firstRaw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, firstRequest, false)
		first := decodeLiveToolsResponses(t, firstRaw)
		assertLiveToolsResponsesBase(t, first)
		call := oneLiveToolsItem(t, first.Output, "tool_search_call")
		assertLiveToolSearchCall(t, call, "client", true)
		if liveToolsItemCount(first.Output, "tool_search_output") != 0 {
			t.Fatal("client-executed first turn unexpectedly contained tool_search_output")
		}

		secondRequest := liveRequestFromFixture(t, config, "tools/tool_search/client_responses_turn2", ProtocolResponses, false, map[string]any{
			"CLIENT_TOOL_SEARCH_CALL":    json.RawMessage(call.Raw),
			"CLIENT_TOOL_SEARCH_CALL_ID": call.CallID,
		})
		secondRaw := invokeLiveToolsRequest(t, adapter, ProtocolResponses, secondRequest, false)
		second := decodeLiveToolsResponses(t, secondRaw)
		assertLiveToolsResponsesBase(t, second)
		functionCall := oneLiveToolsItem(t, second.Output, "function_call")
		assertLiveRecordMarkerFunctionCall(t, functionCall, marker)
	})
}

func invokeLiveToolsRequest(t *testing.T, adapter *Adapter, protocol Protocol, request *Request, streaming bool) []byte {
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
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Fatalf("close %s response failed (%T)", protocol, err)
		}
	}()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s status=%d %s", protocol, response.StatusCode, liveProviderErrorSummary(response.Body))
	}
	wantMode := RouteModeIncremental
	if protocol == ProtocolResponses {
		wantMode = RouteModeNative
	}
	if response.Meta.IngressProtocol != protocol || response.Meta.UpstreamProtocol != ProtocolResponses || response.Meta.Stream != streaming || response.Meta.RouteMode != wantMode {
		t.Fatalf("unexpected response metadata: ingress=%q upstream=%q stream=%v mode=%q", response.Meta.IngressProtocol, response.Meta.UpstreamProtocol, response.Meta.Stream, response.Meta.RouteMode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, liveToolsMaxResponseBytes+1))
	if err != nil {
		t.Fatalf("read %s response failed after %s (%T): %v", protocol, liveBodyFingerprint(body), err, err)
	}
	if len(body) > liveToolsMaxResponseBytes {
		t.Fatalf("%s response exceeded byte limit: %s", protocol, liveBodyFingerprint(body))
	}
	validateLiveDiagnostics(t, response.Meta.Diagnostics())
	if !streaming {
		if err := codec.New(core.Protocol(protocol)).ValidateResponse(context.Background(), body); err != nil {
			t.Fatalf("invalid converted %s response (%T): %s", protocol, err, liveBodyFingerprint(body))
		}
	}
	return body
}

func decodeLiveToolsResponses(t *testing.T, body []byte) liveToolsResponsesEnvelope {
	t.Helper()
	var response liveToolsResponsesEnvelope
	unmarshalLiveResponse(t, body, &response)
	return response
}

func decodeLiveToolsResponsesStream(t *testing.T, body []byte) []liveToolsStreamEvent {
	t.Helper()
	decoder, err := codec.New(core.ProtocolResponses).NewStreamDecoder(bytes.NewReader(body), core.StreamOptions{MaxFrameBytes: liveToolsMaxResponseBytes})
	if err != nil {
		t.Fatalf("create Responses stream decoder failed (%T)", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	events := make([]liveToolsStreamEvent, 0, 32)
	var previous *int64
	for {
		frame, err := decoder.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode Responses stream failed after %d events (%T)", len(events), err)
		}
		if frame.Done {
			t.Fatal("Responses stream unexpectedly contained a [DONE] frame")
		}
		var event liveToolsStreamEvent
		unmarshalLiveResponse(t, frame.Data, &event)
		if event.Type == "" || event.SequenceNumber == nil {
			t.Fatalf("Responses event %d omitted type or sequence_number", len(events))
		}
		if frame.Event != "" && frame.Event != event.Type {
			t.Fatalf("Responses event name mismatch at index=%d", len(events))
		}
		if previous != nil && *event.SequenceNumber != *previous+1 {
			t.Fatalf("Responses sequence_number=%d after %d", *event.SequenceNumber, *previous)
		}
		sequence := *event.SequenceNumber
		previous = &sequence
		events = append(events, event)
	}
	if len(events) == 0 || events[0].Type != "response.created" || events[len(events)-1].Type != "response.completed" {
		first, last := "", ""
		if len(events) > 0 {
			first, last = events[0].Type, events[len(events)-1].Type
		}
		t.Fatalf("incomplete Responses stream lifecycle: events=%d first=%q last=%q", len(events), first, last)
	}
	if *events[0].SequenceNumber != 0 || len(events) < 3 || events[1].Type != "response.in_progress" {
		second := ""
		if len(events) > 1 {
			second = events[1].Type
		}
		t.Fatalf("Responses stream startup lifecycle sequence=%d second=%q", *events[0].SequenceNumber, second)
	}
	if events[0].Response.ID == "" || events[0].Response.Model != liveClientModel || events[0].Response.Status != "in_progress" {
		t.Fatalf("Responses created envelope id_present=%v model=%q status=%q", events[0].Response.ID != "", events[0].Response.Model, events[0].Response.Status)
	}
	return events
}

func assertLiveToolsResponsesBase(t *testing.T, response liveToolsResponsesEnvelope) {
	t.Helper()
	if response.ID == "" || response.Model != liveClientModel || response.Status != "completed" {
		t.Fatalf("Responses envelope id_present=%v model=%q status=%q", response.ID != "", response.Model, response.Status)
	}
	assertLiveToolsUsage(t, response.Usage.InputTokens, response.Usage.OutputTokens, response.Usage.TotalTokens)
}

func assertLiveToolsChatBase(t *testing.T, response liveToolsChatResponse) {
	t.Helper()
	if response.Model != liveClientModel {
		t.Fatalf("Chat model=%q, want client alias %q", response.Model, liveClientModel)
	}
	assertLiveToolsUsage(t, response.Usage.InputTokens, response.Usage.OutputTokens, response.Usage.TotalTokens)
}

func assertLiveToolsUsage(t *testing.T, input, output, total int64) {
	t.Helper()
	if input <= 0 || output <= 0 || total != input+output {
		t.Fatalf("invalid usage: input=%d output=%d total=%d", input, output, total)
	}
}

func oneLiveToolsItem(t *testing.T, items []liveToolsOutputItem, itemType string) liveToolsOutputItem {
	t.Helper()
	var found []liveToolsOutputItem
	for _, item := range items {
		if item.Type == itemType {
			found = append(found, item)
		}
	}
	if len(found) != 1 {
		t.Fatalf("Responses output type=%q count=%d, want 1", itemType, len(found))
	}
	return found[0]
}

func liveToolsItemCount(items []liveToolsOutputItem, itemType string) int {
	count := 0
	for _, item := range items {
		if item.Type == itemType {
			count++
		}
	}
	return count
}

func assertLiveCustomToolItem(t *testing.T, item liveToolsOutputItem, marker string, requireStatus bool) {
	t.Helper()
	statusOK := item.Status == "" || item.Status == "completed"
	if requireStatus {
		statusOK = item.Status == "completed"
	}
	if item.ID == "" || item.CallID == "" || item.Name != "record_marker_text" || strings.TrimSpace(item.Input) != marker || !statusOK {
		t.Fatalf("custom item id_present=%v call_id_present=%v name=%q status=%q input_matches=%v", item.ID != "", item.CallID != "", item.Name, item.Status, strings.TrimSpace(item.Input) == marker)
	}
}

func assertLiveCustomToolStream(t *testing.T, events []liveToolsStreamEvent, marker string) {
	t.Helper()
	positions := map[string]int{}
	deltas := strings.Builder{}
	var callID string
	for index, event := range events {
		switch event.Type {
		case "response.output_item.added":
			if event.Item.Type == "custom_tool_call" {
				positions["added"] = index
				if event.Item.ID == "" || event.Item.CallID == "" || event.Item.Name != "record_marker_text" || event.Item.Status != "in_progress" {
					t.Fatalf("custom added item id_present=%v call_id_present=%v name=%q status=%q", event.Item.ID != "", event.Item.CallID != "", event.Item.Name, event.Item.Status)
				}
				callID = event.Item.CallID
			}
		case "response.custom_tool_call_input.delta":
			if event.ItemID == "" || event.OutputIndex == nil || event.Delta == "" {
				t.Fatalf("custom delta omitted correlation fields at event=%d", index)
			}
			if _, exists := positions["delta"]; !exists {
				positions["delta"] = index
			}
			deltas.WriteString(event.Delta)
		case "response.custom_tool_call_input.done":
			positions["input_done"] = index
			if event.ItemID == "" || event.OutputIndex == nil || strings.TrimSpace(event.Input) != marker {
				t.Fatalf("custom input.done correlation_present=%v input_matches=%v", event.ItemID != "" && event.OutputIndex != nil, strings.TrimSpace(event.Input) == marker)
			}
		case "response.output_item.done":
			if event.Item.Type == "custom_tool_call" {
				positions["item_done"] = index
				assertLiveCustomToolItem(t, event.Item, marker, true)
				if event.Item.CallID != callID {
					t.Fatal("custom stream call_id changed between added and done")
				}
			}
		}
	}
	for _, name := range []string{"added", "delta", "input_done", "item_done"} {
		if _, ok := positions[name]; !ok {
			t.Fatalf("custom stream omitted %s lifecycle event", name)
		}
	}
	if !(positions["added"] < positions["delta"] && positions["delta"] <= positions["input_done"] && positions["input_done"] < positions["item_done"]) {
		t.Fatalf("custom stream lifecycle order invalid: added=%d delta=%d input_done=%d item_done=%d", positions["added"], positions["delta"], positions["input_done"], positions["item_done"])
	}
	if strings.TrimSpace(deltas.String()) != marker {
		t.Fatalf("custom stream delta reconstruction mismatch: %s", liveBodyFingerprint([]byte(deltas.String())))
	}
	terminal := events[len(events)-1].Response
	assertLiveToolsResponsesBase(t, terminal)
	assertLiveCustomToolItem(t, oneLiveToolsItem(t, terminal.Output, "custom_tool_call"), marker, true)
}

func assertLiveCustomOutputResponse(t *testing.T, response liveToolsResponsesEnvelope, marker string) {
	t.Helper()
	assertLiveToolsResponsesBase(t, response)
	message := oneLiveToolsItem(t, response.Output, "message")
	if message.ID == "" || message.Role != "assistant" || message.Status != "completed" {
		t.Fatalf("custom output message id_present=%v role=%q status=%q", message.ID != "", message.Role, message.Status)
	}
	var text strings.Builder
	for _, part := range message.Content {
		if part.Type == "output_text" {
			text.WriteString(part.Text)
		}
	}
	if strings.TrimSpace(text.String()) != marker {
		t.Fatalf("custom output continuation text mismatch: %s", liveBodyFingerprint([]byte(text.String())))
	}
	for _, itemType := range []string{"custom_tool_call", "function_call"} {
		if count := liveToolsItemCount(response.Output, itemType); count != 0 {
			t.Fatalf("custom output continuation emitted %s count=%d", itemType, count)
		}
	}
}

func assertLiveCustomOutputChatResponse(t *testing.T, response liveToolsChatResponse, marker string) {
	t.Helper()
	assertLiveToolsChatBase(t, response)
	if len(response.Choices) != 1 {
		t.Fatalf("Chat choices=%d, want 1", len(response.Choices))
	}
	choice := response.Choices[0]
	if choice.FinishReason != "stop" || strings.TrimSpace(choice.Message.Content) != marker || len(choice.Message.ToolCalls) != 0 {
		t.Fatalf("Chat custom output terminal=%q text_matches=%v tool_calls=%d", choice.FinishReason, strings.TrimSpace(choice.Message.Content) == marker, len(choice.Message.ToolCalls))
	}
}

func assertLiveCustomOutputStream(t *testing.T, events []liveToolsStreamEvent, marker string) {
	t.Helper()
	positions := map[string]int{}
	var messageID string
	var messageOutputIndex int
	var hasMessageOutputIndex bool
	var deltas strings.Builder
	for index, event := range events {
		switch event.Type {
		case "response.output_item.added":
			if event.Item.Type != "message" {
				continue
			}
			positions["message_added"] = index
			messageID = event.Item.ID
			if event.OutputIndex != nil {
				messageOutputIndex = *event.OutputIndex
				hasMessageOutputIndex = true
			}
			if messageID == "" || event.Item.Status != "in_progress" || !hasMessageOutputIndex {
				t.Fatalf("custom output message added id_present=%v status=%q output_index_present=%v", messageID != "", event.Item.Status, hasMessageOutputIndex)
			}
		case "response.output_text.delta":
			if event.Delta == "" {
				continue
			}
			if event.ItemID != messageID || event.OutputIndex == nil || !hasMessageOutputIndex || *event.OutputIndex != messageOutputIndex {
				t.Fatalf("custom output delta correlation_matches=%v", event.ItemID == messageID && event.OutputIndex != nil && hasMessageOutputIndex && *event.OutputIndex == messageOutputIndex)
			}
			if _, exists := positions["text_delta"]; !exists {
				positions["text_delta"] = index
			}
			deltas.WriteString(event.Delta)
		case "response.output_text.done":
			positions["text_done"] = index
			if event.ItemID != messageID || event.OutputIndex == nil || !hasMessageOutputIndex || *event.OutputIndex != messageOutputIndex || strings.TrimSpace(event.Text) != marker {
				t.Fatalf("custom output text.done correlation_matches=%v text_matches=%v", event.ItemID == messageID && event.OutputIndex != nil && hasMessageOutputIndex && *event.OutputIndex == messageOutputIndex, strings.TrimSpace(event.Text) == marker)
			}
		case "response.output_item.done":
			if event.Item.Type == "message" {
				positions["message_done"] = index
				if event.Item.ID != messageID || event.Item.Status != "completed" {
					t.Fatalf("custom output message done id_matches=%v status=%q", event.Item.ID == messageID, event.Item.Status)
				}
			}
		}
	}
	for _, name := range []string{"message_added", "text_delta", "text_done", "message_done"} {
		if _, exists := positions[name]; !exists {
			t.Fatalf("custom output stream omitted %s lifecycle event", name)
		}
	}
	if !(positions["message_added"] < positions["text_delta"] && positions["text_delta"] <= positions["text_done"] && positions["text_done"] < positions["message_done"]) {
		t.Fatalf("custom output stream lifecycle order invalid: %#v", positions)
	}
	if strings.TrimSpace(deltas.String()) != marker {
		t.Fatalf("custom output stream delta reconstruction mismatch: %s", liveBodyFingerprint([]byte(deltas.String())))
	}
	assertLiveCustomOutputResponse(t, events[len(events)-1].Response, marker)
}

func assertLiveWebSearchResponse(t *testing.T, response liveToolsResponsesEnvelope) {
	t.Helper()
	assertLiveToolsResponsesBase(t, response)
	webCount, messageCount := 0, 0
	lastWeb, firstMessage := -1, -1
	sawCitation := false
	for index, item := range response.Output {
		switch item.Type {
		case "web_search_call":
			webCount++
			lastWeb = index
			queryPresent := item.Action != nil && (strings.TrimSpace(item.Action.Query) != "" || len(item.Action.Queries) > 0)
			if item.ID == "" || item.Status != "completed" || item.Action == nil || item.Action.Type != "search" || !queryPresent {
				t.Fatalf("web item id_present=%v status=%q action_present=%v action_type=%q query_present=%v", item.ID != "", item.Status, item.Action != nil, liveToolsActionType(item.Action), queryPresent)
			}
		case "message":
			messageCount++
			if firstMessage < 0 {
				firstMessage = index
			}
			if item.ID == "" || item.Role != "assistant" || item.Status != "completed" {
				t.Fatalf("web message id_present=%v role=%q status=%q", item.ID != "", item.Role, item.Status)
			}
			for _, part := range item.Content {
				if part.Type == "output_text" && strings.TrimSpace(part.Text) != "" {
					for _, annotation := range part.Annotations {
						sawCitation = sawCitation || validLiveURLCitation(annotation)
					}
				}
			}
		}
	}
	if webCount == 0 || messageCount == 0 || lastWeb >= firstMessage || !sawCitation {
		t.Fatalf("web output web_calls=%d messages=%d ordered=%v citation=%v", webCount, messageCount, lastWeb >= 0 && firstMessage >= 0 && lastWeb < firstMessage, sawCitation)
	}
}

func assertLiveWebSearchStream(t *testing.T, events []liveToolsStreamEvent) {
	t.Helper()
	positions := map[string]int{}
	var webID string
	for index, event := range events {
		switch event.Type {
		case "response.output_item.added":
			if event.Item.Type == "web_search_call" {
				positions["web_added"] = index
				webID = event.Item.ID
				if webID == "" || event.Item.Status != "in_progress" || event.OutputIndex == nil {
					t.Fatalf("web added id_present=%v status=%q output_index_present=%v", webID != "", event.Item.Status, event.OutputIndex != nil)
				}
			}
			if event.Item.Type == "message" {
				positions["message_added"] = index
			}
		case "response.web_search_call.in_progress":
			positions["web_in_progress"] = index
			assertLiveWebStreamCorrelation(t, event, webID)
		case "response.web_search_call.searching":
			positions["web_searching"] = index
			assertLiveWebStreamCorrelation(t, event, webID)
		case "response.web_search_call.completed":
			positions["web_completed"] = index
			assertLiveWebStreamCorrelation(t, event, webID)
		case "response.output_text.delta":
			if event.Delta != "" {
				if _, ok := positions["text_delta"]; !ok {
					positions["text_delta"] = index
				}
			}
		case "response.output_text.annotation.added":
			if event.Annotation != nil && validLiveURLCitation(*event.Annotation) {
				positions["annotation"] = index
			}
		case "response.output_item.done":
			switch event.Item.Type {
			case "web_search_call":
				positions["web_done"] = index
				if event.Item.ID != webID || event.Item.Status != "completed" || event.Item.Action == nil || event.Item.Action.Type != "search" {
					t.Fatalf("web done correlation_matches=%v status=%q action_type=%q", event.Item.ID == webID, event.Item.Status, liveToolsActionType(event.Item.Action))
				}
			case "message":
				positions["message_done"] = index
			}
		}
	}
	for _, name := range []string{"web_added", "web_in_progress", "web_searching", "web_completed", "web_done", "message_added", "text_delta", "annotation", "message_done"} {
		if _, ok := positions[name]; !ok {
			t.Fatalf("web stream omitted %s lifecycle event", name)
		}
	}
	if !(positions["web_added"] < positions["web_in_progress"] && positions["web_in_progress"] < positions["web_searching"] && positions["web_searching"] < positions["web_completed"] && positions["web_completed"] < positions["web_done"] && positions["web_done"] < positions["message_added"] && positions["message_added"] < positions["text_delta"] && positions["text_delta"] <= positions["message_done"]) {
		t.Fatalf("web stream lifecycle order invalid: web_added=%d in_progress=%d searching=%d completed=%d web_done=%d message_added=%d text_delta=%d message_done=%d", positions["web_added"], positions["web_in_progress"], positions["web_searching"], positions["web_completed"], positions["web_done"], positions["message_added"], positions["text_delta"], positions["message_done"])
	}
	assertLiveWebSearchResponse(t, events[len(events)-1].Response)
}

func assertLiveWebStreamCorrelation(t *testing.T, event liveToolsStreamEvent, webID string) {
	t.Helper()
	if webID == "" || event.ItemID != webID || event.OutputIndex == nil {
		t.Fatalf("web progress correlation item_matches=%v output_index_present=%v", webID != "" && event.ItemID == webID, event.OutputIndex != nil)
	}
}

func assertLiveChatURLCitation(t *testing.T, annotations []liveToolsAnnotation) {
	t.Helper()
	for _, annotation := range annotations {
		if validLiveURLCitation(annotation) {
			return
		}
	}
	t.Fatal("Chat web response omitted a complete URL citation")
}

func validLiveURLCitation(annotation liveToolsAnnotation) bool {
	if annotation.Type != "url_citation" {
		return false
	}
	urlValue, title := annotation.URL, annotation.Title
	start, end := annotation.StartIndex, annotation.EndIndex
	if annotation.URLCitation != nil {
		urlValue, title = annotation.URLCitation.URL, annotation.URLCitation.Title
		start, end = annotation.URLCitation.StartIndex, annotation.URLCitation.EndIndex
	}
	return (strings.HasPrefix(urlValue, "http://") || strings.HasPrefix(urlValue, "https://")) && strings.TrimSpace(title) != "" && start != nil && end != nil && *end >= *start
}

func liveToolsActionType(action *struct {
	Type    string   `json:"type"`
	Query   string   `json:"query"`
	Queries []string `json:"queries"`
}) string {
	if action == nil {
		return ""
	}
	return action.Type
}

func assertLiveServerToolSearchResponse(t *testing.T, response liveToolsResponsesEnvelope, marker string, requireCorrelation bool) {
	t.Helper()
	assertLiveToolsResponsesBase(t, response)
	call := oneLiveToolsItem(t, response.Output, "tool_search_call")
	output := oneLiveToolsItem(t, response.Output, "tool_search_output")
	assertLiveToolSearchCall(t, call, "server", requireCorrelation)
	assertLiveToolSearchOutput(t, output, "server", call.CallID, requireCorrelation)
	callIndex := liveToolsFirstItemIndex(response.Output, "tool_search_call")
	outputIndex := liveToolsFirstItemIndex(response.Output, "tool_search_output")
	functionIndex := liveToolsFirstItemIndex(response.Output, "function_call")
	if callIndex < 0 || outputIndex <= callIndex || functionIndex <= outputIndex {
		t.Fatalf("server tool-search output order call=%d output=%d function=%d", callIndex, outputIndex, functionIndex)
	}
	assertLiveRecordMarkerFunctionCall(t, oneLiveToolsItem(t, response.Output, "function_call"), marker)
}

func assertLiveToolSearchCall(t *testing.T, item liveToolsOutputItem, execution string, requireCorrelation bool) {
	t.Helper()
	var arguments map[string]any
	if len(bytes.TrimSpace(item.Arguments)) > 0 {
		unmarshalLiveResponse(t, item.Arguments, &arguments)
	}
	executionOK := item.Execution == "" || item.Execution == execution
	correlationOK := !requireCorrelation || item.CallID != ""
	if item.ID == "" || item.Status != "completed" || arguments == nil || !executionOK || !correlationOK {
		t.Fatalf("tool_search_call id_present=%v call_id_present=%v execution=%q status=%q arguments_present=%v", item.ID != "", item.CallID != "", item.Execution, item.Status, arguments != nil)
	}
}

func assertLiveToolSearchOutput(t *testing.T, item liveToolsOutputItem, execution, callID string, requireCorrelation bool) {
	t.Helper()
	executionOK := item.Execution == "" || item.Execution == execution
	correlationOK := !requireCorrelation || item.CallID != "" && item.CallID == callID
	if !requireCorrelation && item.CallID != "" && callID != "" {
		correlationOK = item.CallID == callID
	}
	if item.ID == "" || item.Status != "completed" || item.Tools == nil || !executionOK || !correlationOK {
		t.Fatalf("tool_search_output id_present=%v call_id_present=%v execution=%q status=%q tools_present=%v correlation_matches=%v", item.ID != "", item.CallID != "", item.Execution, item.Status, item.Tools != nil, !requireCorrelation || item.CallID == callID)
	}
	found := false
	for _, raw := range item.Tools {
		var tool struct {
			Type         string `json:"type"`
			Name         string `json:"name"`
			DeferLoading bool   `json:"defer_loading"`
		}
		unmarshalLiveResponse(t, raw, &tool)
		found = found || tool.Type == "function" && tool.Name == "record_marker" && tool.DeferLoading
	}
	if !found {
		t.Fatal("tool_search_output did not load deferred record_marker")
	}
}

func assertLiveRecordMarkerFunctionCall(t *testing.T, item liveToolsOutputItem, marker string) {
	t.Helper()
	arguments := decodeLiveToolsFunctionArguments(t, item.Arguments)
	statusOK := item.Status == "" || item.Status == "completed"
	if item.ID == "" || item.CallID == "" || item.Name != "record_marker" || !statusOK || arguments["marker"] != marker || len(arguments) != 1 {
		t.Fatalf("function call id_present=%v call_id_present=%v name=%q status=%q marker_matches=%v argument_fields=%d", item.ID != "", item.CallID != "", item.Name, item.Status, arguments["marker"] == marker, len(arguments))
	}
}

func decodeLiveToolsFunctionArguments(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '"' {
		var encoded string
		unmarshalLiveResponse(t, trimmed, &encoded)
		return decodeLiveArguments(t, []byte(encoded))
	}
	return decodeLiveArguments(t, trimmed)
}

func assertLiveServerToolSearchStream(t *testing.T, events []liveToolsStreamEvent, marker string) {
	t.Helper()
	positions := map[string]int{}
	var searchCallID string
	for index, event := range events {
		switch event.Type {
		case "response.output_item.added", "response.output_item.done":
			phase := "added"
			wantStatus := "in_progress"
			if event.Type == "response.output_item.done" {
				phase = "done"
				wantStatus = "completed"
			}
			switch event.Item.Type {
			case "tool_search_call":
				positions["call_"+phase] = index
				statusOK := event.Item.Status == wantStatus || phase == "added" && event.Item.Status == "completed"
				executionOK := event.Item.Execution == "" || event.Item.Execution == "server"
				if event.Item.ID == "" || !executionOK || !statusOK || event.OutputIndex == nil || len(bytes.TrimSpace(event.Item.Arguments)) == 0 {
					t.Fatalf("stream tool_search_call phase=%s id_present=%v call_id_present=%v execution=%q status=%q output_index_present=%v arguments_present=%v", phase, event.Item.ID != "", event.Item.CallID != "", event.Item.Execution, event.Item.Status, event.OutputIndex != nil, len(bytes.TrimSpace(event.Item.Arguments)) > 0)
				}
				if searchCallID == "" && event.Item.CallID != "" {
					searchCallID = event.Item.CallID
				} else if searchCallID != "" && event.Item.CallID != "" && event.Item.CallID != searchCallID {
					t.Fatal("tool_search_call call_id changed across stream lifecycle")
				}
			case "tool_search_output":
				positions["output_"+phase] = index
				statusOK := event.Item.Status == wantStatus || phase == "added" && event.Item.Status == "completed"
				executionOK := event.Item.Execution == "" || event.Item.Execution == "server"
				correlationOK := searchCallID == "" || event.Item.CallID == "" || event.Item.CallID == searchCallID
				if event.Item.ID == "" || !correlationOK || !executionOK || !statusOK || event.OutputIndex == nil || event.Item.Tools == nil {
					t.Fatalf("stream tool_search_output phase=%s id_present=%v call_id_present=%v correlation_matches=%v execution=%q status=%q output_index_present=%v tools_present=%v", phase, event.Item.ID != "", event.Item.CallID != "", event.Item.CallID == searchCallID, event.Item.Execution, event.Item.Status, event.OutputIndex != nil, event.Item.Tools != nil)
				}
			case "function_call":
				positions["function_"+phase] = index
			}
		case "response.function_call_arguments.delta":
			if event.Delta != "" {
				if _, exists := positions["function_delta"]; !exists {
					positions["function_delta"] = index
				}
			}
		case "response.function_call_arguments.done":
			if len(bytes.TrimSpace(event.Arguments)) > 0 {
				positions["function_arguments_done"] = index
			}
		}
	}
	for _, name := range []string{"call_added", "call_done", "output_added", "output_done", "function_added", "function_delta", "function_arguments_done", "function_done"} {
		if _, ok := positions[name]; !ok {
			t.Fatalf("tool-search stream omitted %s lifecycle event", name)
		}
	}
	if !(positions["call_added"] < positions["call_done"] && positions["call_done"] < positions["output_added"] && positions["output_added"] < positions["output_done"] && positions["output_done"] < positions["function_added"] && positions["function_added"] < positions["function_delta"] && positions["function_delta"] <= positions["function_arguments_done"] && positions["function_arguments_done"] < positions["function_done"]) {
		t.Fatalf("tool-search stream lifecycle order invalid: %#v", positions)
	}
	assertLiveServerToolSearchResponse(t, events[len(events)-1].Response, marker, false)
}

func liveToolsFirstItemIndex(items []liveToolsOutputItem, itemType string) int {
	for index, item := range items {
		if item.Type == itemType {
			return index
		}
	}
	return -1
}
