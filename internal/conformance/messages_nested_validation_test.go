package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMessagesOutputConfigNestedFieldsFailClosedAcrossTargets(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
		want error
		path string
	}{
		{
			name: "unknown output config field",
			body: `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"output_config":{"future":true}}`,
			want: ErrUnsupported,
			path: "$.output_config.future",
		},
		{
			name: "unknown format field",
			body: `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"output_config":{"format":{"type":"json_schema","schema":{"type":"object"},"future":true}}}`,
			want: ErrUnsupported,
			path: "$.output_config.format.future",
		},
		{
			name: "null output config",
			body: `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"output_config":null}`,
			want: ErrInvalidPayload,
			path: "$.output_config",
		},
		{
			name: "unknown thinking field",
			body: `{"model":"m","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1024,"future":true}}`,
			want: ErrUnsupported,
			path: "$.thinking.future",
		},
		{
			name: "null thinking",
			body: `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"thinking":null}`,
			want: ErrInvalidPayload,
			path: "$.thinking",
		},
	}
	for _, test := range tests {
		for _, target := range []Protocol{ProtocolChat, ProtocolResponses, ProtocolGenerateContent} {
			t.Run(test.name+"/"+string(target), func(t *testing.T) {
				_, err := harness.ToUpstreamRequest(context.Background(), ProtocolMessages, target, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
				if !errors.Is(err, test.want) || !strings.Contains(err.Error(), test.path) {
					t.Fatalf("error = %v, want %v at %s", err, test.want, test.path)
				}
			})
		}
	}
}

func TestOpenAIStructuredOutputNestedFieldsFailClosedAcrossTargets(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		from    Protocol
		targets []Protocol
		body    string
		path    string
	}{
		{
			name:    "Chat response format extension",
			from:    ProtocolChat,
			targets: []Protocol{ProtocolResponses, ProtocolMessages, ProtocolGenerateContent},
			body:    `{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object"},"future":true}}}`,
			path:    "$.response_format.json_schema.future",
		},
		{
			name:    "Responses text extension",
			from:    ProtocolResponses,
			targets: []Protocol{ProtocolChat, ProtocolMessages, ProtocolGenerateContent},
			body:    `{"model":"m","input":"hi","text":{"format":{"type":"json_schema","name":"x","schema":{"type":"object"},"future":true}}}`,
			path:    "$.text.format.future",
		},
	}
	for _, test := range tests {
		for _, target := range test.targets {
			t.Run(test.name+"/"+string(target), func(t *testing.T) {
				_, err := harness.ToUpstreamRequest(context.Background(), test.from, target, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
				if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
					t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.path)
				}
			})
		}
	}
}

func TestKnownMultimodalUnionFieldsFailClosedAcrossTargets(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		from    Protocol
		targets []Protocol
		body    string
		want    error
		path    string
	}{
		{
			name:    "Chat hybrid image and audio",
			from:    ProtocolChat,
			targets: []Protocol{ProtocolResponses, ProtocolMessages, ProtocolGenerateContent},
			body:    `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a.png"},"input_audio":{"data":"YQ==","format":"wav"}}]}]}`,
			want:    ErrInvalidPayload,
			path:    "$.messages[0].content[0].input_audio",
		},
		{
			name:    "Chat image extension",
			from:    ProtocolChat,
			targets: []Protocol{ProtocolResponses, ProtocolMessages, ProtocolGenerateContent},
			body:    `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a.png","future":true}}]}]}`,
			want:    ErrUnsupported,
			path:    "$.messages[0].content[0].image_url.future",
		},
		{
			name:    "Messages hybrid image and text",
			from:    ProtocolMessages,
			targets: []Protocol{ProtocolChat, ProtocolResponses, ProtocolGenerateContent},
			body:    `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YQ=="},"text":""}]}]}`,
			want:    ErrInvalidPayload,
			path:    "$.messages[0].content[0].text",
		},
		{
			name:    "Messages hybrid media source",
			from:    ProtocolMessages,
			targets: []Protocol{ProtocolChat, ProtocolResponses, ProtocolGenerateContent},
			body:    `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YQ==","url":""}}]}]}`,
			want:    ErrInvalidPayload,
			path:    "$.messages[0].content[0].source.url",
		},
		{
			name:    "Messages block extension",
			from:    ProtocolMessages,
			targets: []Protocol{ProtocolChat, ProtocolResponses, ProtocolGenerateContent},
			body:    `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":[{"type":"text","text":"hi","future":true}]}]}`,
			want:    ErrUnsupported,
			path:    "$.messages[0].content[0].future",
		},
	}
	for _, test := range tests {
		for _, target := range test.targets {
			t.Run(test.name+"/"+string(target), func(t *testing.T) {
				_, err := harness.ToUpstreamRequest(context.Background(), test.from, target, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
				if !errors.Is(err, test.want) || !strings.Contains(err.Error(), test.path) {
					t.Fatalf("error = %v, want %v at %s", err, test.want, test.path)
				}
			})
		}
	}
}
