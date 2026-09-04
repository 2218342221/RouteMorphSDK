package routemorph

import (
	"crypto/sha256"
	"io"
	"net/http"
	"net/url"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
	relayx "github.com/2218342221/RouteMorphSDK/internal/relay"
)

// Request is a protocol-native HTTP request. URL describes the client-facing
// endpoint and is required for Gemini, whose model and stream mode live in the
// path. The adapter never mutates Header or URL and never closes Body.
//
// Request is owned by the public package so changes to the internal relay
// representation cannot silently change the SDK contract.
type Request struct {
	Header http.Header
	URL    *url.URL
	Body   io.Reader

	prepared *preparedRequest
}

type preparedRequest struct {
	protocol                  Protocol
	escapedPath               string
	bodyDigest                [sha256.Size]byte
	info                      RequestInfo
	chatStreamIncludeUsage    bool
	chatStreamIncludeUsageSet bool
}

func toRelayRequest(request *Request) *relayx.Request {
	if request == nil {
		return nil
	}
	converted := &relayx.Request{Header: request.Header, URL: request.URL, Body: request.Body}
	if request.prepared != nil {
		converted.Prepared = &relayx.PreparedRequest{
			Protocol:                  toCoreProtocol(request.prepared.protocol),
			EscapedPath:               request.prepared.escapedPath,
			BodyDigest:                request.prepared.bodyDigest,
			Info:                      core.RequestInfo{Model: request.prepared.info.Model, Stream: request.prepared.info.Stream},
			ChatStreamIncludeUsage:    request.prepared.chatStreamIncludeUsage,
			ChatStreamIncludeUsageSet: request.prepared.chatStreamIncludeUsageSet,
		}
	}
	return converted
}
