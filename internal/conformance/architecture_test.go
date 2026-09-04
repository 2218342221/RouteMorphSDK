package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestProtocolCodecContract(t *testing.T) {
	tests := []struct {
		adapter *protocolCodec
		body    string
		hint    requestHint
		model   string
	}{
		{newProtocolCodec(ProtocolChat), `{"model":"chat-model","messages":[{"role":"user","content":"hi"}],"future":{"keep":true}}`, requestHint{}, "chat-model"},
		{newProtocolCodec(ProtocolResponses), `{"model":"responses-model","input":"hi","future":{"keep":true}}`, requestHint{}, "responses-model"},
		{newProtocolCodec(ProtocolMessages), `{"model":"messages-model","max_tokens":10,"messages":[{"role":"user","content":"hi"}],"future":{"keep":true}}`, requestHint{}, "messages-model"},
		{newProtocolCodec(ProtocolGenerateContent), `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"future":{"keep":true}}`, requestHint{Model: "gemini-model"}, "gemini-model"},
	}
	for _, test := range tests {
		t.Run(string(test.adapter.Protocol()), func(t *testing.T) {
			metadata, err := test.adapter.InspectRequest(context.Background(), []byte(test.body), test.hint)
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if metadata.Model != test.model {
				t.Fatalf("model = %q, want %q", metadata.Model, test.model)
			}
			if err := test.adapter.ValidateRequest(context.Background(), []byte(test.body), test.hint); err != nil {
				t.Fatalf("ValidateRequest() error = %v", err)
			}
			newModel, stream := "target-model", true
			patched, err := test.adapter.PatchRequest(context.Background(), []byte(test.body), requestPatch{Model: &newModel, Stream: &stream})
			if err != nil {
				t.Fatalf("PatchRequest() error = %v", err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(patched, &object); err != nil {
				t.Fatalf("patched JSON: %v", err)
			}
			if _, ok := object["future"]; !ok {
				t.Fatalf("unknown field was lost: %s", patched)
			}
		})
	}
}

func TestRequestHintSupportsGeminiProtocolCodec(t *testing.T) {
	stream := true
	metadata, err := newProtocolCodec(ProtocolGenerateContent).InspectRequest(
		context.Background(),
		[]byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
		requestHint{Model: "gemini-model", Stream: &stream},
	)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Model != "gemini-model" || !metadata.Stream {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestMessagesAdapterAcceptsNativeCacheWarmupRequest(t *testing.T) {
	body := []byte(`{"model":"claude","max_tokens":0,"messages":[{"role":"user","content":"warm cache"}]}`)
	if err := newProtocolCodec(ProtocolMessages).ValidateRequest(context.Background(), body, requestHint{}); err != nil {
		t.Fatalf("native max_tokens=0 request rejected: %v", err)
	}
}

func TestProtocolCodecsValidateNativeResponseShape(t *testing.T) {
	tests := []struct {
		adapter *protocolCodec
		valid   string
		invalid string
	}{
		{newProtocolCodec(ProtocolChat), `{"choices":[]}`, `{"choices":{}}`},
		{newProtocolCodec(ProtocolResponses), `{"output":[]}`, `{"output":null}`},
		{newProtocolCodec(ProtocolMessages), `{"content":[]}`, `{"content":"text"}`},
		{newProtocolCodec(ProtocolGenerateContent), `{"promptFeedback":{"blockReason":"SAFETY"}}`, `{}`},
	}
	for _, test := range tests {
		t.Run(string(test.adapter.Protocol()), func(t *testing.T) {
			if err := test.adapter.ValidateResponse(context.Background(), []byte(test.valid)); err != nil {
				t.Fatalf("valid response rejected: %v", err)
			}
			if err := test.adapter.ValidateResponse(context.Background(), []byte(test.invalid)); !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("invalid response error = %v", err)
			}
			if err := test.adapter.ValidateResponse(context.Background(), []byte(`{"error":{"message":"failed"}}`)); !errors.Is(err, ErrUpstreamResponse) {
				t.Fatalf("embedded error = %v", err)
			}
		})
	}
}

func TestBuiltinRouterHasEveryDirectedRoute(t *testing.T) {
	catalog, err := newTestRouteCatalog()
	if err != nil {
		t.Fatal(err)
	}
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, from := range protocols {
		for _, to := range protocols {
			plan, err := catalog.Plan(from, to)
			if err != nil {
				t.Fatalf("Plan(%s, %s) error = %v", from, to, err)
			}
			if from != to && len(plan.RouteIDs) == 0 {
				t.Fatalf("Plan(%s, %s) is empty", from, to)
			}
			if from != to && len(plan.RouteIDs) != 1 {
				t.Fatalf("Plan(%s, %s) is not direct: %#v", from, to, plan.RouteIDs)
			}
			if from == to && plan.RouteMode != RouteModeNative {
				t.Fatalf("Plan(%s, %s) mode = %q", from, to, plan.RouteMode)
			}
			if from != to && plan.RouteMode == RouteModeNative {
				t.Fatalf("Plan(%s, %s) unexpectedly reports native mode", from, to)
			}
		}
	}
}

func TestRouterChatResponsesRoundTrip(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	options := conversionOptions{Exchange: exchangeMetadata{ClientModel: "public-model", UpstreamModel: "provider-model"}}
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, ProtocolResponses, []byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`), options)
	if err != nil {
		t.Fatalf("ToUpstreamRequest() error = %v", err)
	}
	var request map[string]any
	_ = json.Unmarshal(execution.Result.Body, &request)
	if request["model"] != "provider-model" {
		t.Fatalf("upstream request = %s", execution.Result.Body)
	}
	response := []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"provider-model","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	converted, err := harness.ToClientResponse(context.Background(), execution.Plan, response, options)
	if err != nil {
		t.Fatalf("ToClientResponse() error = %v", err)
	}
	if !bytes.Contains(converted.Body, []byte(`"model":"public-model"`)) || !bytes.Contains(converted.Body, []byte(`"content":"hello"`)) {
		t.Fatalf("client response = %s", converted.Body)
	}
}

func TestSameProtocolDefaultOptionsPreserveStream(t *testing.T) {
	harness, _ := newTestRouterHarness()
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, ProtocolChat, []byte(`{"model":"x","messages":[{"role":"user","content":"hi"}],"stream":true}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(execution.Result.Body, &request); err != nil || !request.Stream {
		t.Fatalf("request=%s error=%v", execution.Result.Body, err)
	}
}

func TestSameProtocolWithoutPatchIsByteExactPassThrough(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(" { \n  \"model\" : \"x\", \"messages\" : [ { \"role\" : \"user\", \"content\" : \"hi\" } ] \n } ")
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, ProtocolChat, body, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(execution.Result.Body, body) {
		t.Fatalf("same-protocol body was rewritten:\n got: %q\nwant: %q", execution.Result.Body, body)
	}
	execution.Result.Body[0] = '!'
	if body[0] == '!' {
		t.Fatal("pass-through result aliases caller body")
	}
	if _, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, ProtocolChat, []byte(`not-json`), conversionOptions{}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("malformed pass-through error = %v", err)
	}
}

func TestSSEFramingMultilineAndLimit(t *testing.T) {
	input := "event: sample\ndata: one\ndata: two\n\n: heartbeat\n\ndata: [DONE]\n\n"
	decoder := newSSEDecoder(strings.NewReader(input), streamOptions{})
	frame, err := decoder.Next(context.Background())
	if err != nil || frame.Event != "sample" || string(frame.Data) != "one\ntwo" {
		t.Fatalf("first frame = %#v, err = %v", frame, err)
	}
	frame, err = decoder.Next(context.Background())
	if err != nil || !frame.Done {
		t.Fatalf("done frame = %#v, err = %v", frame, err)
	}
	if _, err := decoder.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal error = %v", err)
	}
	limited := newSSEDecoder(strings.NewReader("data: 123456\n\n"), streamOptions{MaxFrameBytes: 5})
	if _, err := limited.Next(context.Background()); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("limit error = %v", err)
	}
}

func TestSSEEncoderRejectsOversizedAndInvalidFrames(t *testing.T) {
	var output bytes.Buffer
	encoder, err := newProtocolCodec(ProtocolChat).NewStreamEncoder(&output, streamOptions{MaxFrameBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Write(context.Background(), streamFrame{Data: []byte("123456789")}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("oversized frame error = %v", err)
	}
	if err := encoder.Write(context.Background(), streamFrame{Event: "bad\nevent", Data: []byte("{}")}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("invalid event error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := encoder.Write(cancelled, streamFrame{Data: []byte("{}")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write error = %v", err)
	}
}

func TestProtocolCodecsEncodeStreamErrors(t *testing.T) {
	for _, adapter := range []*protocolCodec{newProtocolCodec(ProtocolChat), newProtocolCodec(ProtocolResponses), newProtocolCodec(ProtocolMessages), newProtocolCodec(ProtocolGenerateContent)} {
		frame, err := adapter.EncodeStreamError(ProtocolError{Type: "upstream_error", Code: "stream_error", Message: "failed", StatusCode: 502})
		if err != nil || !json.Valid(frame.Data) {
			t.Fatalf("%s frame=%#v error=%v", adapter.Protocol(), frame, err)
		}
		if adapter.Protocol() == ProtocolResponses {
			var object map[string]any
			_ = json.Unmarshal(frame.Data, &object)
			if frame.Event != "error" || object["type"] != "error" || object["code"] != "stream_error" {
				t.Fatalf("Responses error frame = %#v body=%s", frame, frame.Data)
			}
		}
	}
}

func TestResponseStreamLifecycle(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolChat, ProtocolResponses)
	stream, err := harness.NewResponseStream(context.Background(), plan, conversionOptions{Exchange: exchangeMetadata{ClientModel: "public-model"}})
	if err != nil {
		t.Fatal(err)
	}
	completed := streamFrame{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"model":"provider-model","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)}
	converted, _, err := stream.Convert(context.Background(), completed)
	if err != nil || len(converted) < 2 || !converted[len(converted)-1].Done {
		t.Fatalf("Convert() frames=%#v error=%v", converted, err)
	}
	frames, diagnostics, err := stream.Finalize(context.Background())
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if len(frames) != 0 || len(diagnostics) != 0 {
		t.Fatalf("frames=%#v diagnostics=%#v", frames, diagnostics)
	}
	if _, _, err := stream.Finalize(context.Background()); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("second Finalize() error = %v", err)
	}
}

func TestDecodeJSONRejectsTrailingValue(t *testing.T) {
	adapter := newProtocolCodec(ProtocolChat)
	_, err := adapter.InspectRequest(context.Background(), []byte(`{"model":"x","messages":[]} {}`), requestHint{})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("error = %v", err)
	}
}

func FuzzSSEDecoder(f *testing.F) {
	f.Add("event: message\ndata: {}\n\n")
	f.Add("data: [DONE]\n\n")
	f.Fuzz(func(t *testing.T, input string) {
		decoder := newSSEDecoder(strings.NewReader(input), streamOptions{MaxFrameBytes: 64 << 10})
		for index := 0; index < 100; index++ {
			_, err := decoder.Next(context.Background())
			if err != nil {
				return
			}
		}
	})
}

func FuzzProtocolCodecs(f *testing.F) {
	f.Add(byte(0), []byte(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`))
	f.Add(byte(1), []byte(`{"model":"x","input":"hi"}`))
	f.Add(byte(2), []byte(`{"model":"x","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	f.Add(byte(3), []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	f.Fuzz(func(t *testing.T, selector byte, body []byte) {
		adapters := []*protocolCodec{newProtocolCodec(ProtocolChat), newProtocolCodec(ProtocolResponses), newProtocolCodec(ProtocolMessages), newProtocolCodec(ProtocolGenerateContent)}
		adapter := adapters[int(selector)%len(adapters)]
		hint := requestHint{}
		if adapter.Protocol() == ProtocolGenerateContent {
			hint.Model = "gemini"
		}
		if _, err := adapter.InspectRequest(context.Background(), body, hint); err != nil {
			return
		}
		model := "patched"
		patched, err := adapter.PatchRequest(context.Background(), body, requestPatch{Model: &model})
		if err == nil && !json.Valid(patched) {
			t.Fatalf("PatchRequest returned invalid JSON: %q", patched)
		}
	})
}

func FuzzRouterConversion(f *testing.F) {
	f.Add(byte(0), byte(1), []byte(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`))
	f.Add(byte(1), byte(0), []byte(`{"model":"x","input":"hi"}`))
	f.Fuzz(func(t *testing.T, fromSelector, toSelector byte, body []byte) {
		protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
		from := protocols[int(fromSelector)%len(protocols)]
		to := protocols[int(toSelector)%len(protocols)]
		harness, err := newTestRouterHarness()
		if err != nil {
			t.Fatal(err)
		}
		result, err := harness.ToUpstreamRequest(context.Background(), from, to, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
		if err == nil && !json.Valid(result.Result.Body) {
			t.Fatalf("ToUpstreamRequest returned invalid JSON: %q", result.Result.Body)
		}
	})
}
