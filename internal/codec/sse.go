package codec

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

type sseDecoder struct {
	reader        *bufio.Reader
	maxFrameBytes int
	terminalSeen  bool
	finished      bool
}

func newSSEDecoder(reader io.Reader, options streamOptions) *sseDecoder {
	limit := options.MaxFrameBytes
	if limit <= 0 {
		limit = 4 << 20
	}
	return &sseDecoder{reader: bufio.NewReaderSize(reader, 64<<10), maxFrameBytes: limit}
}

func (d *sseDecoder) Next(ctx context.Context) (streamFrame, error) {
	if d.finished {
		return streamFrame{}, io.EOF
	}
	if d.terminalSeen {
		if err := d.drain(ctx); err != nil {
			return streamFrame{}, err
		}
		d.finished = true
		return streamFrame{}, io.EOF
	}
	var event string
	var data strings.Builder
	size := 0
	for {
		if err := ctx.Err(); err != nil {
			return streamFrame{}, err
		}
		lineBytes, err := d.readLine(d.maxFrameBytes - size)
		if err != nil && err != io.EOF {
			return streamFrame{}, err
		}
		size += len(lineBytes)
		line := string(lineBytes)
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" || err == io.EOF {
			if data.Len() > 0 || event != "" {
				payload := strings.TrimSuffix(data.String(), "\n")
				frame := streamFrame{Event: event, Data: []byte(payload), Done: payload == "[DONE]"}
				if frame.Done {
					d.terminalSeen = true
				}
				return frame, nil
			}
			if err == io.EOF {
				d.finished = true
				return streamFrame{}, io.EOF
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			event = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
}

func (d *sseDecoder) drain(ctx context.Context) error {
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := d.reader.Read(buffer)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (d *sseDecoder) readLine(remaining int) ([]byte, error) {
	if remaining <= 0 {
		return nil, fmt.Errorf("%w: SSE frame exceeds %d bytes", ErrInvalidPayload, d.maxFrameBytes)
	}
	line := make([]byte, 0, min(remaining, 4096))
	for {
		fragment, err := d.reader.ReadSlice('\n')
		if len(line)+len(fragment) > remaining {
			return nil, fmt.Errorf("%w: SSE frame exceeds %d bytes", ErrInvalidPayload, d.maxFrameBytes)
		}
		line = append(line, fragment...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err
	}
}

type sseEncoder struct {
	writer        io.Writer
	flusher       interface{ Flush() }
	maxFrameBytes int
}

func (e *sseEncoder) Write(ctx context.Context, frame streamFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.ContainsAny(frame.Event, "\r\n") {
		return fmt.Errorf("%w: SSE event name contains a newline", ErrInvalidPayload)
	}
	if len(frame.Event)+len(frame.Data) > e.maxFrameBytes {
		return fmt.Errorf("%w: SSE frame exceeds %d bytes", ErrInvalidPayload, e.maxFrameBytes)
	}
	if frame.Event != "" {
		if _, err := fmt.Fprintf(e.writer, "event: %s\n", frame.Event); err != nil {
			return err
		}
	}
	data := frame.Data
	if frame.Done && len(data) == 0 {
		data = []byte("[DONE]")
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if _, err := fmt.Fprintf(e.writer, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := io.WriteString(e.writer, "\n")
	return err
}

func (e *sseEncoder) Flush() error {
	if e.flusher != nil {
		e.flusher.Flush()
	}
	return nil
}
