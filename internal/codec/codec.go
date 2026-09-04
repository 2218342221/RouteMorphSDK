// Package codec owns protocol-envelope validation, request patching, error
// encoding, and the shared SSE framing contract.
package codec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
	jsonx "github.com/2218342221/RouteMorphSDK/internal/jsonx"
)

// requestHint carries request metadata encoded in the HTTP endpoint rather
// than in the JSON document. Gemini is the primary example.
type requestHint = core.RequestHint
type requestPatch = core.RequestPatch
type Protocol = core.Protocol
type RequestInfo = core.RequestInfo
type ProtocolError = core.ProtocolError
type EncodedError = core.EncodedError
type streamOptions = core.StreamOptions
type streamFrame = core.Frame
type streamDecoder = core.StreamDecoder
type streamEncoder = core.StreamEncoder

const (
	ProtocolChat            = core.ProtocolChat
	ProtocolResponses       = core.ProtocolResponses
	ProtocolMessages        = core.ProtocolMessages
	ProtocolGenerateContent = core.ProtocolGenerateContent
)

var ErrInvalidPayload = core.ErrInvalidPayload

func invalid(protocol Protocol, path, format string, args ...any) error {
	return core.Invalid(protocol, path, format, args...)
}

func upstreamResponseError(protocol Protocol, path, format string, args ...any) error {
	return core.UpstreamResponseError(protocol, path, format, args...)
}

func marshal(protocol Protocol, value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, invalid(protocol, "$", "cannot encode JSON: %v", err)
	}
	return data, nil
}

// Codec owns the wire-level rules shared by the built-in protocols.
// Cross-protocol semantic conversion stays in the directed route.
type Codec struct {
	protocol Protocol
}

func New(protocol Protocol) *Codec {
	return &Codec{protocol: protocol}
}

func For(protocol Protocol) (*Codec, error) {
	if !protocol.Valid() {
		return nil, fmt.Errorf("%w: protocol %q", core.ErrRouteNotFound, protocol)
	}
	return New(protocol), nil
}

func (a *Codec) Protocol() Protocol { return a.protocol }

func (a *Codec) InspectRequest(_ context.Context, body []byte, hint requestHint) (RequestInfo, error) {
	object, err := rawObject(a.protocol, body)
	if err != nil {
		return RequestInfo{}, err
	}
	return a.inspectRequestObject(object, hint)
}

func (a *Codec) inspectRequestObject(object map[string]json.RawMessage, hint requestHint) (RequestInfo, error) {
	metadata := RequestInfo{Model: hint.Model}
	if hint.Stream != nil {
		metadata.Stream = *hint.Stream
	}
	if a.protocol != ProtocolGenerateContent {
		if raw := object["model"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &metadata.Model); err != nil {
				return RequestInfo{}, invalid(a.protocol, "$.model", "must be a string")
			}
		}
		if raw := object["stream"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &metadata.Stream); err != nil {
				return RequestInfo{}, invalid(a.protocol, "$.stream", "must be a boolean")
			}
		}
	}
	if metadata.Model == "" {
		return RequestInfo{}, invalid(a.protocol, "$.model", "model is required")
	}
	return metadata, nil
}

func (a *Codec) PatchRequest(_ context.Context, body []byte, patch requestPatch) ([]byte, error) {
	object, err := rawObject(a.protocol, body)
	if err != nil {
		return nil, err
	}
	if a.protocol != ProtocolGenerateContent {
		if patch.Model != nil {
			object["model"], _ = json.Marshal(*patch.Model)
		}
		if patch.Stream != nil {
			object["stream"], _ = json.Marshal(*patch.Stream)
		}
	}
	return marshal(a.protocol, object)
}

func (a *Codec) ValidateRequest(_ context.Context, body []byte, hint requestHint) error {
	object, err := rawObject(a.protocol, body)
	if err != nil {
		return err
	}
	if _, err := a.inspectRequestObject(object, hint); err != nil {
		return err
	}
	return a.validateRequestObject(object)
}

func (a *Codec) validateRequestObject(object map[string]json.RawMessage) error {
	switch a.protocol {
	case ProtocolChat:
		return requireJSONArray(a.protocol, object, "messages")
	case ProtocolResponses:
		if len(object["input"]) == 0 || bytes.Equal(bytes.TrimSpace(object["input"]), []byte("null")) {
			return invalid(a.protocol, "$.input", "input is required")
		}
	case ProtocolMessages:
		if err := requireJSONArray(a.protocol, object, "messages"); err != nil {
			return err
		}
		var maxTokens int
		if err := json.Unmarshal(object["max_tokens"], &maxTokens); err != nil || maxTokens < 0 {
			return invalid(a.protocol, "$.max_tokens", "must be a non-negative integer")
		}
	case ProtocolGenerateContent:
		return requireJSONArray(a.protocol, object, "contents")
	default:
		return invalid(a.protocol, "$", "unknown protocol")
	}
	return nil
}

func (a *Codec) ValidateResponse(_ context.Context, body []byte) error {
	object, err := rawObject(a.protocol, body)
	if err != nil {
		return err
	}
	if raw := bytes.TrimSpace(object["error"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		return upstreamResponseError(a.protocol, "$.error", "successful HTTP response contains a protocol error")
	}
	switch a.protocol {
	case ProtocolChat:
		return validateJSONArrayField(a.protocol, object, "choices")
	case ProtocolResponses:
		return validateJSONArrayField(a.protocol, object, "output")
	case ProtocolMessages:
		return validateJSONArrayField(a.protocol, object, "content")
	case ProtocolGenerateContent:
		if len(object["candidates"]) > 0 {
			return validateJSONArrayField(a.protocol, object, "candidates")
		}
		if len(object["promptFeedback"]) == 0 {
			return invalid(a.protocol, "$", "response requires candidates or promptFeedback")
		}
		return nil
	default:
		return invalid(a.protocol, "$", "unknown protocol")
	}
}

func (a *Codec) NewStreamDecoder(reader io.Reader, options streamOptions) (streamDecoder, error) {
	if reader == nil {
		return nil, invalid(a.protocol, "$", "nil stream reader")
	}
	return newSSEDecoder(reader, options), nil
}

func (a *Codec) NewStreamEncoder(writer io.Writer, options streamOptions) (streamEncoder, error) {
	if writer == nil {
		return nil, invalid(a.protocol, "$", "nil stream writer")
	}
	limit := options.MaxFrameBytes
	if limit <= 0 {
		limit = 4 << 20
	}
	return &sseEncoder{writer: writer, flusher: options.Flusher, maxFrameBytes: limit}, nil
}

func (a *Codec) EncodeError(value ProtocolError) (EncodedError, error) {
	var envelope any
	switch a.protocol {
	case ProtocolMessages:
		envelope = map[string]any{"type": "error", "error": map[string]any{"type": value.Type, "message": value.Message}, "request_id": value.RequestID}
	case ProtocolGenerateContent:
		envelope = map[string]any{"error": map[string]any{"code": value.StatusCode, "status": googleErrorStatus(value.StatusCode), "message": value.Message}}
	default:
		code := value.Code
		if code == "" {
			code = value.Type
		}
		envelope = map[string]any{"error": map[string]any{"type": value.Type, "message": value.Message, "param": nil, "code": code}}
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return EncodedError{}, err
	}
	return EncodedError{ContentType: "application/json", Body: body}, nil
}

func (a *Codec) EncodeStreamError(value ProtocolError) (streamFrame, error) {
	if a.protocol == ProtocolResponses {
		body, err := json.Marshal(map[string]any{
			"type": "error", "code": value.Code, "message": value.Message,
			"param": nil, "sequence_number": 0,
		})
		return streamFrame{Event: "error", Data: body}, err
	}
	encoded, err := a.EncodeError(value)
	if err != nil {
		return streamFrame{}, err
	}
	event := ""
	if a.protocol == ProtocolMessages {
		event = "error"
	}
	return streamFrame{Event: event, Data: encoded.Body}, nil
}

func rawObject(protocol Protocol, body []byte) (map[string]json.RawMessage, error) {
	object, err := jsonx.Object(body)
	if err != nil {
		if errors.Is(err, jsonx.ErrObjectRequired) {
			return nil, invalid(protocol, "$", "JSON object is required")
		}
		return nil, invalid(protocol, "$", "%v", err)
	}
	return object, nil
}

func requireJSONArray(protocol Protocol, object map[string]json.RawMessage, field string) error {
	raw := object[field]
	if len(raw) == 0 {
		return invalid(protocol, "$."+field, "%s is required", field)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 {
		return invalid(protocol, "$."+field, "must be a non-empty array")
	}
	return nil
}

func validateJSONArrayField(protocol Protocol, object map[string]json.RawMessage, field string) error {
	raw := object[field]
	if len(raw) == 0 {
		return invalid(protocol, "$."+field, "%s is required", field)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return invalid(protocol, "$."+field, "must be an array")
	}
	return nil
}

func googleErrorStatus(status int) string {
	switch status {
	case 400:
		return "INVALID_ARGUMENT"
	case 401, 403:
		return "PERMISSION_DENIED"
	case 404:
		return "NOT_FOUND"
	case 429:
		return "RESOURCE_EXHAUSTED"
	case 504:
		return "DEADLINE_EXCEEDED"
	default:
		return "INTERNAL"
	}
}
