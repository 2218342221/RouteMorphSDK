package stream

import (
	"errors"
	"testing"
)

func TestBufferCopiesInputAndFinalizesOnce(t *testing.T) {
	buffer := NewBuffer(frameStorageOverheadBytes + 8)
	data := []byte("payload")
	if err := buffer.Add(Frame{Event: "e", Data: data, Done: true}); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	frames, err := buffer.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || string(frames[0].Data) != "payload" || !frames[0].Done {
		t.Fatalf("frames = %#v", frames)
	}
	frames[0].Data[0] = 'Y'
	if _, err := buffer.Finalize(); !errors.Is(err, ErrFinalized) {
		t.Fatalf("second finalize error = %v", err)
	}
	if err := buffer.Add(Frame{}); !errors.Is(err, ErrFinalized) {
		t.Fatalf("add after finalize error = %v", err)
	}
}

func TestBufferEnforcesCombinedEventAndDataLimit(t *testing.T) {
	buffer := NewBuffer(frameStorageOverheadBytes + 8)
	if err := buffer.Add(Frame{Event: "x", Data: []byte("1234567")}); err != nil {
		t.Fatal(err)
	}
	if err := buffer.Add(Frame{Data: []byte("x")}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("limit error = %v", err)
	}
	frames, err := buffer.Finalize()
	if err != nil || len(frames) != 1 {
		t.Fatalf("frames=%#v error=%v", frames, err)
	}
}

func TestBufferChargesEmptyFramesAgainstLimit(t *testing.T) {
	buffer := NewBuffer(2 * frameStorageOverheadBytes)
	if err := buffer.Add(Frame{}); err != nil {
		t.Fatal(err)
	}
	if err := buffer.Add(Frame{}); err != nil {
		t.Fatal(err)
	}
	if err := buffer.Add(Frame{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("third empty frame error = %v, want ErrLimitExceeded", err)
	}
}

func TestBufferRejectsNegativeLimit(t *testing.T) {
	if err := NewBuffer(-1).Add(Frame{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("error = %v", err)
	}
}
