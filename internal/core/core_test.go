package core

import (
	"errors"
	"strings"
	"testing"
)

func TestProtocolParsingAndValidation(t *testing.T) {
	tests := map[string]Protocol{
		" chat ": ProtocolChat, "chatcompletion": ProtocolChat, "chatcompletions": ProtocolChat, "openai_chat": ProtocolChat,
		"response": ProtocolResponses, "responses": ProtocolResponses, "openai_response": ProtocolResponses, "openai_responses": ProtocolResponses,
		"message": ProtocolMessages, "messages": ProtocolMessages, "anthropic": ProtocolMessages, "anthropic_messages": ProtocolMessages,
		"generateContent": ProtocolGenerateContent, "generate_content": ProtocolGenerateContent, "gemini": ProtocolGenerateContent,
	}
	for input, want := range tests {
		got, err := ParseProtocol(input)
		if err != nil || got != want || !got.Valid() {
			t.Fatalf("ParseProtocol(%q)=%q,%v; want %q", input, got, err, want)
		}
	}
	if got, err := ParseProtocol("unknown"); err == nil || got != "" {
		t.Fatalf("ParseProtocol(unknown)=%q,%v", got, err)
	}
	if Protocol("unknown").Valid() {
		t.Fatal("unknown protocol is valid")
	}
}

func TestConversionErrorKindsAndFormatting(t *testing.T) {
	tests := []struct {
		err  error
		kind error
		text string
	}{
		{Invalid(ProtocolChat, "$.model", "bad %s", "value"), ErrInvalidPayload, "chat: $.model: bad value"},
		{Unsupported(ProtocolMessages, "", "feature"), ErrUnsupported, "messages: feature"},
		{UpstreamResponseError(ProtocolResponses, "$.error", "failed"), ErrUpstreamResponse, "responses: $.error: failed"},
	}
	for _, test := range tests {
		if !errors.Is(test.err, test.kind) || test.err.Error() != test.text {
			t.Fatalf("error=%q kind=%v", test.err, test.kind)
		}
	}
	var empty *ConversionError = &ConversionError{}
	if strings.TrimSpace(empty.Error()) != "" || empty.Unwrap() != nil {
		t.Fatalf("empty error=%q unwrap=%v", empty.Error(), empty.Unwrap())
	}
}
