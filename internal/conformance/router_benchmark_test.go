package conformance

import (
	"context"
	"testing"
)

func BenchmarkBuiltinRoutes(b *testing.B) {
	router, err := newBuiltinRouter()
	if err != nil {
		b.Fatal(err)
	}
	requests := map[Protocol][]byte{
		ProtocolChat:            []byte(`{"model":"public","messages":[{"role":"user","content":"hello"}]}`),
		ProtocolResponses:       []byte(`{"model":"public","input":"hello"}`),
		ProtocolMessages:        []byte(`{"model":"public","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
		ProtocolGenerateContent: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
	}
	responses := map[Protocol][]byte{
		ProtocolChat:            []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"provider","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
		ProtocolResponses:       []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"provider","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
		ProtocolMessages:        []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"provider","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`),
		ProtocolGenerateContent: []byte(`{"responseId":"gemini_1","modelVersion":"provider","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`),
	}
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, from := range protocols {
		for _, to := range protocols {
			if from == to {
				continue
			}
			plan, err := router.Plan(from, to)
			if err != nil {
				b.Fatal(err)
			}
			options := conversionOptions{Exchange: exchangeMetadata{ClientModel: "public", UpstreamModel: "provider"}}
			b.Run(string(from)+"_to_"+string(to), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if _, err := router.ConvertRequest(context.Background(), plan, requests[from], options); err != nil {
						b.Fatal(err)
					}
					if _, err := router.ConvertResponse(context.Background(), plan, responses[to], options); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkBuiltinRouteStreams(b *testing.B) {
	router, err := newBuiltinRouter()
	if err != nil {
		b.Fatal(err)
	}
	responses := map[Protocol][]byte{
		ProtocolChat:            []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"provider","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
		ProtocolResponses:       []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"provider","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
		ProtocolMessages:        []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"provider","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`),
		ProtocolGenerateContent: []byte(`{"responseId":"gemini_1","modelVersion":"provider","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`),
	}
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, from := range protocols {
		for _, to := range protocols {
			if from == to {
				continue
			}
			plan, err := router.Plan(from, to)
			if err != nil {
				b.Fatal(err)
			}
			input, _, err := renderNativeResponseStream(to, responses[to])
			if err != nil {
				b.Fatal(err)
			}
			options := conversionOptions{Exchange: exchangeMetadata{ClientModel: "public", UpstreamModel: "provider"}}
			b.Run(string(from)+"_from_"+string(to), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					stream, err := router.NewResponseStream(context.Background(), plan, options)
					if err != nil {
						b.Fatal(err)
					}
					for _, frame := range input {
						if _, _, err := stream.Convert(context.Background(), frame); err != nil {
							b.Fatal(err)
						}
					}
					if _, _, err := stream.Finalize(context.Background()); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
