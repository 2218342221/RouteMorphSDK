package conformance

import streamx "github.com/2218342221/RouteMorphSDK/internal/stream"

func collectNativeStreamResponse(protocol Protocol, frames []streamFrame, policy lossPolicy) ([]byte, []Diagnostic, error) {
	return streamx.CollectNativeResponse(protocol, frames, policy)
}

func renderNativeResponseStream(protocol Protocol, body []byte) ([]streamFrame, []Diagnostic, error) {
	return streamx.RenderNativeResponse(protocol, body)
}
