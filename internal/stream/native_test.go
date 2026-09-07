package stream

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestNativeCollectRenderMatrix(t *testing.T) {
	responses := map[Protocol][]byte{
		ProtocolChat:            []byte(`{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
		ProtocolResponses:       []byte(`{"id":"r","model":"m","status":"completed","output":[{"id":"i","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
		ProtocolMessages:        []byte(`{"id":"a","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`),
		ProtocolGenerateContent: []byte(`{"responseId":"g","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`),
	}
	for protocol, response := range responses {
		t.Run(string(protocol), func(t *testing.T) {
			frames, _, err := RenderNativeResponse(protocol, response)
			if err != nil || len(frames) == 0 {
				t.Fatalf("render frames=%#v error=%v", frames, err)
			}
			body, _, err := CollectNativeResponse(protocol, frames, core.RejectSemanticLoss)
			if err != nil || len(body) == 0 {
				t.Fatalf("collect body=%s error=%v", body, err)
			}
		})
	}
}

func TestNativeMessagesHostedToolBlocksRoundTrip(t *testing.T) {
	for _, blockType := range []string{"server_tool_use", "mcp_tool_use"} {
		t.Run(blockType, func(t *testing.T) {
			source := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"` + blockType + `","id":"tool_1","name":"lookup","input":{"query":"weather"}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`)
			frames, _, err := RenderNativeResponse(ProtocolMessages, source)
			if err != nil {
				t.Fatal(err)
			}
			body, _, err := CollectNativeResponse(ProtocolMessages, frames, core.RejectSemanticLoss)
			if err != nil {
				t.Fatal(err)
			}
			var response messagesResponse
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			var blocks []messagesBlock
			if err := json.Unmarshal(response.Content, &blocks); err != nil {
				t.Fatal(err)
			}
			if len(blocks) != 1 || blocks[0].Type != blockType || string(blocks[0].Input) != `{"query":"weather"}` {
				t.Fatalf("round-trip block = %#v; body=%s", blocks, body)
			}
		})
	}
}

func TestNativeBufferedCollectorsRejectResponseIdentityChanges(t *testing.T) {
	chatChanges := []struct {
		name   string
		second string
		path   string
	}{
		{name: "id", second: `{"id":"chat_b","model":"m1","created":1,"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`, path: "$.id"},
		{name: "model", second: `{"id":"chat_a","model":"m2","created":1,"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`, path: "$.model"},
		{name: "created", second: `{"id":"chat_a","model":"m1","created":2,"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`, path: "$.created"},
	}
	for _, test := range chatChanges {
		t.Run("chat "+test.name, func(t *testing.T) {
			frames := []streamFrame{
				{Data: []byte(`{"id":"chat_a","model":"m1","created":1,"choices":[{"index":0,"delta":{"content":"hel"},"finish_reason":null}]}`)},
				{Data: []byte(test.second)},
			}
			_, _, err := CollectNativeResponse(ProtocolChat, frames, core.RejectSemanticLoss)
			if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want identity failure at %s", err, test.path)
			}
		})
	}

	geminiChanges := []struct {
		name   string
		second string
		path   string
	}{
		{name: "response id", second: `{"responseId":"gemini_b","modelVersion":"m1","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}]}`, path: "$.responseId"},
		{name: "model", second: `{"responseId":"gemini_a","modelVersion":"m2","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}]}`, path: "$.modelVersion"},
		{name: "candidate index", second: `{"responseId":"gemini_a","modelVersion":"m1","candidates":[{"index":1,"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}]}`, path: ".index"},
	}
	for _, test := range geminiChanges {
		t.Run("gemini "+test.name, func(t *testing.T) {
			frames := []streamFrame{
				{Data: []byte(`{"responseId":"gemini_a","modelVersion":"m1","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hel"}]}}]}`)},
				{Data: []byte(test.second)},
			}
			_, _, err := CollectNativeResponse(ProtocolGenerateContent, frames, core.RejectSemanticLoss)
			if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want identity failure at %s", err, test.path)
			}
		})
	}
}

func TestNativeChatCollectorRejectsCorruptToolCallDeltas(t *testing.T) {
	tests := []struct {
		name   string
		frames []streamFrame
		kind   error
		path   string
	}{
		{
			name:   "custom type",
			frames: []streamFrame{{Data: []byte(`{"id":"c","model":"m","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"custom","function":{"name":"shell","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)}},
			kind:   core.ErrUnsupported, path: ".type",
		},
		{
			name:   "object arguments",
			frames: []streamFrame{{Data: []byte(`{"id":"c","model":"m","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":{"q":1}}}]},"finish_reason":"tool_calls"}]}`)}},
			kind:   core.ErrUpstreamResponse, path: ".arguments",
		},
		{
			name: "changed id",
			frames: []streamFrame{
				{Data: []byte(`{"id":"c","model":"m","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{"}}]},"finish_reason":null}]}`)},
				{Data: []byte(`{"id":"c","model":"m","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_2","type":"function","function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`)},
			},
			kind: core.ErrUpstreamResponse, path: ".id",
		},
		{
			name: "changed name",
			frames: []streamFrame{
				{Data: []byte(`{"id":"c","model":"m","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"first","arguments":"{"}}]},"finish_reason":null}]}`)},
				{Data: []byte(`{"id":"c","model":"m","created":1,"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"name":"second","arguments":"}"}}]},"finish_reason":"tool_calls"}]}`)},
			},
			kind: core.ErrUpstreamResponse, path: ".name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := CollectNativeResponse(ProtocolChat, test.frames, core.RejectSemanticLoss)
			if !errors.Is(err, test.kind) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want %v at %s", err, test.kind, test.path)
			}
		})
	}
}

func TestNativeChatCollectorPreservesLogprobsForLossChecks(t *testing.T) {
	frames := []streamFrame{{Data: []byte(`{"id":"c","model":"m","created":1,"choices":[{"index":0,"delta":{"content":"hi"},"logprobs":{"content":[{"token":"hi","logprob":-0.1,"bytes":[104,105],"top_logprobs":[]}]},"finish_reason":"stop"}]}`)}}
	body, _, err := CollectNativeResponse(ProtocolChat, frames, core.RejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || !jsonValuePresent(response.Choices[0].Logprobs) || !strings.Contains(string(response.Choices[0].Logprobs), `"token":"hi"`) {
		t.Fatalf("logprobs were lost: %s", body)
	}
}

func TestNativeChatCollectorRejectsLegacyFunctionCallDelta(t *testing.T) {
	frames := []streamFrame{{Data: []byte(`{"id":"c","model":"m","created":1,"choices":[{"index":0,"delta":{"function_call":{"name":"legacy","arguments":"{}"}},"finish_reason":"function_call"}]}`)}}
	_, _, err := CollectNativeResponse(ProtocolChat, frames, core.RejectSemanticLoss)
	if !errors.Is(err, core.ErrUnsupported) || !strings.Contains(err.Error(), ".function_call") {
		t.Fatalf("error = %v, want legacy function_call rejection", err)
	}
}

func TestNativeChatRendererHonorsIncludeUsage(t *testing.T) {
	body := []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"gpt","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	withoutUsage, _, err := renderChatResponseStream(body, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range withoutUsage {
		if !frame.Done && strings.Contains(string(frame.Data), `"usage"`) {
			t.Fatalf("include_usage=false leaked usage: %s", frame.Data)
		}
	}
	withUsage, _, err := renderChatResponseStream(body, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(withUsage) != len(withoutUsage)+1 {
		t.Fatalf("include_usage frame count = %d, want %d", len(withUsage), len(withoutUsage)+1)
	}
	for index, frame := range withUsage[:len(withUsage)-2] {
		if !strings.Contains(string(frame.Data), `"usage":null`) {
			t.Fatalf("regular frame %d missing null usage: %s", index, frame.Data)
		}
	}
	usageFrame := withUsage[len(withUsage)-2]
	if !strings.Contains(string(usageFrame.Data), `"choices":[]`) || !strings.Contains(string(usageFrame.Data), `"total_tokens":3`) {
		t.Fatalf("invalid usage-only frame: %s", usageFrame.Data)
	}
}

func TestNativeMessagesCollectorRejectsNonEmptyInitialToolInput(t *testing.T) {
	frames := []streamFrame{
		{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_1","name":"lookup","input":{"seed":1}}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"next\":2}"}}`)},
	}
	_, _, err := CollectNativeResponse(ProtocolMessages, frames, core.RejectSemanticLoss)
	if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), "empty object") {
		t.Fatalf("error = %v, want non-empty initial input rejection", err)
	}
}

func TestNativeMessagesCollectorRequiresBlockIndex(t *testing.T) {
	frames := []streamFrame{
		{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","content_block":{"type":"text","text":""}}`)},
	}
	_, _, err := CollectNativeResponse(ProtocolMessages, frames, core.RejectSemanticLoss)
	if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), "$.index") {
		t.Fatalf("error = %v, want missing index rejection", err)
	}
}

func TestNativeResponsesCollectorRejectsEventsAfterTerminal(t *testing.T) {
	frames := []streamFrame{
		{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"r","model":"m","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`)},
		{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"late"}`)},
	}
	_, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss)
	if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), "after terminal") {
		t.Fatalf("error = %v, want post-terminal rejection", err)
	}
}

func TestNativeResponsesPromptCacheOptionsRoundTrip(t *testing.T) {
	source := []byte(`{"id":"r","object":"response","model":"m","status":"completed","prompt_cache_options":{"mode":"explicit","ttl":"30m"},"output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}`)
	frames, _, err := RenderNativeResponse(ProtocolResponses, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || !strings.Contains(string(frames[0].Data), `"prompt_cache_options":{"mode":"explicit","ttl":"30m"}`) || !strings.Contains(string(frames[1].Data), `"prompt_cache_options":{"mode":"explicit","ttl":"30m"}`) {
		t.Fatalf("rendered frames = %#v", frames)
	}
	body, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	var response responsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if !jsonValuePresent(response.PromptCacheOptions) {
		t.Fatalf("collected response = %s", body)
	}
}

func TestNativeResponsesPreservesReasoningItem(t *testing.T) {
	source := []byte(`{
		"id":"r","object":"response","model":"m","status":"completed",
		"output":[
			{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"opaque","status":"completed"}
		],
		"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}
	}`)
	frames, _, err := RenderNativeResponse(ProtocolResponses, source)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"encrypted_content":"opaque"`, `"type":"reasoning"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("round-trip response missing %s: %s", want, body)
		}
	}
	if !strings.Contains(string(frames[2].Data), `"encrypted_content":"opaque"`) {
		t.Fatalf("reasoning output_item.done lost encrypted content: %s", frames[2].Data)
	}
}

func TestNativeResponsesRejectsItemsOutsideOutputUnion(t *testing.T) {
	for _, itemType := range []string{"configuration_update", "vendor_future_item"} {
		t.Run(itemType, func(t *testing.T) {
			body := []byte(`{"id":"r","model":"m","status":"completed","output":[{"id":"item_1","type":"` + itemType + `"}],"usage":{}}`)
			_, _, err := RenderNativeResponse(ProtocolResponses, body)
			if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), ".type") {
				t.Fatalf("error = %v, want output-union rejection", err)
			}
		})
	}
}

func TestNativeResponsesRequiresNonNullOutputArray(t *testing.T) {
	for _, output := range []string{"", `,"output":null`, `,"output":{}`} {
		body := []byte(`{"id":"r","model":"m","status":"completed"` + output + `,"usage":{}}`)
		if _, _, err := RenderNativeResponse(ProtocolResponses, body); !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), "$.output") {
			t.Fatalf("body=%s error=%v, want upstream output-array rejection", body, err)
		}
	}
	frames := []streamFrame{{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"r","model":"m","status":"completed","output":null,"usage":{}}}`)}}
	if _, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss); !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), "$.output") {
		t.Fatalf("collector error=%v, want upstream output-array rejection", err)
	}
}

func TestNativeResponsesBufferedMessageAndReasoningLifecycle(t *testing.T) {
	source := []byte(`{
		"id":"resp_content","object":"response","model":"gpt-5.6","status":"completed",
		"output":[
			{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"summary","vendor_summary":"keep"}],"content":[{"type":"reasoning_text","text":"details"}],"provider_state":{"keep":true}},
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","provider_state":{"keep":true},"content":[
				{"type":"output_text","text":"answer","annotations":[{"type":"url_citation","url":"https://example.com","title":"source","start_index":0,"end_index":6}],"logprobs":[],"vendor_part":"keep"},
				{"type":"refusal","refusal":"declined"}
			]}
		],
		"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}
	}`)
	frames, _, err := RenderNativeResponse(ProtocolResponses, source)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"response.created",
		"response.output_item.added", "response.reasoning_summary_part.added", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_summary_part.done",
		"response.content_part.added", "response.reasoning_text.delta", "response.reasoning_text.done", "response.content_part.done", "response.output_item.done",
		"response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.annotation.added", "response.output_text.done", "response.content_part.done",
		"response.content_part.added", "response.refusal.delta", "response.refusal.done", "response.content_part.done", "response.output_item.done",
		"response.completed",
	}
	if len(frames) != len(want) {
		t.Fatalf("frame count=%d want=%d: %#v", len(frames), len(want), frames)
	}
	for index, eventType := range want {
		if frames[index].Event != eventType || !strings.Contains(string(frames[index].Data), `"sequence_number":`+fmt.Sprint(index)) {
			t.Fatalf("frame %d=%s %s, want %s with sequence_number", index, frames[index].Event, frames[index].Data, eventType)
		}
	}
	if !strings.Contains(string(frames[14].Data), `"annotation_index":0`) || !strings.Contains(string(frames[14].Data), `"url":"https://example.com"`) {
		t.Fatalf("annotation event=%s", frames[14].Data)
	}
	for index, want := range map[int]string{
		0: `"completed_at":null`, 2: `"text":""`, 5: `"vendor_summary":"keep"`,
		6: `"text":""`, 10: `"provider_state":{"keep":true}`, 12: `"text":""`,
		16: `"vendor_part":"keep"`, 17: `"refusal":""`, 21: `"provider_state":{"keep":true}`,
	} {
		if !strings.Contains(string(frames[index].Data), want) {
			t.Fatalf("frame %d missing %s: %s", index, want, frames[index].Data)
		}
	}
	body, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss)
	var got, expected any
	if err == nil {
		err = json.Unmarshal(body, &got)
	}
	if err == nil {
		err = json.Unmarshal(source, &expected)
	}
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("round-trip body=%s error=%v", body, err)
	}
}

func TestNativeResponsesBufferedToolItemEvents(t *testing.T) {
	source := []byte(`{
		"id":"resp_tools","object":"response","model":"gpt-5.6","status":"completed",
		"output":[
			{"id":"ctc_1","type":"custom_tool_call","call_id":"call_custom","name":"shell","input":"echo hello","status":"completed"},
			{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"RouteMorph"}},
			{"id":"tsc_1","type":"tool_search_call","call_id":"call_search","arguments":{},"execution":"server","status":"completed"},
			{"id":"tso_1","type":"tool_search_output","call_id":"call_search","execution":"server","status":"completed","tools":[]},
			{"id":"tools_1","type":"additional_tools","role":"developer","tools":[]}
		],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`)
	frames, _, err := RenderNativeResponse(ProtocolResponses, source)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []string{
		"response.created",
		"response.output_item.added", "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done", "response.output_item.done",
		"response.output_item.added", "response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed", "response.output_item.done",
		"response.output_item.added", "response.output_item.done",
		"response.output_item.added", "response.output_item.done",
		"response.output_item.added", "response.output_item.done",
		"response.completed",
	}
	if len(frames) != len(wantTypes) {
		t.Fatalf("frame count = %d, want %d: %#v", len(frames), len(wantTypes), frames)
	}
	type renderedEvent struct {
		Type           string        `json:"type"`
		SequenceNumber *int          `json:"sequence_number"`
		OutputIndex    *int          `json:"output_index"`
		ItemID         string        `json:"item_id"`
		Delta          string        `json:"delta"`
		Input          string        `json:"input"`
		Item           responsesItem `json:"item"`
	}
	events := make([]renderedEvent, len(frames))
	for index, frame := range frames {
		if err := json.Unmarshal(frame.Data, &events[index]); err != nil {
			t.Fatalf("frame %d: %v", index, err)
		}
		if frame.Event != wantTypes[index] || events[index].Type != wantTypes[index] {
			t.Fatalf("frame %d type = event:%q payload:%q, want %q", index, frame.Event, events[index].Type, wantTypes[index])
		}
		if events[index].SequenceNumber == nil || *events[index].SequenceNumber != index {
			t.Fatalf("frame %d sequence_number = %v", index, events[index].SequenceNumber)
		}
		switch events[index].Type {
		case "response.output_item.added", "response.output_item.done":
			if events[index].OutputIndex == nil || events[index].Item.ID == "" {
				t.Fatalf("frame %d missing output_index/item: %s", index, frame.Data)
			}
		case "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done",
			"response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed":
			if events[index].OutputIndex == nil || events[index].ItemID == "" {
				t.Fatalf("frame %d missing output_index/item_id: %s", index, frame.Data)
			}
		}
		if strings.Contains(events[index].Type, "function_call_arguments") {
			t.Fatalf("custom tool call was rendered as a function call: %s", frame.Data)
		}
	}
	if events[1].Item.Type != "custom_tool_call" || events[1].Item.Status != "in_progress" || rawString(events[1].Item.Input) != "" {
		t.Fatalf("custom output_item.added = %#v", events[1].Item)
	}
	if events[2].ItemID != "ctc_1" || events[2].Delta != "echo hello" || events[3].Input != "echo hello" {
		t.Fatalf("custom input events = %#v / %#v", events[2], events[3])
	}
	if events[4].Item.Type != "custom_tool_call" || events[4].Item.Status != "completed" || rawString(events[4].Item.Input) != "echo hello" {
		t.Fatalf("custom output_item.done = %#v", events[4].Item)
	}
	for _, index := range []int{5, 10, 12} {
		if events[index].Item.Status != "in_progress" {
			t.Fatalf("frame %d added item status = %q", index, events[index].Item.Status)
		}
	}
	body, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip responsesResponse
	if err := json.Unmarshal(body, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if len(roundTrip.Output) != 5 || roundTrip.Output[0].Type != "custom_tool_call" || roundTrip.Output[4].Type != "additional_tools" {
		t.Fatalf("round-trip output = %#v", roundTrip.Output)
	}
}

func TestNativeResponsesBufferedOptionalItemStatusesRoundTrip(t *testing.T) {
	source := []byte(`{
		"id":"resp_optional_status","object":"response","model":"gpt-5.6","status":"completed",
		"output":[
			{"id":"rs_1","type":"reasoning","summary":[]},
			{"id":"fc_1","type":"function_call","call_id":"call_function","name":"lookup","arguments":"{}"},
			{"id":"ctc_1","type":"custom_tool_call","call_id":"call_custom","name":"shell","input":"pwd"}
		],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2},
		"vendor_large_integer":9007199254740993
	}`)
	frames, _, err := RenderNativeResponse(ProtocolResponses, source)
	if err != nil {
		t.Fatal(err)
	}
	for index, frame := range frames {
		if frame.Event != "response.output_item.added" {
			continue
		}
		var event struct {
			Item map[string]json.RawMessage `json:"item"`
		}
		if err := json.Unmarshal(frame.Data, &event); err != nil {
			t.Fatalf("frame %d: %v", index, err)
		}
		if _, exists := event.Item["status"]; exists {
			t.Fatalf("frame %d invented an optional item status: %s", index, frame.Data)
		}
	}
	body, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(source, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip body=%s", body)
	}
	if !bytes.Contains(frames[len(frames)-1].Data, []byte(`"vendor_large_integer":9007199254740993`)) ||
		!bytes.Contains(body, []byte(`"vendor_large_integer":9007199254740993`)) {
		t.Fatalf("large integer was not preserved exactly: terminal=%s body=%s", frames[len(frames)-1].Data, body)
	}
}

func TestNativeResponsesBufferedGeneratesStableCallItemIDs(t *testing.T) {
	source := []byte(`{
		"id":"resp_generated_ids","object":"response","model":"gpt-5.6","status":"completed",
		"output":[
			{"type":"function_call","call_id":"call_function","name":"lookup","arguments":"{}"},
			{"type":"custom_tool_call","call_id":"call_custom","name":"shell","input":"pwd"}
		],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`)
	frames, diagnostics, err := RenderNativeResponse(ProtocolResponses, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v, want two generated-id warnings", diagnostics)
	}
	for index, diagnostic := range diagnostics {
		if diagnostic.Code != "responses_output_item_id_generated" || diagnostic.Path != fmt.Sprintf("$.output[%d].id", index) {
			t.Fatalf("diagnostic %d = %#v", index, diagnostic)
		}
	}
	body, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	var response responsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 2 || response.Output[0].ID != "fc_generated_0" || response.Output[1].ID != "ctc_generated_1" {
		t.Fatalf("generated ids = %#v", response.Output)
	}
	for _, frame := range frames {
		if strings.Contains(frame.Event, "function_call_arguments") || strings.Contains(frame.Event, "custom_tool_call_input") {
			var event struct {
				ItemID string `json:"item_id"`
			}
			if err := json.Unmarshal(frame.Data, &event); err != nil || event.ItemID == "" {
				t.Fatalf("delta event lacks generated item id: %s", frame.Data)
			}
		}
	}
}

func TestNativeResponsesBufferedGenericItemsKeepSchemaSpecificStatuses(t *testing.T) {
	source := []byte(`{
		"id":"resp_generic","object":"response","model":"gpt-5.6","status":"completed",
		"output":[
			{"id":"program_output_1","type":"program_output","call_id":"call_program","result":"ok","status":"completed"},
			{"id":"patch_output_1","type":"apply_patch_call_output","call_id":"call_patch","status":"failed","output":"conflict"}
		],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`)
	frames, _, err := RenderNativeResponse(ProtocolResponses, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if frame.Event != "response.output_item.added" {
			continue
		}
		if bytes.Contains(frame.Data, []byte(`"status":"in_progress"`)) {
			t.Fatalf("generic item status was rewritten to an invalid enum: %s", frame.Data)
		}
	}
	body, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(source, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip body=%s", body)
	}
}

func TestNativeResponsesBufferedRejectsDuplicateOutputItemIDs(t *testing.T) {
	body := []byte(`{"id":"r","model":"m","status":"completed","output":[{"id":"dup","type":"message","role":"assistant","status":"completed","content":[]},{"id":"dup","type":"message","role":"assistant","status":"completed","content":[]}],"usage":{}}`)
	if _, _, err := RenderNativeResponse(ProtocolResponses, body); !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), "$.output[1].id") {
		t.Fatalf("error=%v, want ErrUpstreamResponse at duplicate item id", err)
	}
}

func TestNativeResponsesBufferedAcceptsOfficialIncompleteReasons(t *testing.T) {
	for _, reason := range []string{"max_output_tokens", "max_messages", "content_filter", "steered"} {
		t.Run(reason, func(t *testing.T) {
			body := []byte(`{"id":"r","model":"m","status":"incomplete","incomplete_details":{"reason":"` + reason + `"},"output":[],"usage":{}}`)
			if _, _, err := RenderNativeResponse(ProtocolResponses, body); err != nil {
				t.Fatalf("official incomplete reason rejected: %v", err)
			}
		})
	}
}

func TestNativeResponsesBufferedToolItemsRejectMissingEventFields(t *testing.T) {
	tests := []struct {
		name string
		item string
		path string
	}{
		{name: "message missing status", item: `{"id":"msg","type":"message","role":"assistant","content":[]}`, path: ".status"},
		{name: "message invalid part", item: `{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"input_text","text":"x"}]}`, path: ".content[0].type"},
		{name: "message output text missing annotations", item: `{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"x"}]}`, path: ".annotations"},
		{name: "message output text null text", item: `{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":null,"annotations":[]}]}`, path: ".text"},
		{name: "message output text scalar annotation", item: `{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"x","annotations":["bad"]}]}`, path: ".annotations[0]"},
		{name: "message output text scalar logprob", item: `{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"x","annotations":[],"logprobs":[1]}]}`, path: ".logprobs[0]"},
		{name: "message refusal null", item: `{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":null}]}`, path: ".refusal"},
		{name: "reasoning invalid summary", item: `{"id":"rs","type":"reasoning","status":"completed","summary":[{"type":"output_text","text":"x"}]}`, path: ".summary[0].type"},
		{name: "reasoning null summary text", item: `{"id":"rs","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":null}]}`, path: ".text"},
		{name: "reasoning object encrypted content", item: `{"id":"rs","type":"reasoning","summary":[],"encrypted_content":{"opaque":true}}`, path: ".encrypted_content"},
		{name: "custom invalid status", item: `{"id":"ctc","type":"custom_tool_call","call_id":"call","name":"shell","input":"echo","status":"queued"}`, path: ".status"},
		{name: "custom missing call id", item: `{"id":"ctc","type":"custom_tool_call","name":"shell","input":"echo"}`, path: ".call_id"},
		{name: "custom missing name", item: `{"id":"ctc","type":"custom_tool_call","call_id":"call","input":"echo"}`, path: ".name"},
		{name: "custom object input", item: `{"id":"ctc","type":"custom_tool_call","call_id":"call","name":"shell","input":{"command":"echo"}}`, path: ".input"},
		{name: "custom null input", item: `{"id":"ctc","type":"custom_tool_call","call_id":"call","name":"shell","input":null}`, path: ".input"},
		{name: "function invalid async", item: `{"id":"fc","type":"function_call","call_id":"call","name":"lookup","arguments":"{}","async":"yes"}`, path: ".async"},
		{name: "function invalid namespace", item: `{"id":"fc","type":"function_call","call_id":"call","name":"lookup","arguments":"{}","namespace":1}`, path: ".namespace"},
		{name: "function invalid caller", item: `{"id":"fc","type":"function_call","call_id":"call","name":"lookup","arguments":"{}","caller":{"type":"program"}}`, path: ".caller.caller_id"},
		{name: "custom invalid async", item: `{"id":"ctc","type":"custom_tool_call","call_id":"call","name":"shell","input":"echo","async":"yes"}`, path: ".async"},
		{name: "custom invalid caller", item: `{"id":"ctc","type":"custom_tool_call","call_id":"call","name":"shell","input":"echo","caller":1}`, path: ".caller"},
		{name: "function output missing output", item: `{"id":"fco","type":"function_call_output","status":"completed"}`, path: ".output"},
		{name: "function output object output", item: `{"id":"fco","type":"function_call_output","status":"completed","output":{}}`, path: ".output"},
		{name: "function output invalid created by", item: `{"id":"fco","type":"function_call_output","status":"completed","output":"ok","created_by":1}`, path: ".created_by"},
		{name: "custom output missing call id", item: `{"id":"ctco","type":"custom_tool_call_output","status":"completed","output":"ok"}`, path: ".call_id"},
		{name: "custom output missing status", item: `{"id":"ctco","type":"custom_tool_call_output","call_id":"call","output":"ok"}`, path: ".status"},
		{name: "custom output missing output", item: `{"id":"ctco","type":"custom_tool_call_output","call_id":"call","status":"completed"}`, path: ".output"},
		{name: "custom output invalid caller", item: `{"id":"ctco","type":"custom_tool_call_output","call_id":"call","status":"completed","output":"ok","caller":{"type":"future"}}`, path: ".caller.type"},
		{name: "web missing id", item: `{"type":"web_search_call","action":{"type":"search","query":"x"},"status":"completed"}`, path: ".id"},
		{name: "web missing action", item: `{"id":"ws","type":"web_search_call","status":"completed"}`, path: ".action"},
		{name: "web unknown action", item: `{"id":"ws","type":"web_search_call","action":{"type":"crawl"},"status":"completed"}`, path: ".action.type"},
		{name: "web numeric query", item: `{"id":"ws","type":"web_search_call","action":{"type":"search","query":123},"status":"completed"}`, path: ".action.query"},
		{name: "web null queries", item: `{"id":"ws","type":"web_search_call","action":{"type":"search","queries":null},"status":"completed"}`, path: ".action.queries"},
		{name: "web null sources", item: `{"id":"ws","type":"web_search_call","action":{"type":"search","sources":null},"status":"completed"}`, path: ".action.sources"},
		{name: "web unknown action field", item: `{"id":"ws","type":"web_search_call","action":{"type":"search","vendor":true},"status":"completed"}`, path: ".action.vendor"},
		{name: "web find missing url", item: `{"id":"ws","type":"web_search_call","action":{"type":"find_in_page","pattern":"needle"},"status":"completed"}`, path: ".action"},
		{name: "web find missing pattern", item: `{"id":"ws","type":"web_search_call","action":{"type":"find_in_page","url":"https://example.com"},"status":"completed"}`, path: ".action"},
		{name: "web incomplete status", item: `{"id":"ws","type":"web_search_call","action":{"type":"search","query":"x"},"status":"incomplete"}`, path: ".status"},
		{name: "web unknown status", item: `{"id":"ws","type":"web_search_call","action":{"type":"search","query":"x"},"status":"done"}`, path: ".status"},
		{name: "tool search missing arguments", item: `{"id":"ts","type":"tool_search_call","call_id":"call","execution":"server","status":"completed"}`, path: ".arguments"},
		{name: "client tool search missing call id", item: `{"id":"ts","type":"tool_search_call","arguments":{},"execution":"client","status":"completed"}`, path: ".call_id"},
		{name: "tool search empty call id", item: `{"id":"ts","type":"tool_search_call","call_id":"","arguments":{},"execution":"server","status":"completed"}`, path: ".call_id"},
		{name: "tool search invalid execution", item: `{"id":"ts","type":"tool_search_call","call_id":"call","arguments":{},"execution":"remote","status":"completed"}`, path: ".execution"},
		{name: "tool search output missing tools", item: `{"id":"tso","type":"tool_search_output","call_id":"call","execution":"server","status":"completed"}`, path: ".tools"},
		{name: "tool search output malformed tool", item: `{"id":"tso","type":"tool_search_output","call_id":"call","execution":"server","status":"completed","tools":[null]}`, path: ".tools[0]"},
		{name: "tool search output incomplete function", item: `{"id":"tso","type":"tool_search_output","call_id":"call","execution":"server","status":"completed","tools":[{"type":"function","name":"lookup"}]}`, path: ".tools[0].parameters"},
		{name: "tool search output unknown tool", item: `{"id":"tso","type":"tool_search_output","call_id":"call","execution":"server","status":"completed","tools":[{"type":"vendor_future"}]}`, path: ".tools[0].type"},
		{name: "additional tools missing role", item: `{"id":"tools","type":"additional_tools","tools":[]}`, path: ".role"},
		{name: "additional tools null tools", item: `{"id":"tools","type":"additional_tools","role":"developer","tools":null}`, path: ".tools"},
		{name: "additional tools missing tool type", item: `{"id":"tools","type":"additional_tools","role":"developer","tools":[{"name":"lookup"}]}`, path: ".tools[0]"},
		{name: "message invalid phase", item: `{"id":"msg","type":"message","role":"assistant","status":"completed","phase":"draft","content":[]}`, path: ".phase"},
		{name: "file search non-string query", item: `{"id":"fs","type":"file_search_call","status":"completed","queries":[1]}`, path: ".queries[0]"},
		{name: "file search invalid result score", item: `{"id":"fs","type":"file_search_call","status":"completed","queries":[],"results":[{"score":"high"}]}`, path: ".results[0].score"},
		{name: "file search invalid result attributes", item: `{"id":"fs","type":"file_search_call","status":"completed","queries":[],"results":[{"attributes":{"nested":{}}}]}`, path: ".attributes.nested"},
		{name: "computer call missing call id", item: `{"id":"cc","type":"computer_call","pending_safety_checks":[],"status":"completed","action":{"type":"screenshot"}}`, path: ".call_id"},
		{name: "computer call null safety checks", item: `{"id":"cc","type":"computer_call","call_id":"call","pending_safety_checks":null,"status":"completed","action":{"type":"screenshot"}}`, path: ".pending_safety_checks"},
		{name: "computer call invalid action coordinate", item: `{"id":"cc","type":"computer_call","call_id":"call","pending_safety_checks":[],"status":"completed","action":{"type":"click","button":"left","x":"1","y":2}}`, path: ".action.x"},
		{name: "computer output invalid screenshot", item: `{"id":"cco","type":"computer_call_output","call_id":"call","status":"completed","output":{"type":"image","image_url":"https://example.com/image.png"}}`, path: ".output.type"},
		{name: "computer output invalid created by", item: `{"id":"cco","type":"computer_call_output","call_id":"call","status":"completed","output":{"type":"computer_screenshot","file_id":"file"},"created_by":1}`, path: ".created_by"},
		{name: "program missing fingerprint", item: `{"id":"p","type":"program","call_id":"call","code":"return 1"}`, path: ".fingerprint"},
		{name: "program output invalid result", item: `{"id":"po","type":"program_output","call_id":"call","result":{},"status":"completed"}`, path: ".result"},
		{name: "compaction invalid encrypted content", item: `{"id":"cmp","type":"compaction","encrypted_content":1}`, path: ".encrypted_content"},
		{name: "image generation invalid result", item: `{"id":"ig","type":"image_generation_call","result":{},"status":"completed"}`, path: ".result"},
		{name: "code interpreter invalid output", item: `{"id":"ci","type":"code_interpreter_call","code":"print(1)","container_id":"container","outputs":[{"type":"logs","logs":1}],"status":"completed"}`, path: ".outputs[0].logs"},
		{name: "local shell invalid env", item: `{"id":"lsc","type":"local_shell_call","call_id":"call","action":{"type":"exec","command":["pwd"],"env":[]},"status":"completed"}`, path: ".action.env"},
		{name: "local shell invalid timeout", item: `{"id":"lsc","type":"local_shell_call","call_id":"call","action":{"type":"exec","command":["pwd"],"env":{},"timeout_ms":"soon"},"status":"completed"}`, path: ".action.timeout_ms"},
		{name: "local shell invalid output", item: `{"id":"lsco","type":"local_shell_call_output","output":{},"status":"completed"}`, path: ".output"},
		{name: "shell call invalid commands", item: `{"id":"sc","type":"shell_call","call_id":"call","action":{"commands":[1],"max_output_length":1024,"timeout_ms":1000},"environment":{"type":"local"},"status":"completed"}`, path: ".action.commands[0]"},
		{name: "shell call missing container", item: `{"id":"sc","type":"shell_call","call_id":"call","action":{"commands":["pwd"],"max_output_length":1024,"timeout_ms":1000},"environment":{"type":"container_reference"},"status":"completed"}`, path: ".environment.container_id"},
		{name: "shell output missing exit code", item: `{"id":"sco","type":"shell_call_output","call_id":"call","max_output_length":1024,"output":[{"stdout":"ok","stderr":"","outcome":{"type":"exit"}}],"status":"completed"}`, path: ".outcome.exit_code"},
		{name: "apply patch call missing diff", item: `{"id":"ap","type":"apply_patch_call","call_id":"call","operation":{"type":"update_file","path":"a.txt"},"status":"completed"}`, path: ".operation.diff"},
		{name: "apply patch output invalid output", item: `{"id":"apo","type":"apply_patch_call_output","call_id":"call","status":"completed","output":{}}`, path: ".output"},
		{name: "mcp call invalid output", item: `{"id":"mcp","type":"mcp_call","arguments":"{}","name":"lookup","server_label":"server","status":"completed","output":{}}`, path: ".output"},
		{name: "mcp call invalid error code", item: `{"id":"mcp","type":"mcp_call","arguments":"{}","name":"lookup","server_label":"server","status":"failed","error":{"type":"http_error","code":"500","message":"failed"}}`, path: ".error.code"},
		{name: "mcp list tools missing schema", item: `{"id":"list","type":"mcp_list_tools","server_label":"server","tools":[{"name":"lookup"}]}`, path: ".tools[0].input_schema"},
		{name: "mcp list tools invalid description", item: `{"id":"list","type":"mcp_list_tools","server_label":"server","tools":[{"name":"lookup","input_schema":{},"description":1}]}`, path: ".tools[0].description"},
		{name: "mcp approval request invalid arguments", item: `{"id":"approval","type":"mcp_approval_request","arguments":{},"name":"lookup","server_label":"server"}`, path: ".arguments"},
		{name: "mcp approval response invalid approve", item: `{"id":"approval","type":"mcp_approval_response","approval_request_id":"request","approve":"yes"}`, path: ".approve"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := `{"id":"r","model":"m","status":"completed","output":[` + test.item + `],"usage":{}}`
			if _, _, err := RenderNativeResponse(ProtocolResponses, []byte(body)); !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), test.path) {
				t.Errorf("body=%s error=%v, want %v containing %q", body, err, core.ErrUpstreamResponse, test.path)
			}
		})
	}
}

func TestNativeChatRendererDoesNotMasqueradeCustomToolCallsAsFunctions(t *testing.T) {
	body := []byte(`{"id":"chat","object":"chat.completion","model":"gpt-5.6","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"custom","custom":{"name":"shell","input":"echo hello"}}]},"finish_reason":"tool_calls"}],"usage":{}}`)
	frames, _, err := RenderNativeResponse(ProtocolChat, body)
	if !errors.Is(err, core.ErrUnsupported) || !strings.Contains(err.Error(), "$.choices[0].message.tool_calls[0].type") {
		t.Fatalf("frames=%#v error=%v, want ErrUnsupported at custom tool-call type", frames, err)
	}
}

func TestNativeChatRendererRejectsMalformedFunctionToolCalls(t *testing.T) {
	items := []string{
		`{"id":"call_1","custom":{"name":"shell","input":"echo"}}`,
		`{"id":"call_1","type":"function","function":{"arguments":"{}"}}`,
		`{"id":"call_1","type":"function","function":{"name":"lookup","arguments":{}}}`,
		`{"id":"call_1","type":"function","function":{"name":"lookup","arguments":null}}`,
	}
	for _, item := range items {
		body := []byte(`{"id":"chat","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[` + item + `]},"finish_reason":"tool_calls"}],"usage":{}}`)
		if _, _, err := RenderNativeResponse(ProtocolChat, body); !errors.Is(err, core.ErrUpstreamResponse) {
			t.Errorf("tool call %s error=%v, want ErrUpstreamResponse", item, err)
		}
	}
}

func TestNativeResponsesWebSearchLifecycleMatchesTerminalStatus(t *testing.T) {
	for _, test := range []struct {
		status string
		want   []string
	}{
		{status: "completed", want: []string{"response.output_item.added", "response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed", "response.output_item.done"}},
		{status: "failed", want: []string{"response.output_item.added", "response.web_search_call.in_progress", "response.output_item.done"}},
	} {
		t.Run(test.status, func(t *testing.T) {
			body := []byte(`{"id":"r","model":"m","status":"completed","output":[{"id":"ws","type":"web_search_call","action":{"type":"search","query":"x"},"status":"` + test.status + `"}],"usage":{}}`)
			frames, _, err := RenderNativeResponse(ProtocolResponses, body)
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) != len(test.want)+2 {
				t.Fatalf("frames=%#v, want %d item events", frames, len(test.want))
			}
			for index, want := range test.want {
				frame := frames[index+1]
				if frame.Event != want {
					t.Fatalf("event %d = %q, want %q", index, frame.Event, want)
				}
				var event struct {
					SequenceNumber *int   `json:"sequence_number"`
					OutputIndex    *int   `json:"output_index"`
					ItemID         string `json:"item_id"`
				}
				if err := json.Unmarshal(frame.Data, &event); err != nil {
					t.Fatal(err)
				}
				if event.SequenceNumber == nil || *event.SequenceNumber != index+1 || event.OutputIndex == nil || *event.OutputIndex != 0 {
					t.Fatalf("event %s missing sequence/output index: %s", frame.Event, frame.Data)
				}
				if strings.HasPrefix(frame.Event, "response.web_search_call.") && event.ItemID != "ws" {
					t.Fatalf("event %s item_id=%q, want ws", frame.Event, event.ItemID)
				}
			}
		})
	}
}

func TestNativeResponsesBufferedRejectsNonTerminalItemStatuses(t *testing.T) {
	tests := []struct {
		name string
		item string
	}{
		{name: "message", item: `{"id":"msg","type":"message","role":"assistant","status":"in_progress","content":[]}`},
		{name: "reasoning", item: `{"id":"rs","type":"reasoning","status":"in_progress","summary":[]}`},
		{name: "function call", item: `{"id":"fc","type":"function_call","call_id":"call","name":"lookup","arguments":"{}","status":"in_progress"}`},
		{name: "function output", item: `{"id":"fco","type":"function_call_output","call_id":"call","status":"in_progress","output":"ok"}`},
		{name: "custom output", item: `{"id":"ctco","type":"custom_tool_call_output","call_id":"call","status":"in_progress","output":"ok"}`},
		{name: "web search searching", item: `{"id":"ws","type":"web_search_call","status":"searching","action":{"type":"search","query":"x"}}`},
		{name: "file search searching", item: `{"id":"fs","type":"file_search_call","status":"searching","queries":[]}`},
		{name: "computer call", item: `{"id":"cc","type":"computer_call","call_id":"call","pending_safety_checks":[],"status":"in_progress","action":{"type":"screenshot"}}`},
		{name: "computer output", item: `{"id":"cco","type":"computer_call_output","call_id":"call","status":"in_progress","output":{"type":"computer_screenshot","image_url":"https://example.com/image.png"}}`},
		{name: "program output", item: `{"id":"po","type":"program_output","call_id":"call","result":"ok","status":"in_progress"}`},
		{name: "tool search call", item: `{"id":"ts","type":"tool_search_call","call_id":"call","arguments":{},"execution":"server","status":"in_progress"}`},
		{name: "tool search output", item: `{"id":"tso","type":"tool_search_output","call_id":"call","execution":"server","status":"in_progress","tools":[]}`},
		{name: "image generation generating", item: `{"id":"ig","type":"image_generation_call","result":"aGVsbG8=","status":"generating"}`},
		{name: "code interpreter interpreting", item: `{"id":"ci","type":"code_interpreter_call","code":"print(1)","container_id":"container","outputs":[],"status":"interpreting"}`},
		{name: "local shell call", item: `{"id":"lsc","type":"local_shell_call","call_id":"call","action":{"type":"exec","command":["pwd"],"env":{}},"status":"in_progress"}`},
		{name: "local shell output", item: `{"id":"lsco","type":"local_shell_call_output","output":"ok","status":"in_progress"}`},
		{name: "shell call", item: `{"id":"sc","type":"shell_call","call_id":"call","action":{"commands":["pwd"],"max_output_length":1024,"timeout_ms":1000},"environment":{"type":"local"},"status":"in_progress"}`},
		{name: "shell output", item: `{"id":"sco","type":"shell_call_output","call_id":"call","max_output_length":1024,"output":[{"stdout":"ok","stderr":"","outcome":{"type":"exit","exit_code":0}}],"status":"in_progress"}`},
		{name: "apply patch call", item: `{"id":"ap","type":"apply_patch_call","call_id":"call","operation":{"type":"delete_file","path":"old.txt"},"status":"in_progress"}`},
		{name: "mcp calling", item: `{"id":"mcp","type":"mcp_call","arguments":"{}","name":"lookup","server_label":"server","status":"calling"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"id":"r","model":"m","status":"completed","output":[` + test.item + `],"usage":{}}`)
			if _, _, err := RenderNativeResponse(ProtocolResponses, body); !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), ".status") {
				t.Fatalf("error=%v, want ErrUpstreamResponse at item status", err)
			}
		})
	}
}

func TestNativeResponsesBufferedAcceptsTerminalIncompleteAndFailedItemStatuses(t *testing.T) {
	tests := []struct {
		name string
		item string
	}{
		{name: "message incomplete", item: `{"id":"msg","type":"message","role":"assistant","status":"incomplete","content":[]}`},
		{name: "reasoning incomplete", item: `{"id":"rs","type":"reasoning","status":"incomplete","summary":[]}`},
		{name: "function call incomplete", item: `{"id":"fc","type":"function_call","call_id":"call","name":"lookup","arguments":"{}","status":"incomplete"}`},
		{name: "function output incomplete", item: `{"id":"fco","type":"function_call_output","call_id":"call","status":"incomplete","output":"partial"}`},
		{name: "custom output incomplete", item: `{"id":"ctco","type":"custom_tool_call_output","call_id":"call","status":"incomplete","output":"partial"}`},
		{name: "web search failed", item: `{"id":"ws","type":"web_search_call","status":"failed","action":{"type":"search","query":"x"}}`},
		{name: "file search incomplete", item: `{"id":"fs","type":"file_search_call","status":"incomplete","queries":[]}`},
		{name: "file search failed", item: `{"id":"fs","type":"file_search_call","status":"failed","queries":[]}`},
		{name: "computer call incomplete", item: `{"id":"cc","type":"computer_call","call_id":"call","pending_safety_checks":[],"status":"incomplete","action":{"type":"screenshot"}}`},
		{name: "computer output failed", item: `{"id":"cco","type":"computer_call_output","call_id":"call","status":"failed","output":{"type":"computer_screenshot","file_id":"file"}}`},
		{name: "program output incomplete", item: `{"id":"po","type":"program_output","call_id":"call","result":"partial","status":"incomplete"}`},
		{name: "tool search call incomplete", item: `{"id":"ts","type":"tool_search_call","call_id":"call","arguments":{},"execution":"server","status":"incomplete"}`},
		{name: "tool search output incomplete", item: `{"id":"tso","type":"tool_search_output","call_id":"call","execution":"server","status":"incomplete","tools":[]}`},
		{name: "image generation failed", item: `{"id":"ig","type":"image_generation_call","result":"","status":"failed"}`},
		{name: "code interpreter failed", item: `{"id":"ci","type":"code_interpreter_call","code":null,"container_id":"container","outputs":null,"status":"failed"}`},
		{name: "local shell call incomplete", item: `{"id":"lsc","type":"local_shell_call","call_id":"call","action":{"type":"exec","command":["pwd"],"env":{}},"status":"incomplete"}`},
		{name: "local shell output incomplete", item: `{"id":"lsco","type":"local_shell_call_output","output":"partial","status":"incomplete"}`},
		{name: "shell call incomplete", item: `{"id":"sc","type":"shell_call","call_id":"call","action":{"commands":["pwd"],"max_output_length":1024,"timeout_ms":1000},"environment":{"type":"local"},"status":"incomplete"}`},
		{name: "shell output incomplete", item: `{"id":"sco","type":"shell_call_output","call_id":"call","max_output_length":1024,"output":[{"stdout":"partial","stderr":"","outcome":{"type":"timeout"}}],"status":"incomplete"}`},
		{name: "apply patch output failed", item: `{"id":"apo","type":"apply_patch_call_output","call_id":"call","status":"failed","output":"conflict"}`},
		{name: "mcp call failed", item: `{"id":"mcp","type":"mcp_call","arguments":"{}","name":"lookup","server_label":"server","status":"failed","error":{"type":"http_error","code":500,"message":"failed"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"id":"r","model":"m","status":"completed","output":[` + test.item + `],"usage":{},"provider_extension":{"keep":true}}`)
			frames, _, err := RenderNativeResponse(ProtocolResponses, body)
			if err != nil {
				t.Fatalf("terminal item rejected: %v", err)
			}
			if !bytes.Contains(frames[len(frames)-1].Data, []byte(`"provider_extension":{"keep":true}`)) {
				t.Fatalf("top-level provider extension was not preserved: %s", frames[len(frames)-1].Data)
			}
		})
	}
}

func TestNativeResponsesBufferedAcceptsCompleteKnownUnionShapes(t *testing.T) {
	tests := []struct {
		name string
		item string
	}{
		{name: "file search results", item: `{"id":"fs","type":"file_search_call","status":"completed","queries":["docs"],"results":[{"file_id":"file","filename":"docs.md","score":0.9,"text":"found","attributes":{"page":1,"fresh":true}}]}`},
		{name: "computer actions", item: `{"id":"cc","type":"computer_call","call_id":"call","pending_safety_checks":[{"id":"safe","code":null,"message":"ok"}],"actions":[{"type":"keypress","keys":["CTRL","L"]}],"status":"completed"}`},
		{name: "program", item: `{"id":"p","type":"program","call_id":"call","code":"return 1","fingerprint":"opaque"}`},
		{name: "compaction", item: `{"id":"cmp","type":"compaction","encrypted_content":"opaque","created_by":"system"}`},
		{name: "code interpreter outputs", item: `{"id":"ci","type":"code_interpreter_call","code":"print(1)","container_id":"container","outputs":[{"type":"logs","logs":"1"},{"type":"image","url":"https://example.com/image.png"}],"status":"completed"}`},
		{name: "local shell nullable status", item: `{"id":"lsco","type":"local_shell_call_output","output":"ok","status":null}`},
		{name: "shell call", item: `{"id":"sc","type":"shell_call","call_id":"call","action":{"commands":["pwd"],"max_output_length":1024,"timeout_ms":1000},"environment":{"type":"container_reference","container_id":"container"},"caller":{"type":"program","caller_id":"program"},"created_by":"assistant","status":"completed"}`},
		{name: "apply patch call", item: `{"id":"ap","type":"apply_patch_call","call_id":"call","operation":{"type":"create_file","path":"new.txt","diff":"+hello"},"caller":{"type":"direct"},"created_by":"assistant","status":"completed"}`},
		{name: "mcp list tools", item: `{"id":"list","type":"mcp_list_tools","server_label":"server","tools":[{"name":"lookup","input_schema":{},"annotations":{"readOnlyHint":true},"description":null}],"error":null}`},
		{name: "mcp approval request", item: `{"id":"request","type":"mcp_approval_request","arguments":"{}","name":"lookup","server_label":"server"}`},
		{name: "mcp approval response", item: `{"id":"response","type":"mcp_approval_response","approval_request_id":"request","approve":false,"reason":null}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"id":"r","model":"m","status":"completed","output":[` + strings.TrimSuffix(test.item, "}") + `,"vendor_item":{"keep":true}}],"usage":{}}`)
			frames, _, err := RenderNativeResponse(ProtocolResponses, body)
			if err != nil {
				t.Fatalf("known union item rejected: %v", err)
			}
			var preserved bool
			for _, frame := range frames {
				if frame.Event == "response.output_item.done" && bytes.Contains(frame.Data, []byte(`"vendor_item":{"keep":true}`)) {
					preserved = true
				}
			}
			if !preserved {
				t.Fatalf("item provider extension was not preserved: %#v", frames)
			}
		})
	}
}

func TestNativeResponsesIncompleteUsesIncompleteTerminalEvent(t *testing.T) {
	body := []byte(`{"id":"r","object":"response","model":"m","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{}}`)
	frames, _, err := RenderNativeResponse(ProtocolResponses, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[1].Event != "response.incomplete" || !strings.Contains(string(frames[1].Data), `"sequence_number":1`) {
		t.Fatalf("frames = %#v", frames)
	}
	collected, _, err := CollectNativeResponse(ProtocolResponses, frames, core.RejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	var response responsesResponse
	if err := json.Unmarshal(collected, &response); err != nil || response.Status != "incomplete" {
		t.Fatalf("response=%#v error=%v", response, err)
	}
}

func TestNativeGeminiRendererRequiresTerminalFinishReason(t *testing.T) {
	invalid := []byte(`{"responseId":"g","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{}}}]}}],"usageMetadata":{}}`)
	if _, _, err := RenderNativeResponse(ProtocolGenerateContent, invalid); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("error=%v, want ErrUpstreamResponse", err)
	}
	valid := []byte(`{"responseId":"g","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{}}}]},"finishReason":"STOP"}],"usageMetadata":{}}`)
	frames, _, err := RenderNativeResponse(ProtocolGenerateContent, valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || string(frames[0].Data) != string(valid) || strings.Contains(string(frames[0].Data), `"type":"function"`) {
		t.Fatalf("Gemini native frame was rewritten: %#v", frames)
	}
}

func TestNativeGeminiRendererPreservesNativeMetadata(t *testing.T) {
	body := []byte(`{"responseId":"g","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"text":"answer"}]},"finishReason":"STOP","avgLogprobs":-0.25,"logprobsResult":{"chosenCandidates":[]},"citationMetadata":{"citations":[]},"groundingMetadata":{"groundingChunks":[]},"urlContextMetadata":{"urlMetadata":[]},"groundingAttributions":[],"safetyRatings":[],"tokenCount":1}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2,"promptTokensDetails":[{"modality":"TEXT","tokenCount":1}]},"promptFeedback":{"blockReason":"BLOCK_REASON_UNSPECIFIED"},"modelStatus":{"status":"ready"}}`)
	frames, diagnostics, err := RenderNativeResponse(ProtocolGenerateContent, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 0 || len(frames) != 1 || string(frames[0].Data) != string(body) {
		t.Fatalf("frames=%#v diagnostics=%#v", frames, diagnostics)
	}
}

func TestNativeChatRendererRejectsMalformedEnvelope(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		path string
	}{
		{name: "role", body: `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"user","content":"answer"},"finish_reason":"stop"}],"usage":{}}`, path: ".message.role"},
		{name: "content", body: `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":{"text":"answer"}},"finish_reason":"stop"}],"usage":{}}`, path: ".message.content"},
		{name: "finish reason", body: `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"future_reason"}],"usage":{}}`, path: ".finish_reason"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := RenderNativeResponse(ProtocolChat, []byte(test.body)); !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error=%v, want ErrUpstreamResponse containing %q", err, test.path)
			}
		})
	}
}

func TestNativeCollectorsRejectInvalidTerminalState(t *testing.T) {
	tests := []struct {
		name     string
		protocol Protocol
		frames   []streamFrame
		kind     error
	}{
		{"chat missing", ProtocolChat, []streamFrame{{Data: []byte(`{"choices":[{"index":0,"delta":{"content":"x"}}]}`)}}, core.ErrInvalidPayload},
		{"chat provider error", ProtocolChat, []streamFrame{{Data: []byte(`{"error":{"message":"boom"}}`)}}, core.ErrUpstreamResponse},
		{"responses missing", ProtocolResponses, []streamFrame{{Event: "response.created", Data: []byte(`{"type":"response.created"}`)}}, core.ErrInvalidPayload},
		{"responses failed", ProtocolResponses, []streamFrame{{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"status":"failed","error":{"message":"boom"},"output":[]}}`)}}, core.ErrUpstreamResponse},
		{"messages missing", ProtocolMessages, []streamFrame{{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"m","model":"x","usage":{}}}`)}}, core.ErrInvalidPayload},
		{"messages provider error", ProtocolMessages, []streamFrame{{Event: "error", Data: []byte(`{"type":"error","error":{"message":"boom"}}`)}}, core.ErrUpstreamResponse},
		{"gemini missing", ProtocolGenerateContent, []streamFrame{{Data: []byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`)}}, core.ErrInvalidPayload},
		{"gemini blocked", ProtocolGenerateContent, []streamFrame{{Data: []byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`)}}, core.ErrUpstreamResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := CollectNativeResponse(test.protocol, test.frames, core.RejectSemanticLoss); !errors.Is(err, test.kind) {
				t.Fatalf("error=%v, want %v", err, test.kind)
			}
		})
	}
	if _, _, err := CollectNativeResponse(Protocol("unknown"), nil, core.RejectSemanticLoss); !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("unknown protocol error=%v", err)
	}
	if _, _, err := RenderNativeResponse(Protocol("unknown"), nil); !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("unknown protocol error=%v", err)
	}
}

func TestFinishReasonValidation(t *testing.T) {
	for _, value := range []string{"length", "max_tokens", "tool_calls", "function_call", "content_filter", "stop"} {
		if _, err := parseChatFinish(value); err != nil {
			t.Fatalf("chat %q: %v", value, err)
		}
	}
	if _, err := parseChatFinish("unknown"); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("chat unknown=%v", err)
	}
	for _, value := range []string{"end_turn", "stop_sequence", "max_tokens", "tool_use", "refusal"} {
		if _, err := parseMessagesFinish(value); err != nil {
			t.Fatalf("messages %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "pause_turn", "unknown"} {
		if _, err := parseMessagesFinish(value); !errors.Is(err, core.ErrUpstreamResponse) {
			t.Fatalf("messages %q=%v", value, err)
		}
	}
	for _, value := range []string{"STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY"} {
		if _, err := parseGeminiFinish(value); err != nil {
			t.Fatalf("gemini %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "MALFORMED_FUNCTION_CALL", "OTHER"} {
		if _, err := parseGeminiFinish(value); !errors.Is(err, core.ErrUpstreamResponse) {
			t.Fatalf("gemini %q=%v", value, err)
		}
	}
}

func TestNativeRichResponseRoundTrips(t *testing.T) {
	tests := []struct {
		protocol Protocol
		body     string
	}{
		{ProtocolChat, `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"thought","refusal":"no","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{\"q\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7,"prompt_tokens_details":{"cached_tokens":1},"completion_tokens_details":{"reasoning_tokens":2}}}`},
		{ProtocolResponses, `{"id":"r","model":"m","status":"completed","output":[{"id":"call","type":"function_call","status":"completed","call_id":"call","name":"lookup","arguments":"{\"q\":1}"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`},
		{ProtocolMessages, `{"id":"a","type":"message","role":"assistant","model":"m","content":[{"type":"thinking","thinking":"thought","signature":"sig"},{"type":"tool_use","id":"call","name":"lookup","input":{"q":1}}],"stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":3}}`},
	}
	for _, test := range tests {
		t.Run(string(test.protocol), func(t *testing.T) {
			frames, _, err := RenderNativeResponse(test.protocol, []byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := CollectNativeResponse(test.protocol, frames, core.RejectSemanticLoss); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeRenderRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		protocol Protocol
		body     string
		kind     error
	}{
		{ProtocolChat, `{"error":{"message":"boom"}}`, core.ErrUpstreamResponse},
		{ProtocolChat, `{"choices":[]}`, core.ErrUnsupported},
		{ProtocolResponses, `{"status":"failed","error":{"message":"boom"},"output":[]}`, core.ErrUpstreamResponse},
		{ProtocolMessages, `{"type":"message","role":"user","content":[],"stop_reason":"end_turn"}`, core.ErrUpstreamResponse},
		{ProtocolGenerateContent, `{"candidates":[{},{}]}`, core.ErrUnsupported},
	}
	for _, test := range tests {
		if _, _, err := RenderNativeResponse(test.protocol, []byte(test.body)); !errors.Is(err, test.kind) {
			t.Errorf("%s body=%s error=%v, want %v", test.protocol, test.body, err, test.kind)
		}
	}
}

func TestProtocolEnvelopeValidationBranches(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`), json.RawMessage(`[]`)} {
		if _, err := decodeMessagesBlocks(raw, "$.content"); err == nil {
			t.Fatalf("messages content %s unexpectedly accepted", raw)
		}
	}
	blocks, err := decodeMessagesBlocks(json.RawMessage(`"text"`), "$.content")
	if err != nil || len(blocks) != 1 || blocks[0].Text != "text" {
		t.Fatalf("blocks=%#v error=%v", blocks, err)
	}

	invalidMessages := []messagesResponse{
		{Type: "error"},
		{Type: "message", Role: "user"},
		{Type: "message", Role: "assistant", ID: "id"},
		{Type: "message", Role: "assistant", ID: "id", Model: "m", Content: json.RawMessage(`{}`)},
		{Type: "message", Role: "assistant", ID: "id", Model: "m", Content: json.RawMessage(`[]`), StopReason: "end_turn", Usage: messagesUsage{InputTokens: -1}},
		{Type: "message", Role: "assistant", ID: "id", Model: "m", Content: json.RawMessage(`[]`), StopReason: "stop_sequence"},
	}
	for _, response := range invalidMessages {
		if err := validateMessagesResponse(response); err == nil {
			t.Fatalf("invalid Messages response accepted: %#v", response)
		}
	}

	items := []responsesItem{
		{Type: "message", Role: "user"},
		{Type: "message", Role: "assistant", EncryptedContent: json.RawMessage(`"secret"`)},
		{Type: "function_call"},
		{Type: "function_call_output"},
	}
	for _, item := range items {
		if err := validateResponsesItems([]responsesItem{item}, "$.output"); err == nil {
			t.Fatalf("invalid Responses item accepted: %#v", item)
		}
	}
	for _, response := range []responsesResponse{
		{Status: "cancelled"},
		{Status: "queued"},
		{Status: "incomplete", IncompleteDetails: &struct {
			Reason string `json:"reason"`
		}{Reason: "unknown"}},
	} {
		if err := validateResponsesTerminal(response); err == nil {
			t.Fatalf("invalid terminal response accepted: %#v", response)
		}
	}

	average := -0.5
	gemini := &geminiResponse{
		Candidates: []geminiCandidate{{
			AvgLogprobs:        &average,
			SafetyRatings:      json.RawMessage(`[{"category":"safe"}]`),
			CitationMetadata:   json.RawMessage(`{"source":"x"}`),
			GroundingMetadata:  json.RawMessage(`{"source":"x"}`),
			URLContextMetadata: json.RawMessage(`{"source":"x"}`),
		}},
		PromptFeedback: json.RawMessage(`{"blockReason":"BLOCK_REASON_UNSPECIFIED"}`),
	}
	gemini.UsageMetadata.PromptTokensDetails = json.RawMessage(`[{"modality":"TEXT","tokenCount":1}]`)
	if _, err := validateGeminiResponseEnvelope(gemini, core.RejectSemanticLoss); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("strict Gemini metadata error=%v", err)
	}
	diagnostics, err := validateGeminiResponseEnvelope(gemini, core.AllowDocumentedLoss)
	if err != nil || len(diagnostics) < 4 {
		t.Fatalf("diagnostics=%#v error=%v", diagnostics, err)
	}
	if _, err := validateGeminiResponseEnvelope(nil, core.RejectSemanticLoss); !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("nil Gemini response error=%v", err)
	}
}
