// Package stream contains protocol-neutral response-stream lifecycle helpers.
package stream

import (
	"errors"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

var (
	ErrFinalized     = errors.New("stream buffer already finalized")
	ErrLimitExceeded = errors.New("stream buffer byte limit exceeded")
)

// frameStorageOverheadBytes conservatively accounts for the Frame value,
// slice growth, and framing metadata even when Event and Data are empty. This
// keeps the aggregate memory bound effective for streams made of empty frames.
const frameStorageOverheadBytes int64 = 64

// Frame is an opaque SSE frame. Buffer does not interpret Event or Data.
type Frame = core.Frame

// Buffer owns bounded frame storage and its single-use finalize transition.
// It copies frame data so callers may safely reuse their input buffers.
type Buffer struct {
	frames    []Frame
	bytes     int64
	maxBytes  int64
	finalized bool
}

func NewBuffer(maxBytes int64) *Buffer {
	return &Buffer{maxBytes: maxBytes}
}

func (b *Buffer) Add(frame Frame) error {
	if b.finalized {
		return ErrFinalized
	}
	frameBytes := frameStorageOverheadBytes + int64(len(frame.Event)) + int64(len(frame.Data))
	if b.maxBytes < 0 || frameBytes > b.maxBytes-b.bytes {
		return ErrLimitExceeded
	}
	frame.Data = append([]byte(nil), frame.Data...)
	b.frames = append(b.frames, frame)
	b.bytes += frameBytes
	return nil
}

// Finalize returns a detached snapshot. It may be called exactly once.
func (b *Buffer) Finalize() ([]Frame, error) {
	if b.finalized {
		return nil, ErrFinalized
	}
	b.finalized = true
	frames := make([]Frame, len(b.frames))
	for index, frame := range b.frames {
		frames[index] = frame
		frames[index].Data = append([]byte(nil), frame.Data...)
	}
	return frames, nil
}
