package routemorph_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	routemorph "github.com/2218342221/RouteMorphSDK"
)

type responsesCaller interface {
	OpenAIResponses(context.Context, *routemorph.Request) (*routemorph.Response, error)
}

type adapterAPI interface {
	OpenAIChatCompletions(context.Context, *routemorph.Request) (*routemorph.Response, error)
	OpenAIResponses(context.Context, *routemorph.Request) (*routemorph.Response, error)
	AnthropicMessages(context.Context, *routemorph.Request) (*routemorph.Response, error)
	GeminiGenerateContent(context.Context, *routemorph.Request) (*routemorph.Response, error)
}

var (
	_ adapterAPI = (*routemorph.Adapter)(nil)

	_ func(string, string, ...routemorph.Option) (*routemorph.Adapter, error)                                           = routemorph.NewOpenAIChatCompletionsAdapter
	_ func(string, string, ...routemorph.Option) (*routemorph.Adapter, error)                                           = routemorph.NewOpenAIResponsesAdapter
	_ func(string, string, ...routemorph.Option) (*routemorph.Adapter, error)                                           = routemorph.NewAnthropicMessagesAdapter
	_ func(string, string, ...routemorph.Option) (*routemorph.Adapter, error)                                           = routemorph.NewGeminiGenerateContentAdapter
	_ func(string) routemorph.Option                                                                                    = routemorph.WithModel
	_ func(string) (routemorph.Protocol, error)                                                                         = routemorph.ParseProtocol
	_ func(context.Context, routemorph.Protocol, *url.URL, []byte) (routemorph.RequestInfo, error)                      = routemorph.InspectRequest
	_ func(context.Context, routemorph.Protocol, *url.URL, []byte) (*routemorph.Request, routemorph.RequestInfo, error) = routemorph.PrepareRequest
	_ func(routemorph.Protocol, routemorph.ProtocolError) (routemorph.EncodedError, error)                              = routemorph.EncodeError

	_ interface{ Valid() bool } = routemorph.Protocol("")
	_ interface {
		Diagnostics() []routemorph.Diagnostic
	} = routemorph.ResponseMeta{}
	_ interface {
		WriteTo(http.ResponseWriter) error
	} = (*routemorph.Response)(nil)
	_ interface {
		error
		Unwrap() error
	} = (*routemorph.ConversionError)(nil)
)

func TestConstructorsReturnUsableConcreteAdapter(t *testing.T) {
	adapter, err := routemorph.NewOpenAIResponsesAdapter("https://example.test", "secret", routemorph.WithModel("upstream-model"))
	if err != nil {
		t.Fatal(err)
	}
	var _ responsesCaller = adapter
}

func TestInspectRequestPublicBoundary(t *testing.T) {
	requestURL, err := url.Parse("https://gateway.test/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	info, err := routemorph.InspectRequest(context.Background(), routemorph.ProtocolChat, requestURL, []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "client-model" || !info.Stream {
		t.Fatalf("request info = %+v", info)
	}
}

func TestConversionErrorPublicBoundary(t *testing.T) {
	_, err := routemorph.InspectRequest(context.Background(), routemorph.ProtocolChat, nil, []byte(`{}`))
	if !errors.Is(err, routemorph.ErrInvalidPayload) {
		t.Fatalf("error = %v, want ErrInvalidPayload", err)
	}
	var conversion *routemorph.ConversionError
	if !errors.As(err, &conversion) {
		t.Fatalf("error %T does not expose *routemorph.ConversionError", err)
	}
	if conversion.Protocol != routemorph.ProtocolChat || conversion.Path != "$.model" || !errors.Is(conversion, routemorph.ErrInvalidPayload) {
		t.Fatalf("conversion error = %+v", conversion)
	}
}

func TestPrepareRequestPublicBoundary(t *testing.T) {
	requestURL, err := url.Parse("https://gateway.test/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	request, info, err := routemorph.PrepareRequest(context.Background(), routemorph.ProtocolChat, requestURL, []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.URL != requestURL || request.Body == nil || info.Model != "client-model" || !info.Stream {
		t.Fatalf("request=%#v info=%+v", request, info)
	}
}

func TestPublicInspectionRejectsOversizedBodies(t *testing.T) {
	const maxBodyBytes = 32 << 20
	body := make([]byte, maxBodyBytes+1)
	if _, err := routemorph.InspectRequest(context.Background(), routemorph.ProtocolChat, nil, body); !errors.Is(err, routemorph.ErrInvalidPayload) {
		t.Fatalf("InspectRequest() error = %v, want ErrInvalidPayload", err)
	}
	if _, _, err := routemorph.PrepareRequest(context.Background(), routemorph.ProtocolChat, nil, body); !errors.Is(err, routemorph.ErrInvalidPayload) {
		t.Fatalf("PrepareRequest() error = %v, want ErrInvalidPayload", err)
	}
}

func TestEncodeErrorPublicBoundary(t *testing.T) {
	encoded, err := routemorph.EncodeError(routemorph.ProtocolMessages, routemorph.ProtocolError{
		Type: "invalid_request_error", Message: "invalid", RequestID: "req_1", StatusCode: 400,
	})
	if err != nil {
		t.Fatal(err)
	}
	if encoded.ContentType != "application/json" || len(encoded.Body) == 0 {
		t.Fatalf("encoded error = %+v", encoded)
	}
}

func TestPublicStructContracts(t *testing.T) {
	const packagePath = "github.com/2218342221/RouteMorphSDK"
	for _, typ := range []reflect.Type{
		reflect.TypeOf(routemorph.Protocol("")),
		reflect.TypeOf(routemorph.RouteMode("")),
		reflect.TypeOf(routemorph.Diagnostic{}),
		reflect.TypeOf(routemorph.RequestInfo{}),
		reflect.TypeOf(routemorph.ProtocolError{}),
		reflect.TypeOf(routemorph.EncodedError{}),
		reflect.TypeOf(routemorph.ConversionError{}),
	} {
		if typ.PkgPath() != packagePath {
			t.Errorf("%s is owned by %q, want %q", typ, typ.PkgPath(), packagePath)
		}
	}
	assertFields(t, reflect.TypeOf(routemorph.Request{}), map[string]reflect.Type{
		"Header": reflect.TypeOf(http.Header{}),
		"URL":    reflect.TypeOf((*url.URL)(nil)),
		"Body":   reflect.TypeOf((*io.Reader)(nil)).Elem(),
	})
	assertFields(t, reflect.TypeOf(routemorph.Response{}), map[string]reflect.Type{
		"StatusCode": reflect.TypeOf(int(0)),
		"Header":     reflect.TypeOf(http.Header{}),
		"Body":       reflect.TypeOf((*io.ReadCloser)(nil)).Elem(),
		"Trailer":    reflect.TypeOf(http.Header{}),
		"Meta":       reflect.TypeOf(routemorph.ResponseMeta{}),
	})
	assertFields(t, reflect.TypeOf(routemorph.ResponseMeta{}), map[string]reflect.Type{
		"IngressProtocol":  reflect.TypeOf(routemorph.ProtocolChat),
		"UpstreamProtocol": reflect.TypeOf(routemorph.ProtocolChat),
		"Stream":           reflect.TypeOf(false),
		"RouteMode":        reflect.TypeOf(routemorph.RouteModeNative),
	})
}

func assertFields(t *testing.T, typ reflect.Type, want map[string]reflect.Type) {
	t.Helper()
	got := make(map[string]reflect.Type)
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.IsExported() {
			got[field.Name] = field.Type
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s fields = %#v, want %#v", typ, got, want)
	}
}
