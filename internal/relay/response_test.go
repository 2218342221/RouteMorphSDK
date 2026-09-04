package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestLimitedSSEBodyPreservesBytesAcrossReadSizes(t *testing.T) {
	t.Parallel()
	payload := []byte(": comment\r\nevent: update\r\ndata: first\r\ndata: second\r\n\r\ndata: tail\n\n")
	for _, size := range []int{1, 2, 7, len(payload)} {
		size := size
		t.Run(string(rune('a'+size%26)), func(t *testing.T) {
			t.Parallel()
			body := newLimitedSSEBody(io.NopCloser(bytes.NewReader(payload)), core.ProtocolResponses, int64(len(payload)))
			got, err := readWithBuffer(body, size)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("body changed:\n got %q\nwant %q", got, payload)
			}
		})
	}
}

func TestLimitedSSEBodyResetsAtEveryBlankLineStyle(t *testing.T) {
	t.Parallel()
	for _, payload := range [][]byte{
		[]byte("data: 123\n\ndata: 456\n\n"),
		[]byte("data: 123\r\rdata: 456\r\r"),
		[]byte("data: 123\r\n\r\ndata: 456\r\n\r\n"),
		[]byte("data: 123\r\n\ndata: 456\n\r\n"),
	} {
		body := newLimitedSSEBody(io.NopCloser(bytes.NewReader(payload)), core.ProtocolResponses, 13)
		got, err := readWithBuffer(body, 1)
		if err != nil {
			t.Fatalf("payload %q: %v", payload, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("body changed: got %q, want %q", got, payload)
		}
	}
}

func TestLimitedSSEBodyAcceptsExactLimitAndRejectsNextByte(t *testing.T) {
	t.Parallel()
	const exact = "data: x\n\n"
	body := newLimitedSSEBody(io.NopCloser(bytes.NewReader([]byte(exact))), core.ProtocolResponses, int64(len(exact)))
	if got, err := readWithBuffer(body, 2); err != nil || string(got) != exact {
		t.Fatalf("exact limit: body=%q error=%v", got, err)
	}

	body = newLimitedSSEBody(io.NopCloser(bytes.NewReader([]byte(exact))), core.ProtocolResponses, int64(len(exact)-1))
	got, err := readWithBuffer(body, 2)
	if !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("oversized error=%v, want ErrUpstreamResponse (body=%q)", err, got)
	}
	var conversion *core.ConversionError
	if !errors.As(err, &conversion) || conversion.Protocol != core.ProtocolResponses {
		t.Fatalf("oversized error=%#v, want Responses ConversionError", err)
	}
	buffer := make([]byte, 1)
	if read, repeated := body.Read(buffer); read != 0 || repeated != err {
		t.Fatalf("sticky read=(%d, %v), want (0, %v)", read, repeated, err)
	}
}

func TestLimitedSSEBodyCloseDelegates(t *testing.T) {
	t.Parallel()
	underlying := &trackedReadCloser{Reader: bytes.NewReader(nil)}
	body := newLimitedSSEBody(underlying, core.ProtocolChat, 16)
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if !underlying.closed {
		t.Fatal("underlying body was not closed")
	}
}

func TestValidatingSSEBodyPreservesValidatedBytes(t *testing.T) {
	t.Parallel()
	payload := []byte(": keepalive\r\nevent: response.completed\r\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\r\n\r\n")
	for _, size := range []int{1, 7, len(payload)} {
		body := newValidatingSSEBody(context.Background(), io.NopCloser(bytes.NewReader(payload)), core.ProtocolResponses, codec.New(core.ProtocolResponses), int64(len(payload)))
		got, err := readWithBuffer(body, size)
		if err != nil {
			t.Fatalf("read size %d: %v", size, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("read size %d changed body:\n got %q\nwant %q", size, got, payload)
		}
	}
}

func TestValidatingSSEBodySupportsEverySSELineEnding(t *testing.T) {
	t.Parallel()
	for _, separator := range []string{"\n", "\r", "\r\n"} {
		separator := separator
		t.Run(fmt.Sprintf("%q", separator), func(t *testing.T) {
			t.Parallel()
			payload := []byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}" + separator + separator +
				"data: [DONE]" + separator + separator)
			body := newValidatingSSEBody(context.Background(), io.NopCloser(bytes.NewReader(payload)), core.ProtocolChat, codec.New(core.ProtocolChat), int64(len(payload)))
			got, err := readWithBuffer(body, 3)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("body changed:\n got %q\nwant %q", got, payload)
			}
		})
	}
}

func TestValidatingResponsesAllowsCompatibleLifecycleEvents(t *testing.T) {
	t.Parallel()
	payload := []byte(
		"event: response.queued\ndata: {\"type\":\"response.queued\"}\n\n" +
			"event: response.in_progress\ndata: {\"type\":\"response.in_progress\"}\n\n" +
			"event: response.refusal.done\ndata: {\"type\":\"response.refusal.done\",\"item_id\":\"msg_1\",\"content_index\":0,\"refusal\":\"no\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n",
	)
	body := newValidatingSSEBody(context.Background(), io.NopCloser(bytes.NewReader(payload)), core.ProtocolResponses, codec.New(core.ProtocolResponses), int64(len(payload)))
	got, err := readWithBuffer(body, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("body changed:\n got %q\nwant %q", got, payload)
	}
}

func TestValidatingSSEBodyAllowsNativeProtocolExtensions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		protocol core.Protocol
		payload  string
	}{
		{
			protocol: core.ProtocolChat,
			payload:  "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"future_reason\"}]}\n\ndata: [DONE]\n\n",
		},
		{
			protocol: core.ProtocolMessages,
			payload: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"future_reason\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		},
		{
			protocol: core.ProtocolGenerateContent,
			payload:  "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[]},\"finishReason\":\"FUTURE_REASON\"}]}\n\n",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(string(test.protocol), func(t *testing.T) {
			t.Parallel()
			body := newValidatingSSEBody(context.Background(), io.NopCloser(strings.NewReader(test.payload)), test.protocol, codec.New(test.protocol), int64(len(test.payload)))
			got, err := readWithBuffer(body, 7)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.payload {
				t.Fatalf("body changed:\n got %q\nwant %q", got, test.payload)
			}
		})
	}
}

func TestValidatingGeminiAllowsUnspecifiedPromptFeedback(t *testing.T) {
	t.Parallel()
	payload := []byte(
		"data: {\"promptFeedback\":{\"blockReason\":\"BLOCK_REASON_UNSPECIFIED\"},\"usageMetadata\":{\"promptTokenCount\":1}}\n\n" +
			"data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[]},\"finishReason\":\"STOP\"}]}\n\n",
	)
	body := newValidatingSSEBody(context.Background(), io.NopCloser(bytes.NewReader(payload)), core.ProtocolGenerateContent, codec.New(core.ProtocolGenerateContent), int64(len(payload)))
	got, err := readWithBuffer(body, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("body changed:\n got %q\nwant %q", got, payload)
	}
}

func TestValidatingSSEBodyRejectsMissingTerminal(t *testing.T) {
	t.Parallel()
	payload := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\",\"output\":[]}}\n\n")
	body := newValidatingSSEBody(context.Background(), io.NopCloser(bytes.NewReader(payload)), core.ProtocolResponses, codec.New(core.ProtocolResponses), int64(len(payload)))
	got, err := readWithBuffer(body, 11)
	if !bytes.Equal(got, payload) || !errors.Is(err, core.ErrUpstreamResponse) || !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("body=%q error=%v", got, err)
	}
}

func TestValidatingSSEBodyRejectsInvalidNativeLifecycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		protocol core.Protocol
		payload  string
		category error
	}{
		{
			name:     "chat done before finish reason",
			protocol: core.ProtocolChat,
			payload:  "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n",
			category: core.ErrInvalidPayload,
		},
		{
			name:     "chat content after finish",
			protocol: core.ProtocolChat,
			payload: "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"late\"},\"finish_reason\":null}]}\n\n",
			category: core.ErrInvalidPayload,
		},
		{
			name:     "messages stop before terminal delta",
			protocol: core.ProtocolMessages,
			payload: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			category: core.ErrInvalidPayload,
		},
		{
			name:     "messages terminal delta with open block",
			protocol: core.ProtocolMessages,
			payload: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n",
			category: core.ErrInvalidPayload,
		},
		{
			name:     "gemini failure finish reason",
			protocol: core.ProtocolGenerateContent,
			payload:  "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[]},\"finishReason\":\"MALFORMED_FUNCTION_CALL\"}]}\n\n",
			category: core.ErrUpstreamResponse,
		},
		{
			name:     "gemini candidate after finish",
			protocol: core.ProtocolGenerateContent,
			payload: "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[]},\"finishReason\":\"STOP\"}]}\n\n" +
				"data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"late\"}]}}]}\n\n",
			category: core.ErrInvalidPayload,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := newValidatingSSEBody(
				context.Background(),
				io.NopCloser(bytes.NewReader([]byte(test.payload))),
				test.protocol,
				codec.New(test.protocol),
				int64(len(test.payload)),
			)
			_, err := readWithBuffer(body, 13)
			if !errors.Is(err, core.ErrUpstreamResponse) || !errors.Is(err, test.category) {
				t.Fatalf("error=%v, want ErrUpstreamResponse and %v", err, test.category)
			}
		})
	}
}

func TestValidatingSSEBodyRejectsMalformedKnownFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		protocol core.Protocol
		payload  string
	}{
		{
			name:     "chat delta content type",
			protocol: core.ProtocolChat,
			payload:  "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":123},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		},
		{
			name:     "chat null choice fields",
			protocol: core.ProtocolChat,
			payload:  "data: {\"choices\":[{\"index\":null,\"delta\":null,\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		},
		{
			name:     "responses delta type",
			protocol: core.ProtocolResponses,
			payload:  "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":123}\n\n",
		},
		{
			name:     "messages text delta type",
			protocol: core.ProtocolMessages,
			payload: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":123}}\n\n" +
				"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		},
		{
			name:     "messages null text block",
			protocol: core.ProtocolMessages,
			payload: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":null}}\n\n",
		},
		{
			name:     "gemini candidate content type",
			protocol: core.ProtocolGenerateContent,
			payload:  "data: {\"candidates\":[{\"index\":0,\"content\":123,\"finishReason\":\"STOP\"}]}\n\n",
		},
		{
			name:     "gemini null candidate content",
			protocol: core.ProtocolGenerateContent,
			payload:  "data: {\"candidates\":[{\"index\":0,\"content\":null,\"finishReason\":\"STOP\"}]}\n\n",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := newValidatingSSEBody(
				context.Background(),
				io.NopCloser(bytes.NewReader([]byte(test.payload))),
				test.protocol,
				codec.New(test.protocol),
				int64(len(test.payload)),
			)
			_, err := readWithBuffer(body, 17)
			if !errors.Is(err, core.ErrUpstreamResponse) || !errors.Is(err, core.ErrInvalidPayload) {
				t.Fatalf("error=%v, want typed malformed upstream error", err)
			}
		})
	}
}

func readWithBuffer(reader io.Reader, size int) ([]byte, error) {
	var output bytes.Buffer
	buffer := make([]byte, size)
	for {
		read, err := reader.Read(buffer)
		output.Write(buffer[:read])
		if err != nil {
			if errors.Is(err, io.EOF) {
				return output.Bytes(), nil
			}
			return output.Bytes(), err
		}
	}
}

type trackedReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackedReadCloser) Close() error {
	r.closed = true
	return nil
}
