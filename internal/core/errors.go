package core

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidPayload   = errors.New("invalid protocol payload")
	ErrUnsupported      = errors.New("unsupported cross-protocol feature")
	ErrUpstreamResponse = errors.New("upstream protocol response failed")
)

type ConversionError struct {
	Protocol Protocol
	Path     string
	Reason   string
	Kind     error
}

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

func (e *ConversionError) Unwrap() error { return e.Kind }

func Invalid(protocol Protocol, path, format string, args ...any) error {
	return &ConversionError{Protocol: protocol, Path: path, Reason: fmt.Sprintf(format, args...), Kind: ErrInvalidPayload}
}

func Unsupported(protocol Protocol, path, format string, args ...any) error {
	return &ConversionError{Protocol: protocol, Path: path, Reason: fmt.Sprintf(format, args...), Kind: ErrUnsupported}
}

func UpstreamResponseError(protocol Protocol, path, format string, args ...any) error {
	return &ConversionError{Protocol: protocol, Path: path, Reason: fmt.Sprintf(format, args...), Kind: ErrUpstreamResponse}
}
