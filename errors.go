package routemorph

import (
	"errors"
	"strings"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

var (
	// ErrInvalidPayload marks malformed or semantically invalid protocol data.
	ErrInvalidPayload = core.ErrInvalidPayload
	// ErrUnsupported marks a cross-protocol feature that cannot be represented.
	ErrUnsupported = core.ErrUnsupported
	// ErrUpstreamResponse marks an invalid response received from the upstream.
	ErrUpstreamResponse = core.ErrUpstreamResponse
	// ErrRouteNotFound marks an unsupported protocol conversion pair.
	ErrRouteNotFound = core.ErrRouteNotFound
	// ErrInvalidPlan marks an internally inconsistent conversion plan.
	ErrInvalidPlan = core.ErrInvalidPlan
)

// ProtocolError is the protocol-neutral description encoded into a provider's
// native error envelope.
type ProtocolError struct {
	Type       string
	Code       string
	Message    string
	RequestID  string
	StatusCode int
}

// EncodedError contains a provider-native error envelope and its media type.
type EncodedError struct {
	ContentType string
	Body        []byte
}

// ConversionError identifies the protocol field and reason for a conversion
// failure. Kind can be inspected with errors.Is.
type ConversionError struct {
	Protocol Protocol
	Path     string
	Reason   string
	Kind     error
}

// Error returns a stable protocol, path, and reason description.
func (e *ConversionError) Error() string {
	parts := []string{string(e.Protocol)}
	if e.Path != "" {
		parts = append(parts, e.Path)
	}
	if e.Reason != "" {
		parts = append(parts, e.Reason)
	}
	return strings.Join(parts, ": ")
}

// Unwrap exposes the conversion error category for errors.Is.
func (e *ConversionError) Unwrap() error { return e.Kind }

// boundaryError preserves the original error text and chain while exposing a
// public-package ConversionError through errors.As.
type boundaryError struct {
	err        error
	conversion *ConversionError
}

func (e *boundaryError) Error() string { return e.err.Error() }
func (e *boundaryError) Unwrap() error { return e.err }

func (e *boundaryError) As(target any) bool {
	conversion, ok := target.(**ConversionError)
	if !ok {
		return false
	}
	*conversion = e.conversion
	return true
}

func toPublicError(err error) error {
	if err == nil {
		return nil
	}
	var publicError *ConversionError
	if errors.As(err, &publicError) {
		return err
	}
	var internalError *core.ConversionError
	if !errors.As(err, &internalError) {
		return err
	}
	return &boundaryError{
		err: err,
		conversion: &ConversionError{
			Protocol: Protocol(internalError.Protocol),
			Path:     internalError.Path,
			Reason:   internalError.Reason,
			Kind:     internalError.Kind,
		},
	}
}
