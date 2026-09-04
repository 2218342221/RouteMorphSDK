package relay

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

// Response is a complete relay response. Callers that do not use WriteTo must
// close Body themselves.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
	Trailer    http.Header
	Meta       ResponseMeta
}

// ResponseMeta describes the selected conversion path. Diagnostics returns a
// concurrency-safe snapshot; for streams it becomes complete after Body ends.
type ResponseMeta struct {
	IngressProtocol  core.Protocol
	UpstreamProtocol core.Protocol
	Stream           bool
	RouteMode        core.RouteMode

	diagnostics *diagnosticStore
}

func (m ResponseMeta) Diagnostics() []core.Diagnostic {
	if m.diagnostics == nil {
		return nil
	}
	return m.diagnostics.snapshot()
}

type diagnosticStore struct {
	mu     sync.RWMutex
	values []core.Diagnostic
}

func newDiagnosticStore(values []core.Diagnostic) *diagnosticStore {
	store := &diagnosticStore{}
	store.add(values...)
	return store
}

func (s *diagnosticStore) add(values ...core.Diagnostic) {
	if s == nil || len(values) == 0 {
		return
	}
	s.mu.Lock()
	s.values = append(s.values, values...)
	s.mu.Unlock()
}

func (s *diagnosticStore) snapshot() []core.Diagnostic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]core.Diagnostic(nil), s.values...)
}

func setAdapterMetadataHeaders(header http.Header, meta ResponseMeta) {
	header.Set("X-RouteMorph-Conversion", string(meta.IngressProtocol)+"->"+string(meta.UpstreamProtocol)+"->"+string(meta.IngressProtocol))
	header.Set("X-RouteMorph-Stream-Mode", string(meta.RouteMode))
	header.Set("X-RouteMorph-Diagnostics", fmt.Sprintf("%d", len(meta.Diagnostics())))
}

type limitedSSEBody struct {
	body           io.ReadCloser
	protocol       core.Protocol
	maxFrameBytes  int64
	frameBytes     int64
	lineHasContent bool
	pendingCR      bool
	err            error
}

func newLimitedSSEBody(body io.ReadCloser, protocol core.Protocol, maxFrameBytes int64) io.ReadCloser {
	return &limitedSSEBody{body: body, protocol: protocol, maxFrameBytes: maxFrameBytes}
}

func (b *limitedSSEBody) Read(buffer []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	read, readErr := b.body.Read(buffer)
	for index := 0; index < read; index++ {
		if b.pendingCR {
			if buffer[index] == '\n' {
				if err := b.consumeByte(); err != nil {
					return index, b.remember(err)
				}
				b.finishLine()
				b.pendingCR = false
				continue
			}
			b.finishLine()
			b.pendingCR = false
		}
		if err := b.consumeByte(); err != nil {
			return index, b.remember(err)
		}
		switch buffer[index] {
		case '\r':
			b.pendingCR = true
		case '\n':
			b.finishLine()
		default:
			b.lineHasContent = true
		}
	}
	if readErr != nil {
		if b.pendingCR {
			b.finishLine()
			b.pendingCR = false
		}
		return read, readErr
	}
	return read, nil
}

func (b *limitedSSEBody) consumeByte() error {
	b.frameBytes++
	if b.maxFrameBytes >= 0 && b.frameBytes > b.maxFrameBytes {
		return core.UpstreamResponseError(b.protocol, "$", "SSE frame exceeds %d bytes", b.maxFrameBytes)
	}
	return nil
}

func (b *limitedSSEBody) finishLine() {
	if !b.lineHasContent {
		b.frameBytes = 0
	}
	b.lineHasContent = false
}

func (b *limitedSSEBody) remember(err error) error {
	b.err = err
	return err
}

func (b *limitedSSEBody) Close() error {
	if b.body == nil {
		return errors.New("response body is nil")
	}
	return b.body.Close()
}

type finalizingBody struct {
	body     io.ReadCloser
	finalize func()
	once     sync.Once
}

func newFinalizingBody(body io.ReadCloser, finalize func()) io.ReadCloser {
	return &finalizingBody{body: body, finalize: finalize}
}

func (b *finalizingBody) Read(buffer []byte) (int, error) {
	read, err := b.body.Read(buffer)
	if err != nil {
		b.finish()
	}
	return read, err
}

func (b *finalizingBody) Close() error {
	err := b.body.Close()
	b.finish()
	return err
}

func (b *finalizingBody) finish() {
	b.once.Do(func() {
		if b.finalize != nil {
			b.finalize()
		}
	})
}
