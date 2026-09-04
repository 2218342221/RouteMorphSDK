package conformance

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

func TestSameProtocolContractMatrixIsByteExact(t *testing.T) {
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, protocol := range protocols {
		t.Run(string(protocol), func(t *testing.T) {
			harness, err := newTestRouterHarness()
			if err != nil {
				t.Fatal(err)
			}
			request := readRelayFixture(t, protocol, "request")
			response := readRelayFixture(t, protocol, "response")
			options := conversionOptions{Exchange: exchangeMetadata{ClientModel: "fixture-model"}}
			execution, err := harness.ToUpstreamRequest(context.Background(), protocol, protocol, request, options)
			if err != nil || !bytes.Equal(execution.Result.Body, request) {
				t.Fatalf("request body=%q error=%v", execution.Result.Body, err)
			}
			converted, err := harness.ToClientResponse(context.Background(), execution.Plan, response, options)
			if err != nil || !bytes.Equal(converted.Body, response) {
				t.Fatalf("response body=%q error=%v", converted.Body, err)
			}
			stream, err := harness.NewResponseStream(context.Background(), execution.Plan, options)
			if err != nil {
				t.Fatal(err)
			}
			input := streamFrame{Event: "opaque", Data: []byte(" exact frame ")}
			frames, diagnostics, err := stream.Convert(context.Background(), input)
			if err != nil || len(diagnostics) != 0 || len(frames) != 1 || frames[0].Event != input.Event || !bytes.Equal(frames[0].Data, input.Data) {
				t.Fatalf("frames=%#v diagnostics=%#v error=%v", frames, diagnostics, err)
			}
			if _, _, err := stream.Finalize(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCrossProtocolRequestContractMatrixAppliesExchangeMetadata(t *testing.T) {
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, from := range protocols {
		for _, to := range protocols {
			if from == to {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				harness, err := newTestRouterHarness()
				if err != nil {
					t.Fatal(err)
				}
				stream := true
				execution, err := harness.ToUpstreamRequest(
					context.Background(),
					from,
					to,
					readRelayFixture(t, from, "request"),
					conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider-model", Stream: stream, StreamSet: true}},
				)
				if err != nil {
					t.Fatalf("convert request: %v", err)
				}
				info, err := newProtocolCodec(to).InspectRequest(context.Background(), execution.Result.Body, requestHint{Model: "provider-model", Stream: &stream})
				if err != nil {
					t.Fatalf("inspect converted request: %v\nbody=%s", err, execution.Result.Body)
				}
				if info.Model != "provider-model" || !info.Stream {
					t.Fatalf("metadata=%#v\nbody=%s", info, execution.Result.Body)
				}
			})
		}
	}
}

func TestCrossProtocolStreamContractMatrix(t *testing.T) {
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	responses := make(map[Protocol][]byte, len(protocols))
	validFrames := make(map[Protocol][]streamFrame, len(protocols))
	for _, protocol := range protocols {
		responses[protocol] = readRelayFixture(t, protocol, "response")
		frames, _, err := renderNativeResponseStream(protocol, responses[protocol])
		if err != nil {
			t.Fatalf("render %s fixture: %v", protocol, err)
		}
		validFrames[protocol] = frames
	}

	for _, from := range protocols {
		for _, to := range protocols {
			if from == to {
				continue
			}
			name := string(from) + "_from_" + string(to)
			t.Run(name+"/normal", func(t *testing.T) {
				frames, err := runStreamContract(t, from, to, validFrames[to])
				if err != nil {
					t.Fatal(err)
				}
				if len(frames) == 0 {
					t.Fatal("stream produced no target frames")
				}
			})
			t.Run(name+"/missing_terminal", func(t *testing.T) {
				if _, err := runStreamContract(t, from, to, truncatedStream(to)); err == nil {
					t.Fatal("truncated stream unexpectedly succeeded")
				}
			})
			t.Run(name+"/early_close", func(t *testing.T) {
				if _, err := runStreamContract(t, from, to, nil); err == nil {
					t.Fatal("empty upstream stream unexpectedly succeeded")
				}
			})
			t.Run(name+"/error_frame", func(t *testing.T) {
				if _, err := runStreamContract(t, from, to, []streamFrame{protocolErrorFrame(to)}); !errors.Is(err, ErrUpstreamResponse) {
					t.Fatalf("error=%v, want ErrUpstreamResponse", err)
				}
			})
		}
	}
}

func TestCrossProtocolStreamFrameLimitMatrix(t *testing.T) {
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, from := range protocols {
		for _, to := range protocols {
			if from == to {
				continue
			}
			t.Run(string(from)+"_from_"+string(to), func(t *testing.T) {
				decoder := newSSEDecoder(bytes.NewReader([]byte("data: 123456789\n\n")), streamOptions{MaxFrameBytes: 8})
				if _, err := decoder.Next(context.Background()); !errors.Is(err, ErrInvalidPayload) {
					t.Fatalf("error=%v, want ErrInvalidPayload", err)
				}
			})
		}
	}
}

func runStreamContract(t *testing.T, from, to Protocol, input []streamFrame) ([]streamFrame, error) {
	t.Helper()
	harness, err := newTestRouterHarness()
	if err != nil {
		return nil, err
	}
	plan, err := harness.catalog().Plan(from, to)
	if err != nil {
		return nil, err
	}
	stream, err := harness.NewResponseStream(context.Background(), plan, conversionOptions{Exchange: exchangeMetadata{ClientModel: "public", UpstreamModel: "provider"}})
	if err != nil {
		return nil, err
	}
	var output []streamFrame
	for _, frame := range input {
		frames, _, convertErr := stream.Convert(context.Background(), frame)
		output = append(output, frames...)
		if convertErr != nil {
			return output, convertErr
		}
	}
	frames, _, err := stream.Finalize(context.Background())
	return append(output, frames...), err
}

func truncatedStream(protocol Protocol) []streamFrame {
	switch protocol {
	case ProtocolChat:
		return []streamFrame{{Data: []byte(`{"id":"chat_1","model":"provider","choices":[{"index":0,"delta":{"content":"partial"}}]}`)}}
	case ProtocolResponses:
		return []streamFrame{{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","delta":"partial"}`)}}
	case ProtocolMessages:
		return []streamFrame{{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","model":"provider","usage":{"input_tokens":1}}}`)}}
	case ProtocolGenerateContent:
		return []streamFrame{{Data: []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`)}}
	default:
		return nil
	}
}

func protocolErrorFrame(protocol Protocol) streamFrame {
	switch protocol {
	case ProtocolChat:
		return streamFrame{Data: []byte(`{"error":{"message":"boom"}}`)}
	case ProtocolResponses:
		return streamFrame{Event: "error", Data: []byte(`{"type":"error","message":"boom"}`)}
	case ProtocolMessages:
		return streamFrame{Event: "error", Data: []byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`)}
	case ProtocolGenerateContent:
		return streamFrame{Data: []byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`)}
	default:
		return streamFrame{}
	}
}

func readRelayFixture(t *testing.T, protocol Protocol, kind string) []byte {
	t.Helper()
	name := string(protocol)
	if protocol == ProtocolGenerateContent {
		name = "generateContent"
	}
	data, err := os.ReadFile("../../testdata/relay/" + name + "_" + kind + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}
