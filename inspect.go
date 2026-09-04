package routemorph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/url"

	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

// RequestInfo is the deliberately small control-plane view of a request.
// It is not a semantic intermediate representation.
type RequestInfo struct {
	Model  string
	Stream bool
}

// InspectRequest validates a protocol-native request and returns only the
// routing information needed before an Adapter is selected. The request body
// and URL are never mutated.
func InspectRequest(ctx context.Context, protocol Protocol, requestURL *url.URL, body []byte) (RequestInfo, error) {
	if err := validateRequestBodySize(protocol, body); err != nil {
		return RequestInfo{}, err
	}
	info, err := codec.InspectRequest(ctx, toCoreProtocol(protocol), requestURL, body)
	return fromCoreRequestInfo(info), toPublicError(err)
}

// PrepareRequest validates a protocol-native request once and returns both the
// routing metadata and a Request that can be passed to an Adapter. The Adapter
// reuses the validated metadata only while the body and URL path are unchanged;
// otherwise it falls back to full validation.
func PrepareRequest(ctx context.Context, protocol Protocol, requestURL *url.URL, body []byte) (*Request, RequestInfo, error) {
	if err := validateRequestBodySize(protocol, body); err != nil {
		return nil, RequestInfo{}, err
	}
	internalInfo, includeUsage, includeUsageSet, err := codec.InspectRelayRequest(ctx, toCoreProtocol(protocol), requestURL, body)
	if err != nil {
		return nil, RequestInfo{}, toPublicError(err)
	}
	info := fromCoreRequestInfo(internalInfo)
	escapedPath := ""
	if requestURL != nil {
		escapedPath = requestURL.EscapedPath()
	}
	request := &Request{
		URL:  requestURL,
		Body: bytes.NewReader(body),
		prepared: &preparedRequest{
			protocol:                  protocol,
			escapedPath:               escapedPath,
			bodyDigest:                sha256.Sum256(body),
			info:                      info,
			chatStreamIncludeUsage:    includeUsage,
			chatStreamIncludeUsageSet: includeUsageSet,
		},
	}
	return request, info, nil
}

func validateRequestBodySize(protocol Protocol, body []byte) error {
	if int64(len(body)) <= defaultAdapterMaxBodyBytes {
		return nil
	}
	return toPublicError(core.Invalid(toCoreProtocol(protocol), "$", "body exceeds %d bytes", defaultAdapterMaxBodyBytes))
}

// EncodeError encodes an error using the wire envelope of protocol.
func EncodeError(protocol Protocol, value ProtocolError) (EncodedError, error) {
	adapter, err := codec.For(toCoreProtocol(protocol))
	if err != nil {
		return EncodedError{}, toPublicError(err)
	}
	encoded, err := adapter.EncodeError(core.ProtocolError{
		Type:       value.Type,
		Code:       value.Code,
		Message:    value.Message,
		RequestID:  value.RequestID,
		StatusCode: value.StatusCode,
	})
	return EncodedError{ContentType: encoded.ContentType, Body: encoded.Body}, toPublicError(err)
}

func fromCoreRequestInfo(info core.RequestInfo) RequestInfo {
	return RequestInfo{Model: info.Model, Stream: info.Stream}
}
