package routemorph

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCodexCLI01491NativeResponsesRequestAndStreamRegression(t *testing.T) {
	const clientModel = "codex-client-model"
	const upstreamModel = "gpt-5.4"
	requestBody := `{
		"model":"codex-client-model",
		"instructions":"Use tools when needed and return a concise final answer.",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"Work only in the fixture workspace."}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Read fixture.txt and report its marker."}]}
		],
		"tools":[{
			"type":"function",
			"name":"exec_command",
			"description":"Run a command in the fixture workspace.",
			"parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"],"additionalProperties":false},
			"strict":false
		}],
		"tool_choice":"auto",
		"parallel_tool_calls":true,
		"reasoning":{"effort":"high","summary":"auto"},
		"store":false,
		"stream":true,
		"include":["reasoning.encrypted_content"],
		"service_tier":"default",
		"prompt_cache_key":"00000000-0000-4000-8000-000000000001",
		"text":{"verbosity":"medium"},
		"client_metadata":{
			"session_id":"00000000-0000-4000-8000-000000000002",
			"thread_id":"00000000-0000-4000-8000-000000000002",
			"turn_id":"00000000-0000-4000-8000-000000000003",
			"x-codex-installation-id":"00000000-0000-4000-8000-000000000004",
			"x-codex-window-id":"00000000-0000-4000-8000-000000000002:1",
			"x-codex-turn-metadata":"{\"request_kind\":\"turn\",\"sandbox\":\"workspace-write\"}"
		}
	}`

	receivedBody := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read upstream request: %v", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		receivedBody <- body
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, codexCLI01491ResponsesStream)
	}))
	defer upstream.Close()

	adapter, err := NewOpenAIResponsesAdapter(upstream.URL, "", WithModel(upstreamModel))
	if err != nil {
		t.Fatalf("new Responses adapter: %v", err)
	}
	response, err := adapter.OpenAIResponses(context.Background(), &Request{
		Header: make(http.Header),
		Body:   strings.NewReader(requestBody),
	})
	if err != nil {
		t.Fatalf("invoke native Responses adapter: %v", err)
	}
	streamBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read stream: read error=%v close error=%v body=%s", readErr, closeErr, streamBody)
	}

	wantRequest := codexCLI01491JSONObject(t, []byte(requestBody))
	wantRequest["model"] = upstreamModel
	gotRequest := codexCLI01491JSONObject(t, <-receivedBody)
	if !reflect.DeepEqual(gotRequest, wantRequest) {
		t.Fatalf("native request changed beyond WithModel override:\n got: %#v\nwant: %#v", gotRequest, wantRequest)
	}

	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("response status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if !response.Meta.Stream || response.Meta.RouteMode != RouteModeNative ||
		response.Meta.IngressProtocol != ProtocolResponses || response.Meta.UpstreamProtocol != ProtocolResponses {
		t.Fatalf("response meta = %#v", response.Meta)
	}
	if diagnostics := response.Meta.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("native relay diagnostics = %#v, want none", diagnostics)
	}

	wantEvents := codexCLI01491SSEEvents(t, codexCLI01491ResponsesStream)
	for _, event := range wantEvents {
		if snapshot, ok := event.Data["response"].(map[string]any); ok {
			snapshot["model"] = clientModel
		}
	}
	gotEvents := codexCLI01491SSEEvents(t, string(streamBody))
	if !reflect.DeepEqual(gotEvents, wantEvents) {
		t.Fatalf("native Responses SSE lifecycle changed:\n got: %#v\nwant: %#v", gotEvents, wantEvents)
	}
}

type codexCLI01491SSEEvent struct {
	Event string
	Data  map[string]any
}

func codexCLI01491JSONObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("decode JSON object: %v", err)
	}
	return object
}

func codexCLI01491SSEEvents(t *testing.T, stream string) []codexCLI01491SSEEvent {
	t.Helper()
	var events []codexCLI01491SSEEvent
	var eventName string
	var dataLines []string
	flush := func() {
		if eventName == "" && len(dataLines) == 0 {
			return
		}
		data := codexCLI01491JSONObject(t, []byte(strings.Join(dataLines, "\n")))
		events = append(events, codexCLI01491SSEEvent{Event: eventName, Data: data})
		eventName = ""
		dataLines = nil
	}
	scanner := bufio.NewScanner(strings.NewReader(stream))
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan SSE: %v", err)
	}
	flush()
	return events
}

const codexCLI01491ResponsesStream = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_fixture","object":"response","created_at":1,"model":"gpt-5.4","status":"in_progress","service_tier":"default","output":[],"usage":null}}

event: response.in_progress
data: {"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_fixture","object":"response","created_at":1,"model":"gpt-5.4","status":"in_progress","service_tier":"default","output":[],"usage":null}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"rs_fixture","type":"reasoning","status":"in_progress","summary":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"rs_fixture","type":"reasoning","status":"completed","summary":[],"encrypted_content":"fixture-ciphertext-before-terminal"}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":4,"output_index":1,"item":{"id":"msg_fixture","type":"message","role":"assistant","status":"in_progress","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","sequence_number":5,"item_id":"msg_fixture","output_index":1,"content_index":0,"part":{"type":"output_text","text":"","annotations":[],"logprobs":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":6,"item_id":"msg_fixture","output_index":1,"content_index":0,"delta":"fixture-ok","logprobs":[]}

event: response.output_text.done
data: {"type":"response.output_text.done","sequence_number":7,"item_id":"msg_fixture","output_index":1,"content_index":0,"text":"fixture-ok","logprobs":[]}

event: response.content_part.done
data: {"type":"response.content_part.done","sequence_number":8,"item_id":"msg_fixture","output_index":1,"content_index":0,"part":{"type":"output_text","text":"fixture-ok","annotations":[],"logprobs":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":9,"output_index":1,"item":{"id":"msg_fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fixture-ok","annotations":[],"logprobs":[]}]}}

event: response.completed
data: {"type":"response.completed","sequence_number":10,"response":{"id":"resp_fixture","object":"response","created_at":1,"model":"gpt-5.4","status":"completed","service_tier":"default","output":[{"id":"rs_fixture","type":"reasoning","status":"completed","summary":[],"encrypted_content":"fixture-ciphertext-at-terminal"},{"id":"msg_fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fixture-ok","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}}

`
