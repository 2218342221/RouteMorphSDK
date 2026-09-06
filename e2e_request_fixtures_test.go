package routemorph

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"

	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
	streamx "github.com/2218342221/RouteMorphSDK/internal/stream"
)

const (
	liveClientModel       = "routemorph-live-client"
	e2eFixtureRoot        = "testdata/e2e/responses"
	e2eFixtureCatalogPath = e2eFixtureRoot + "/catalog.json"
)

//go:embed testdata/e2e/responses
var e2eFixtureFS embed.FS

var e2eStringPlaceholder = regexp.MustCompile(`\{\{([A-Z][A-Z0-9_]*)\}\}`)

var (
	e2eSecretTokenPattern   = regexp.MustCompile(`(?i)(^|[^a-z0-9])sk-[a-z0-9_-]{6,}`)
	e2eProviderModelPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])(?:gpt|claude|gemini)-[a-z0-9][a-z0-9._-]*`)
)

type e2eRequestFixture struct {
	ID         string   `json:"id"`
	Suite      string   `json:"suite"`
	Capability string   `json:"capability"`
	Protocol   Protocol `json:"protocol"`
	Stream     bool     `json:"stream"`
	Request    string   `json:"request"`
	Marker     string   `json:"marker,omitempty"`
	Assertion  string   `json:"assertion"`
	Dynamic    []string `json:"dynamic,omitempty"`
}

type e2eFixtureCatalog struct {
	Version  int                 `json:"version"`
	Requests []e2eRequestFixture `json:"requests"`
}

type e2eFixtureExpectation struct {
	Suite      string
	Capability string
	Protocol   Protocol
	Stream     bool
	Marker     string
	Assertion  string
	Dynamic    []string
}

func expectedE2EFixtureMatrix() map[string]e2eFixtureExpectation {
	expected := make(map[string]e2eFixtureExpectation, 63)
	add := func(id string, protocol Protocol, stream bool, assertion string, dynamic ...string) {
		if _, exists := expected[id]; exists {
			panic("duplicate expected E2E fixture " + id)
		}
		parts := strings.Split(id, "/")
		expected[id] = e2eFixtureExpectation{
			Suite: parts[0], Capability: parts[1], Protocol: protocol, Stream: stream,
			Marker: expectedE2EFixtureMarker(id), Assertion: assertion, Dynamic: dynamic,
		}
	}
	protocols := []struct {
		slug     string
		protocol Protocol
	}{
		{"chat", ProtocolChat},
		{"responses", ProtocolResponses},
		{"messages", ProtocolMessages},
		{"generate_content", ProtocolGenerateContent},
	}
	for _, capability := range []struct {
		name      string
		assertion string
	}{
		{"text", "text"},
		{"tool_call", "tool_call"},
		{"tool_result", "text"},
		{"structured_output", "structured_output"},
	} {
		for _, protocol := range protocols {
			add("core/"+capability.name+"/"+protocol.slug+"/non_stream", protocol.protocol, false, capability.assertion)
			add("core/"+capability.name+"/"+protocol.slug+"/stream", protocol.protocol, true, capability.assertion)
		}
	}
	for _, protocol := range protocols {
		add("extended/conversation/"+protocol.slug+"/non_stream", protocol.protocol, false, "exact_text")
		add("extended/conversation/"+protocol.slug+"/stream", protocol.protocol, true, "exact_text")
		add("extended/complex_tool_call/"+protocol.slug+"/non_stream", protocol.protocol, false, "complex_tool_call")
		add("extended/parallel_tool_result/"+protocol.slug+"/non_stream", protocol.protocol, false, "exact_text")
	}
	add("extended/metadata_service_tier/chat/non_stream", ProtocolChat, false, "metadata_service_tier")
	add("extended/metadata_service_tier/responses/non_stream", ProtocolResponses, false, "metadata_service_tier")
	add("tools/custom_tool_call/responses_nonstream", ProtocolResponses, false, "custom_tool_call")
	add("tools/custom_tool_call/responses_stream", ProtocolResponses, true, "custom_tool_call")
	add("tools/custom_tool_call/chat_nonstream", ProtocolChat, false, "custom_tool_call")
	add("tools/custom_tool_call_output/responses_nonstream", ProtocolResponses, false, "custom_tool_output")
	add("tools/custom_tool_call_output/responses_stream", ProtocolResponses, true, "custom_tool_output")
	add("tools/custom_tool_call_output/chat_nonstream", ProtocolChat, false, "custom_tool_output")
	add("tools/web_search/responses_nonstream", ProtocolResponses, false, "web_search")
	add("tools/web_search/responses_stream", ProtocolResponses, true, "web_search")
	add("tools/web_search/chat_nonstream", ProtocolChat, false, "web_search")
	add("tools/tool_search/server_responses_nonstream", ProtocolResponses, false, "server_tool_search")
	add("tools/tool_search/server_responses_stream", ProtocolResponses, true, "server_tool_search")
	add("tools/tool_search/client_responses_turn1", ProtocolResponses, false, "client_tool_search_call")
	add("tools/tool_search/client_responses_turn2", ProtocolResponses, false, "client_tool_search_continuation", "CLIENT_TOOL_SEARCH_CALL", "CLIENT_TOOL_SEARCH_CALL_ID")
	return expected
}

func expectedE2EFixtureMarker(id string) string {
	switch id {
	case "tools/custom_tool_call/responses_nonstream":
		return "RM_CUSTOM_NATIVE_NONSTREAM"
	case "tools/custom_tool_call/responses_stream":
		return "RM_CUSTOM_NATIVE_STREAM"
	case "tools/custom_tool_call/chat_nonstream":
		return "RM_CUSTOM_CHAT_NONSTREAM"
	case "tools/custom_tool_call_output/responses_nonstream":
		return "RM_CUSTOM_OUTPUT_RESPONSES_NONSTREAM"
	case "tools/custom_tool_call_output/responses_stream":
		return "RM_CUSTOM_OUTPUT_RESPONSES_STREAM"
	case "tools/custom_tool_call_output/chat_nonstream":
		return "RM_CUSTOM_OUTPUT_CHAT_NONSTREAM"
	case "tools/tool_search/server_responses_nonstream":
		return "RM_TOOL_SEARCH_SERVER_NONSTREAM"
	case "tools/tool_search/server_responses_stream":
		return "RM_TOOL_SEARCH_SERVER_STREAM"
	case "tools/tool_search/client_responses_turn1", "tools/tool_search/client_responses_turn2":
		return "RM_TOOL_SEARCH_CLIENT_CONTINUATION"
	case "tools/web_search/responses_nonstream", "tools/web_search/responses_stream", "tools/web_search/chat_nonstream":
		return ""
	}
	parts := strings.Split(id, "/")
	if len(parts) != 4 {
		panic("invalid expected E2E fixture id " + id)
	}
	protocol, mode := strings.ToUpper(parts[2]), strings.ToUpper(parts[3])
	switch parts[0] + "/" + parts[1] {
	case "core/text":
		return "RM_TEXT_" + strings.ReplaceAll(protocol, "_", "") + "_" + mode
	case "core/tool_call":
		return "RM_TOOL_" + strings.ReplaceAll(protocol, "_", "") + "_" + mode
	case "core/tool_result":
		return "RM_RESULT_" + strings.ReplaceAll(protocol, "_", "") + "_" + mode
	case "core/structured_output":
		return "RM_JSON_" + strings.ReplaceAll(protocol, "_", "") + "_" + mode
	case "extended/conversation":
		return "RM_CONVERSATION_" + protocol + "_" + mode
	case "extended/complex_tool_call":
		return "RM_COMPLEX_TOOL_" + protocol + "_" + mode
	case "extended/parallel_tool_result":
		return "RM_PARALLEL_RESULT_" + protocol + "_" + mode
	case "extended/metadata_service_tier":
		return "RM_METADATA_TIER_" + protocol + "_" + mode
	}
	panic("missing expected E2E fixture marker for " + id)
}

func validateE2EFixtureMatrix(catalog []e2eRequestFixture) error {
	expected := expectedE2EFixtureMatrix()
	if len(catalog) != len(expected) {
		return fmt.Errorf("fixture request count=%d, want %d", len(catalog), len(expected))
	}
	for _, fixture := range catalog {
		want, exists := expected[fixture.ID]
		if !exists {
			return fmt.Errorf("unexpected fixture id %q", fixture.ID)
		}
		if fixture.Suite != want.Suite || fixture.Capability != want.Capability || fixture.Protocol != want.Protocol || fixture.Stream != want.Stream || fixture.Marker != want.Marker || fixture.Assertion != want.Assertion || !equalE2EStrings(fixture.Dynamic, want.Dynamic) {
			return fmt.Errorf("fixture %q metadata suite=%q capability=%q protocol=%q stream=%v marker=%q assertion=%q dynamic=%v, want suite=%q capability=%q protocol=%q stream=%v marker=%q assertion=%q dynamic=%v", fixture.ID, fixture.Suite, fixture.Capability, fixture.Protocol, fixture.Stream, fixture.Marker, fixture.Assertion, fixture.Dynamic, want.Suite, want.Capability, want.Protocol, want.Stream, want.Marker, want.Assertion, want.Dynamic)
		}
		delete(expected, fixture.ID)
	}
	if len(expected) != 0 {
		missing := make([]string, 0, len(expected))
		for id := range expected {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return fmt.Errorf("missing fixture ids: %s", strings.Join(missing, ", "))
	}
	return nil
}

func validateE2EPlaceholderPolicy(fixture e2eRequestFixture) error {
	var want []string
	if fixture.ID == "tools/tool_search/client_responses_turn2" {
		want = []string{"CLIENT_TOOL_SEARCH_CALL", "CLIENT_TOOL_SEARCH_CALL_ID"}
	}
	if !equalE2EStrings(fixture.Dynamic, want) {
		return fmt.Errorf("fixture %q dynamic placeholders=%v, want %v", fixture.ID, fixture.Dynamic, want)
	}
	return nil
}

func equalE2EStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateE2EPlaceholderUsage(want map[string]bool, used map[string]int) error {
	for name := range want {
		if used[name] != 1 {
			return fmt.Errorf("placeholder %q occurrence count=%d, want 1", name, used[name])
		}
	}
	for name, count := range used {
		if !want[name] || count != 1 {
			return fmt.Errorf("unexpected placeholder %q occurrence count=%d", name, count)
		}
	}
	return nil
}

func validateE2ENoResidualPlaceholders(encoded []byte) error {
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return fmt.Errorf("decode rendered request for placeholder scan: %w", err)
	}
	return validateE2ENoResidualPlaceholderValue(value, "$")
}

func validateE2ENoResidualPlaceholderValue(value any, jsonPath string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.Contains(key, "{{") || strings.Contains(key, "}}") || strings.Contains(key, "$live_fixture") {
				return fmt.Errorf("rendered request retains placeholder syntax at %s", jsonPath)
			}
			if err := validateE2ENoResidualPlaceholderValue(child, jsonPath+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := validateE2ENoResidualPlaceholderValue(child, fmt.Sprintf("%s[%d]", jsonPath, index)); err != nil {
				return err
			}
		}
	case string:
		if strings.Contains(typed, "{{") || strings.Contains(typed, "}}") || strings.Contains(typed, "$live_fixture") {
			return fmt.Errorf("rendered request retains placeholder syntax at %s", jsonPath)
		}
	}
	return nil
}

func validateE2ENoCredentialOrProviderMaterial(value any, jsonPath string) error {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := jsonPath + "." + key
			if isE2EForbiddenMaterialKey(key) {
				return fmt.Errorf("forbidden credential/provider field at %s", childPath)
			}
			if err := validateE2ENoCredentialOrProviderMaterial(typed[key], childPath); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := validateE2ENoCredentialOrProviderMaterial(child, fmt.Sprintf("%s[%d]", jsonPath, index)); err != nil {
				return err
			}
		}
	case string:
		lower := strings.ToLower(strings.TrimSpace(typed))
		lower = strings.ReplaceAll(lower, strings.ToLower(liveClientModel), "")
		if strings.Contains(lower, "bearer ") || strings.Contains(lower, "routemorph_live_api_key") || strings.Contains(lower, "routemorph_live_base_url") || e2eSecretTokenPattern.MatchString(lower) || e2eProviderModelPattern.MatchString(lower) {
			return fmt.Errorf("forbidden credential/provider value at %s", jsonPath)
		}
	}
	return nil
}

func isE2EForbiddenMaterialKey(value string) bool {
	lower := strings.ToLower(value)
	var normalized strings.Builder
	for _, character := range lower {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			normalized.WriteRune(character)
		}
	}
	switch normalized.String() {
	case "authorization", "proxyauthorization", "apikey", "xapikey", "xgoogapikey", "baseurl", "providerbaseurl", "upstreambaseurl", "providerendpoint", "endpointurl", "clientsecret", "secretkey", "accesskey", "accesskeyid", "secretaccesskey", "bearertoken":
		return true
	default:
		return false
	}
}

func TestValidateE2EFixtureMatrixRejectsDrift(t *testing.T) {
	expected := expectedE2EFixtureMatrix()
	ids := make([]string, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fixtures := make([]e2eRequestFixture, 0, len(ids))
	for _, id := range ids {
		want := expected[id]
		fixtures = append(fixtures, e2eRequestFixture{
			ID: id, Suite: want.Suite, Capability: want.Capability, Protocol: want.Protocol,
			Stream: want.Stream, Marker: want.Marker, Assertion: want.Assertion,
			Dynamic: append([]string(nil), want.Dynamic...),
		})
	}
	if err := validateE2EFixtureMatrix(fixtures); err != nil {
		t.Fatalf("canonical matrix rejected: %v", err)
	}

	t.Run("id", func(t *testing.T) {
		drifted := append([]e2eRequestFixture(nil), fixtures...)
		drifted[0].ID = "core/unreviewed/case"
		if err := validateE2EFixtureMatrix(drifted); err == nil {
			t.Fatal("unexpected fixture ID was accepted")
		}
	})
	t.Run("metadata", func(t *testing.T) {
		drifted := append([]e2eRequestFixture(nil), fixtures...)
		drifted[0].Assertion = "text"
		if drifted[0].Assertion == expected[drifted[0].ID].Assertion {
			drifted[0].Stream = !drifted[0].Stream
		}
		if err := validateE2EFixtureMatrix(drifted); err == nil {
			t.Fatal("fixture metadata drift was accepted")
		}
	})
	t.Run("missing", func(t *testing.T) {
		if err := validateE2EFixtureMatrix(fixtures[1:]); err == nil {
			t.Fatal("missing fixture ID was accepted")
		}
	})
}

func TestValidateE2EPlaceholderPolicyRejectsDrift(t *testing.T) {
	valid := e2eRequestFixture{
		ID:      "tools/tool_search/client_responses_turn2",
		Dynamic: []string{"CLIENT_TOOL_SEARCH_CALL", "CLIENT_TOOL_SEARCH_CALL_ID"},
	}
	if err := validateE2EPlaceholderPolicy(valid); err != nil {
		t.Fatalf("canonical placeholder policy rejected: %v", err)
	}
	for _, fixture := range []e2eRequestFixture{
		{ID: "core/text/chat/non_stream", Dynamic: []string{"CLIENT_TOOL_SEARCH_CALL"}},
		{ID: valid.ID, Dynamic: []string{"CLIENT_TOOL_SEARCH_CALL_ID", "CLIENT_TOOL_SEARCH_CALL"}},
		{ID: valid.ID, Dynamic: []string{"CLIENT_TOOL_SEARCH_CALL"}},
	} {
		if err := validateE2EPlaceholderPolicy(fixture); err == nil {
			t.Fatalf("invalid placeholder policy accepted for %q: %v", fixture.ID, fixture.Dynamic)
		}
	}
}

func TestValidateE2EPlaceholderUsageRejectsWrongCounts(t *testing.T) {
	want := map[string]bool{"CLIENT_TOOL_SEARCH_CALL": true, "CLIENT_TOOL_SEARCH_CALL_ID": true}
	if err := validateE2EPlaceholderUsage(want, map[string]int{"CLIENT_TOOL_SEARCH_CALL": 1, "CLIENT_TOOL_SEARCH_CALL_ID": 1}); err != nil {
		t.Fatalf("canonical placeholder usage rejected: %v", err)
	}
	for _, used := range []map[string]int{
		{"CLIENT_TOOL_SEARCH_CALL": 2, "CLIENT_TOOL_SEARCH_CALL_ID": 1},
		{"CLIENT_TOOL_SEARCH_CALL": 1},
		{"CLIENT_TOOL_SEARCH_CALL": 1, "CLIENT_TOOL_SEARCH_CALL_ID": 1, "OTHER": 1},
	} {
		if err := validateE2EPlaceholderUsage(want, used); err == nil {
			t.Fatalf("invalid placeholder counts accepted: %v", used)
		}
	}
}

func TestValidateE2ENoResidualPlaceholdersRejectsAllSyntax(t *testing.T) {
	if err := validateE2ENoResidualPlaceholders([]byte(`{"input":"ordinary text","nested":{"ok":true}}`)); err != nil {
		t.Fatalf("ordinary JSON rejected: %v", err)
	}
	for _, body := range []string{
		`{"input":"{{lowercase_name}}"}`,
		`{"input":"unclosed {{placeholder"}`,
		`{"input":"orphan }} token"}`,
		`{"nested":{"$live_fixture":"CLIENT_TOOL_SEARCH_CALL"}}`,
	} {
		if err := validateE2ENoResidualPlaceholders([]byte(body)); err == nil {
			t.Fatalf("residual placeholder accepted: %s", body)
		}
	}
}

func TestValidateE2ENoCredentialOrProviderMaterialRejectsNestedValues(t *testing.T) {
	if err := validateE2ENoCredentialOrProviderMaterial(map[string]any{
		"model": liveClientModel,
		"input": "ordinary fixture text",
	}, "$"); err != nil {
		t.Fatalf("fixture model alias rejected: %v", err)
	}

	var escapedKey any
	if err := json.Unmarshal([]byte(`{"nested":{"api\u004bey":"redacted"}}`), &escapedKey); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{
		escapedKey,
		map[string]any{"nested": []any{"Bearer redacted-token"}},
		map[string]any{"model": "gpt-5.4"},
		map[string]any{"note": "token sk-not-a-real-secret"},
	} {
		if err := validateE2ENoCredentialOrProviderMaterial(value, "$"); err == nil {
			t.Fatalf("forbidden nested material accepted: %#v", value)
		}
	}
}

func TestValidateE2EConvertedResponsesRequestRejectsSemanticLoss(t *testing.T) {
	t.Run("web search tool", func(t *testing.T) {
		fixture := e2eRequestFixture{Assertion: "web_search"}
		body := []byte(`{"model":"fixture-provider-model","input":"search","stream":false,"tools":[]}`)
		if err := validateE2EConvertedResponsesRequest(fixture, body); err == nil {
			t.Fatal("missing web_search tool was accepted")
		}
	})
	t.Run("structured schema", func(t *testing.T) {
		fixture := e2eRequestFixture{Assertion: "structured_output", Marker: "RM_MARKER"}
		body := []byte(`{"model":"fixture-provider-model","input":"RM_MARKER","text":{"format":{"type":"json_schema","schema":{"type":"object","properties":{},"required":[]}}}}`)
		if err := validateE2EConvertedResponsesRequest(fixture, body); err == nil {
			t.Fatal("schema without marker property was accepted")
		}
	})
	t.Run("call output correlation", func(t *testing.T) {
		request := map[string]any{"input": []any{
			map[string]any{"type": "function_call", "call_id": "call_a", "name": "record_marker"},
			map[string]any{"type": "function_call_output", "call_id": "call_b", "output": "ok"},
		}}
		if err := validateE2ECallOutputLedger(request, "function_call", "function_call_output", []string{"record_marker"}); err == nil {
			t.Fatal("uncorrelated function output was accepted")
		}
	})
	t.Run("client tool search correlation", func(t *testing.T) {
		request := map[string]any{"input": []any{
			map[string]any{"type": "message", "role": "user"},
			map[string]any{"type": "tool_search_call", "call_id": "call_a", "execution": "client", "status": "completed"},
			map[string]any{"type": "tool_search_output", "call_id": "call_b"},
			map[string]any{"type": "additional_tools", "role": "developer"},
		}}
		if err := validateE2EClientToolSearchContinuation(request); err == nil {
			t.Fatal("uncorrelated client tool search continuation was accepted")
		}
	})
}

func TestE2ERequestFixtureCatalog(t *testing.T) {
	catalog := loadE2EFixtureCatalog(t)
	if got, want := len(catalog), len(expectedE2EFixtureMatrix()); got != want {
		t.Fatalf("fixture request count=%d, want %d", got, want)
	}

	referenced := make(map[string]string, len(catalog))
	for _, fixture := range catalog {
		fixture := fixture
		t.Run(strings.ReplaceAll(fixture.ID, "/", "_"), func(t *testing.T) {
			if previous, exists := referenced[fixture.Request]; exists {
				t.Fatalf("request fixture %q is also referenced by %q", fixture.Request, previous)
			}
			referenced[fixture.Request] = fixture.ID
			replacements := e2eFixtureDummyReplacements(fixture)
			body := renderE2ERequestFixture(t, fixture, replacements)
			requestURL := e2eFixtureRequestURL(t, fixture)
			info, err := InspectRequest(context.Background(), fixture.Protocol, requestURL, body)
			if err != nil {
				t.Fatalf("fixture request does not pass public inspection (%T): %v", err, err)
			}
			if info.Model != liveClientModel || info.Stream != fixture.Stream {
				t.Fatalf("fixture routing info model=%q stream=%v, want model=%q stream=%v", info.Model, info.Stream, liveClientModel, fixture.Stream)
			}
			if fixture.Marker != "" && !bytes.Contains(body, []byte(fixture.Marker)) {
				t.Fatalf("fixture does not contain declared marker %q", fixture.Marker)
			}
		})
	}

	requestFiles := make(map[string]bool, len(catalog))
	err := fs.WalkDir(e2eFixtureFS, e2eFixtureRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(name, ".request.json") {
			requestFiles[strings.TrimPrefix(name, e2eFixtureRoot+"/")] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for request := range requestFiles {
		if _, exists := referenced[request]; !exists {
			t.Errorf("orphan request fixture %q is absent from catalog", request)
		}
	}
	for request := range referenced {
		if !requestFiles[request] {
			t.Errorf("catalog references missing request fixture %q", request)
		}
	}
}

func TestE2ERequestFixturesConvertThroughPublicAdapter(t *testing.T) {
	capturedRequests := make(chan []byte, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.EscapedPath(), "/responses") {
			t.Errorf("upstream request method=%q path=%q", request.Method, request.URL.EscapedPath())
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Header.Get("Authorization") != "Bearer fixture-provider-key" {
			t.Error("adapter did not install fixture provider credentials")
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read upstream request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		info, err := InspectRequest(context.Background(), ProtocolResponses, nil, body)
		if err != nil {
			t.Errorf("converted Responses request is invalid (%T): %v", err, err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if info.Model != "fixture-provider-model" {
			t.Errorf("converted model=%q, want fixture-provider-model", info.Model)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		capturedRequests <- append([]byte(nil), body...)
		if info.Stream {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, e2eFixtureProviderStream)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, e2eFixtureProviderResponse)
	}))
	defer provider.Close()

	adapter, err := NewOpenAIResponsesAdapter(provider.URL+"/v1", "fixture-provider-key", WithModel("fixture-provider-model"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range loadE2EFixtureCatalog(t) {
		fixture := fixture
		t.Run(strings.ReplaceAll(fixture.ID, "/", "_"), func(t *testing.T) {
			body := renderE2ERequestFixture(t, fixture, e2eFixtureDummyReplacements(fixture))
			response, err := invokeAdapter(context.Background(), adapter, fixture.Protocol, newE2EFixtureRequest(t, fixture, body, ""))
			if err != nil {
				t.Fatalf("fixture adapter invocation failed (%T): %v", err, err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("fixture adapter status=%d", response.StatusCode)
			}
			convertedRequest := <-capturedRequests
			if err := validateE2EConvertedResponsesRequest(fixture, convertedRequest); err != nil {
				t.Fatalf("converted request lost %q semantics: %v", fixture.Assertion, err)
			}
			if fixture.Stream {
				decoder, err := codec.New(core.Protocol(fixture.Protocol)).NewStreamDecoder(response.Body, core.StreamOptions{MaxFrameBytes: 1 << 20})
				if err != nil {
					t.Fatal(err)
				}
				frames := make([]core.Frame, 0, 12)
				for {
					frame, err := decoder.Next(context.Background())
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatalf("decode converted fixture stream (%T): %v", err, err)
					}
					frames = append(frames, frame)
				}
				terminal, _, err := streamx.CollectNativeResponse(core.Protocol(fixture.Protocol), frames, core.RejectSemanticLoss)
				if err != nil {
					t.Fatalf("collect converted fixture stream (%T): %v", err, err)
				}
				if err := codec.New(core.Protocol(fixture.Protocol)).ValidateResponse(context.Background(), terminal); err != nil {
					t.Fatalf("invalid converted fixture terminal response (%T): %v", err, err)
				}
				return
			}
			body, err = io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err := codec.New(core.Protocol(fixture.Protocol)).ValidateResponse(context.Background(), body); err != nil {
				t.Fatalf("invalid converted fixture response (%T): %v", err, err)
			}
		})
	}
}

func validateE2EConvertedResponsesRequest(fixture e2eRequestFixture, body []byte) error {
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("decode converted Responses request: %w", err)
	}
	if model, _ := request["model"].(string); model != "fixture-provider-model" {
		return fmt.Errorf("model=%q, want fixture-provider-model", model)
	}
	stream, ok := request["stream"].(bool)
	if stream != fixture.Stream || fixture.Stream && !ok {
		return fmt.Errorf("stream=%v (present=%v), want %v", stream, ok, fixture.Stream)
	}
	if fixture.Marker != "" && !e2eValueContainsString(map[string]any{
		"input":        request["input"],
		"instructions": request["instructions"],
	}, fixture.Marker) {
		return fmt.Errorf("input and instructions omit marker")
	}

	switch fixture.Assertion {
	case "text":
		if fixture.Capability == "tool_result" {
			return validateE2ECallOutputLedger(request, "function_call", "function_call_output", []string{"record_marker"})
		}
		return validateE2EInputPresent(request)
	case "exact_text":
		switch fixture.Capability {
		case "conversation":
			return validateE2EConversation(fixture, request)
		case "parallel_tool_result":
			return validateE2ECallOutputLedger(request, "function_call", "function_call_output", []string{"record_left", "record_right"})
		default:
			return validateE2EInputPresent(request)
		}
	case "tool_call":
		tool, err := e2eFindResponseTool(request, "function", "record_marker")
		if err != nil {
			return err
		}
		if err := validateE2EJSONSchema(tool["parameters"], []string{"marker"}); err != nil {
			return fmt.Errorf("record_marker parameters: %w", err)
		}
		return validateE2EToolChoice(request, "function", "record_marker", true)
	case "structured_output":
		text, err := e2eObjectField(request, "text")
		if err != nil {
			return err
		}
		format, err := e2eObjectField(text, "format")
		if err != nil {
			return fmt.Errorf("text.%w", err)
		}
		if formatType, _ := format["type"].(string); formatType != "json_schema" {
			return fmt.Errorf("text.format.type=%q, want json_schema", formatType)
		}
		return validateE2EJSONSchema(format["schema"], []string{"marker"})
	case "complex_tool_call":
		tool, err := e2eFindResponseTool(request, "function", "record_complex")
		if err != nil {
			return err
		}
		if err := validateE2EJSONSchema(tool["parameters"], []string{"marker", "count", "enabled", "mode", "tags", "payload"}); err != nil {
			return fmt.Errorf("record_complex parameters: %w", err)
		}
		schema, _ := tool["parameters"].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		payload, _ := properties["payload"].(map[string]any)
		if err := validateE2EJSONSchema(payload, []string{"nested", "score"}); err != nil {
			return fmt.Errorf("record_complex payload: %w", err)
		}
		if fixture.Protocol != ProtocolGenerateContent {
			if parallel, ok := request["parallel_tool_calls"].(bool); !ok || parallel {
				return fmt.Errorf("parallel_tool_calls=%v (present=%v), want false", parallel, ok)
			}
		}
		return validateE2EToolChoice(request, "function", "record_complex", true)
	case "metadata_service_tier":
		metadata, err := e2eObjectField(request, "metadata")
		if err != nil {
			return err
		}
		if metadata["routemorph_suite"] != "extended" {
			return fmt.Errorf("metadata.routemorph_suite=%v, want extended", metadata["routemorph_suite"])
		}
		wantCase := "metadata_service_tier_chat"
		if fixture.Protocol == ProtocolResponses {
			wantCase = "metadata_service_tier_responses"
		}
		if metadata["routemorph_case"] != wantCase {
			return fmt.Errorf("metadata.routemorph_case=%v, want %s", metadata["routemorph_case"], wantCase)
		}
		if request["service_tier"] != "auto" {
			return fmt.Errorf("service_tier=%v, want auto", request["service_tier"])
		}
		return nil
	case "custom_tool_call":
		tool, err := e2eFindResponseTool(request, "custom", "record_marker_text")
		if err != nil {
			return err
		}
		format, err := e2eObjectField(tool, "format")
		if err != nil {
			return fmt.Errorf("custom tool %w", err)
		}
		if format["type"] != "text" {
			return fmt.Errorf("custom tool format.type=%v, want text", format["type"])
		}
		return validateE2EToolChoice(request, "custom", "record_marker_text", false)
	case "custom_tool_output":
		return validateE2ECallOutputLedger(request, "custom_tool_call", "custom_tool_call_output", []string{"record_marker_text"})
	case "web_search":
		tool, err := e2eFindResponseTool(request, "web_search", "")
		if err != nil {
			return err
		}
		if tool["search_context_size"] != "low" {
			return fmt.Errorf("web_search search_context_size=%v, want low", tool["search_context_size"])
		}
		location, err := e2eObjectField(tool, "user_location")
		if err != nil {
			return fmt.Errorf("web_search %w", err)
		}
		if location["type"] != "approximate" || location["city"] != "Shanghai" || location["country"] != "CN" || location["timezone"] != "Asia/Shanghai" {
			return fmt.Errorf("web_search user_location lost approximate Shanghai/CN/Asia/Shanghai fields")
		}
		return nil
	case "server_tool_search":
		functionTool, err := e2eFindResponseTool(request, "function", "record_marker")
		if err != nil {
			return err
		}
		if functionTool["defer_loading"] != true {
			return fmt.Errorf("record_marker.defer_loading=%v, want true", functionTool["defer_loading"])
		}
		searchTool, err := e2eFindResponseTool(request, "tool_search", "")
		if err != nil {
			return err
		}
		if searchTool["execution"] != "server" {
			return fmt.Errorf("tool_search.execution=%v, want server", searchTool["execution"])
		}
		return validateE2EToolChoice(request, "", "", true)
	case "client_tool_search_call":
		searchTool, err := e2eFindResponseTool(request, "tool_search", "")
		if err != nil {
			return err
		}
		if searchTool["execution"] != "client" {
			return fmt.Errorf("tool_search.execution=%v, want client", searchTool["execution"])
		}
		return validateE2EJSONSchema(searchTool["parameters"], []string{"query"})
	case "client_tool_search_continuation":
		return validateE2EClientToolSearchContinuation(request)
	default:
		return fmt.Errorf("unknown assertion %q", fixture.Assertion)
	}
}

func validateE2EInputPresent(request map[string]any) error {
	switch input := request["input"].(type) {
	case string:
		if strings.TrimSpace(input) == "" {
			return fmt.Errorf("input is empty")
		}
	case []any:
		if len(input) == 0 {
			return fmt.Errorf("input is empty")
		}
	default:
		return fmt.Errorf("input has type %T, want string or array", request["input"])
	}
	return nil
}

func validateE2EConversation(fixture e2eRequestFixture, request map[string]any) error {
	items, err := e2eResponseInputItems(request)
	if err != nil {
		return err
	}
	messages := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if item["type"] == "message" {
			messages = append(messages, item)
		}
	}
	prefixRoles := make([]string, 0, 2)
	for len(messages) > 0 && (messages[0]["role"] == "system" || messages[0]["role"] == "developer") {
		role, _ := messages[0]["role"].(string)
		if !e2eMessageHasContentType(messages[0], "input_text") {
			return fmt.Errorf("%s message omits input_text content", role)
		}
		prefixRoles = append(prefixRoles, role)
		messages = messages[1:]
	}
	if fixture.Protocol == ProtocolChat && !equalE2EStrings(prefixRoles, []string{"system", "developer"}) {
		return fmt.Errorf("Chat instruction roles=%v, want system/developer", prefixRoles)
	}
	if len(messages) != 3 || messages[0]["role"] != "user" || messages[1]["role"] != "assistant" || messages[2]["role"] != "user" {
		roles := make([]any, 0, len(messages))
		for _, message := range messages {
			roles = append(roles, message["role"])
		}
		return fmt.Errorf("message roles=%v, want user/assistant/user conversation", roles)
	}
	for index, wantType := range []string{"input_text", "output_text", "input_text"} {
		if !e2eMessageHasContentType(messages[index], wantType) {
			return fmt.Errorf("conversation message[%d] omits %s content", index, wantType)
		}
	}
	if !e2eValueContainsString(messages[0], "RM_CONVERSATION_OLD_") || !e2eValueContainsString(messages[1], "ACK") {
		return fmt.Errorf("conversation history omits old marker or assistant ACK")
	}
	return nil
}

func e2eMessageHasContentType(message map[string]any, wantType string) bool {
	content, ok := message["content"].([]any)
	if !ok {
		return false
	}
	for _, value := range content {
		part, ok := value.(map[string]any)
		if ok && part["type"] == wantType {
			return true
		}
	}
	return false
}

func validateE2ECallOutputLedger(request map[string]any, callType, outputType string, wantNames []string) error {
	items, err := e2eResponseInputItems(request)
	if err != nil {
		return err
	}
	calls := make(map[string]string, len(wantNames))
	outputs := make(map[string]bool, len(wantNames))
	for _, item := range items {
		typeName, _ := item["type"].(string)
		callID, _ := item["call_id"].(string)
		switch typeName {
		case callType:
			name, _ := item["name"].(string)
			if callID == "" || name == "" {
				return fmt.Errorf("%s omits call_id or name", callType)
			}
			if _, duplicate := calls[callID]; duplicate {
				return fmt.Errorf("duplicate %s call_id %q", callType, callID)
			}
			calls[callID] = name
		case outputType:
			if callID == "" {
				return fmt.Errorf("%s omits call_id", outputType)
			}
			if _, present := item["output"]; !present {
				return fmt.Errorf("%s %q omits output", outputType, callID)
			}
			outputs[callID] = true
		}
	}
	if len(calls) != len(wantNames) || len(outputs) != len(wantNames) {
		return fmt.Errorf("ledger has %d calls and %d outputs, want %d correlated pairs", len(calls), len(outputs), len(wantNames))
	}
	want := make(map[string]bool, len(wantNames))
	for _, name := range wantNames {
		want[name] = true
	}
	for callID, name := range calls {
		if !want[name] {
			return fmt.Errorf("unexpected %s name %q", callType, name)
		}
		delete(want, name)
		if !outputs[callID] {
			return fmt.Errorf("%s %q has no correlated %s", callType, callID, outputType)
		}
	}
	if len(want) != 0 {
		return fmt.Errorf("ledger omits expected call names")
	}
	return nil
}

func validateE2EClientToolSearchContinuation(request map[string]any) error {
	items, err := e2eResponseInputItems(request)
	if err != nil {
		return err
	}
	wantTypes := []string{"message", "tool_search_call", "tool_search_output", "additional_tools"}
	if len(items) != len(wantTypes) {
		return fmt.Errorf("input item count=%d, want %d", len(items), len(wantTypes))
	}
	for index, wantType := range wantTypes {
		if items[index]["type"] != wantType {
			return fmt.Errorf("input[%d].type=%v, want %s", index, items[index]["type"], wantType)
		}
	}
	callID, _ := items[1]["call_id"].(string)
	if items[1]["id"] != "tsc_fixture" || callID != "call_fixture_tool_search" || items[1]["execution"] != "client" || items[1]["status"] != "completed" {
		return fmt.Errorf("tool_search_call lost rendered id/call_id/client/completed semantics")
	}
	if _, ok := items[1]["arguments"].(map[string]any); !ok {
		return fmt.Errorf("tool_search_call.arguments has type %T, want object", items[1]["arguments"])
	}
	if outputCallID, _ := items[2]["call_id"].(string); outputCallID != callID {
		return fmt.Errorf("tool_search_output call_id=%q, want %q", outputCallID, callID)
	}
	if items[3]["role"] != "developer" {
		return fmt.Errorf("additional_tools.role=%v, want developer", items[3]["role"])
	}
	for index := 2; index <= 3; index++ {
		tool, err := e2eFindToolInValue(items[index]["tools"], "function", "record_marker")
		if err != nil {
			return fmt.Errorf("input[%d]: %w", index, err)
		}
		if tool["defer_loading"] != true {
			return fmt.Errorf("input[%d] record_marker.defer_loading=%v, want true", index, tool["defer_loading"])
		}
		if err := validateE2EJSONSchema(tool["parameters"], []string{"marker"}); err != nil {
			return fmt.Errorf("input[%d] record_marker parameters: %w", index, err)
		}
	}
	return nil
}

func validateE2EJSONSchema(value any, required []string) error {
	schema, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("schema has type %T, want object", value)
	}
	if schema["type"] != "object" {
		return fmt.Errorf("schema.type=%v, want object", schema["type"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("schema.properties has type %T, want object", schema["properties"])
	}
	requiredValues, ok := schema["required"].([]any)
	if !ok {
		return fmt.Errorf("schema.required has type %T, want array", schema["required"])
	}
	requiredSet := make(map[string]bool, len(requiredValues))
	for _, value := range requiredValues {
		name, ok := value.(string)
		if ok {
			requiredSet[name] = true
		}
	}
	for _, name := range required {
		if _, ok := properties[name].(map[string]any); !ok {
			return fmt.Errorf("schema.properties.%s is missing", name)
		}
		if !requiredSet[name] {
			return fmt.Errorf("schema.required omits %q", name)
		}
	}
	return nil
}

func validateE2EToolChoice(request map[string]any, wantType, wantName string, allowRequired bool) error {
	choice := request["tool_choice"]
	if allowRequired && choice == "required" {
		return nil
	}
	object, ok := choice.(map[string]any)
	if !ok {
		return fmt.Errorf("tool_choice has type %T, want object", choice)
	}
	if object["type"] != wantType || object["name"] != wantName {
		return fmt.Errorf("tool_choice type/name=%v/%v, want %s/%s", object["type"], object["name"], wantType, wantName)
	}
	return nil
}

func e2eFindResponseTool(request map[string]any, wantType, wantName string) (map[string]any, error) {
	return e2eFindToolInValue(request["tools"], wantType, wantName)
}

func e2eFindToolInValue(value any, wantType, wantName string) (map[string]any, error) {
	tools, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("tools has type %T, want array", value)
	}
	for _, value := range tools {
		tool, ok := value.(map[string]any)
		if !ok || tool["type"] != wantType {
			continue
		}
		if wantName == "" || tool["name"] == wantName {
			return tool, nil
		}
	}
	return nil, fmt.Errorf("tools omit type=%q name=%q", wantType, wantName)
}

func e2eResponseInputItems(request map[string]any) ([]map[string]any, error) {
	values, ok := request["input"].([]any)
	if !ok {
		return nil, fmt.Errorf("input has type %T, want item array", request["input"])
	}
	items := make([]map[string]any, 0, len(values))
	for index, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("input[%d] has type %T, want object", index, value)
		}
		items = append(items, item)
	}
	return items, nil
}

func e2eObjectField(parent map[string]any, field string) (map[string]any, error) {
	value, ok := parent[field].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s has type %T, want object", field, parent[field])
	}
	return value, nil
}

func e2eValueContainsString(value any, needle string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if e2eValueContainsString(child, needle) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if e2eValueContainsString(child, needle) {
				return true
			}
		}
	case string:
		return strings.Contains(typed, needle)
	}
	return false
}

func loadE2EFixtureCatalog(tb testing.TB) []e2eRequestFixture {
	tb.Helper()
	raw, err := e2eFixtureFS.ReadFile(e2eFixtureCatalogPath)
	if err != nil {
		tb.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var catalog e2eFixtureCatalog
	if err := decoder.Decode(&catalog); err != nil {
		tb.Fatalf("decode E2E fixture catalog: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		tb.Fatalf("E2E fixture catalog has trailing JSON: %v", err)
	}
	var decodedCatalog any
	if err := json.Unmarshal(raw, &decodedCatalog); err != nil {
		tb.Fatalf("decode E2E fixture catalog for material scan: %v", err)
	}
	if err := validateE2ENoCredentialOrProviderMaterial(decodedCatalog, "$catalog"); err != nil {
		tb.Fatalf("E2E fixture catalog contains forbidden material: %v", err)
	}
	if catalog.Version != 1 || len(catalog.Requests) == 0 {
		tb.Fatalf("invalid E2E fixture catalog version=%d requests=%d", catalog.Version, len(catalog.Requests))
	}
	seenIDs := make(map[string]bool, len(catalog.Requests))
	for index, fixture := range catalog.Requests {
		if fixture.ID == "" || fixture.Suite == "" || fixture.Capability == "" || fixture.Request == "" || fixture.Assertion == "" {
			tb.Fatalf("catalog request[%d] omits required metadata", index)
		}
		if seenIDs[fixture.ID] {
			tb.Fatalf("duplicate E2E fixture id %q", fixture.ID)
		}
		seenIDs[fixture.ID] = true
		if fixture.Suite != "core" && fixture.Suite != "extended" && fixture.Suite != "tools" {
			tb.Fatalf("fixture %q has unknown suite %q", fixture.ID, fixture.Suite)
		}
		if fixture.Protocol != ProtocolChat && fixture.Protocol != ProtocolResponses && fixture.Protocol != ProtocolMessages && fixture.Protocol != ProtocolGenerateContent {
			tb.Fatalf("fixture %q has unknown protocol %q", fixture.ID, fixture.Protocol)
		}
		clean := path.Clean(fixture.Request)
		if clean != fixture.Request || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || !strings.HasSuffix(clean, ".request.json") {
			tb.Fatalf("fixture %q has unsafe request path %q", fixture.ID, fixture.Request)
		}
		if !strings.HasPrefix(clean, fixture.Suite+"/") {
			tb.Fatalf("fixture %q request %q is outside suite %q", fixture.ID, fixture.Request, fixture.Suite)
		}
		if fixture.ID != strings.TrimSuffix(clean, ".request.json") {
			tb.Fatalf("fixture id %q does not match request path %q", fixture.ID, fixture.Request)
		}
		if !strings.HasPrefix(clean, fixture.Suite+"/"+fixture.Capability+"/") {
			tb.Fatalf("fixture %q request %q does not match capability %q", fixture.ID, fixture.Request, fixture.Capability)
		}
		switch fixture.Assertion {
		case "text", "exact_text", "tool_call", "structured_output", "complex_tool_call", "metadata_service_tier", "custom_tool_call", "custom_tool_output", "web_search", "server_tool_search", "client_tool_search_call", "client_tool_search_continuation":
		default:
			tb.Fatalf("fixture %q has unknown assertion %q", fixture.ID, fixture.Assertion)
		}
		seenDynamic := make(map[string]bool, len(fixture.Dynamic))
		for _, name := range fixture.Dynamic {
			if !validE2EPlaceholderName(name) || seenDynamic[name] {
				tb.Fatalf("fixture %q has invalid or duplicate dynamic placeholder %q", fixture.ID, name)
			}
			seenDynamic[name] = true
		}
		if err := validateE2EPlaceholderPolicy(fixture); err != nil {
			tb.Fatal(err)
		}
	}
	if err := validateE2EFixtureMatrix(catalog.Requests); err != nil {
		tb.Fatal(err)
	}
	return catalog.Requests
}

func e2eFixtureByID(tb testing.TB, id string) e2eRequestFixture {
	tb.Helper()
	for _, fixture := range loadE2EFixtureCatalog(tb) {
		if fixture.ID == id {
			return fixture
		}
	}
	tb.Fatalf("unknown E2E fixture id %q", id)
	return e2eRequestFixture{}
}

func renderE2ERequestFixture(tb testing.TB, fixture e2eRequestFixture, replacements map[string]any) []byte {
	tb.Helper()
	raw, err := e2eFixtureFS.ReadFile(e2eFixtureRoot + "/" + fixture.Request)
	if err != nil {
		tb.Fatalf("read request fixture %q: %v", fixture.Request, err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		tb.Fatalf("request fixture %q is invalid JSON: %v", fixture.Request, err)
	}
	if _, ok := value.(map[string]any); !ok {
		tb.Fatalf("request fixture %q must contain one JSON object", fixture.Request)
	}
	if err := validateE2ENoCredentialOrProviderMaterial(value, "$raw"); err != nil {
		tb.Fatalf("request fixture %q contains forbidden material: %v", fixture.Request, err)
	}
	want := make(map[string]bool, len(fixture.Dynamic))
	for _, name := range fixture.Dynamic {
		want[name] = true
	}
	if len(replacements) != len(want) {
		tb.Fatalf("fixture %q replacement count=%d, want %d", fixture.ID, len(replacements), len(want))
	}
	for name := range replacements {
		if !want[name] {
			tb.Fatalf("fixture %q received undeclared replacement %q", fixture.ID, name)
		}
	}
	used := make(map[string]int, len(replacements))
	value, err = expandE2EFixtureValue(value, replacements, used, "$")
	if err != nil {
		tb.Fatalf("render fixture %q: %v", fixture.ID, err)
	}
	if err := validateE2EPlaceholderUsage(want, used); err != nil {
		tb.Fatalf("fixture %q: %v", fixture.ID, err)
	}
	if err := validateE2ENoCredentialOrProviderMaterial(value, "$rendered"); err != nil {
		tb.Fatalf("rendered fixture %q contains forbidden material: %v", fixture.ID, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		tb.Fatalf("encode rendered fixture %q: %v", fixture.ID, err)
	}
	if err := validateE2ENoResidualPlaceholders(encoded); err != nil {
		tb.Fatalf("fixture %q: %v", fixture.ID, err)
	}
	return encoded
}

func expandE2EFixtureValue(value any, replacements map[string]any, used map[string]int, jsonPath string) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		if marker, exists := typed["$live_fixture"]; exists {
			if len(typed) != 1 {
				return nil, fmt.Errorf("%s: $live_fixture sentinel must be the only object member", jsonPath)
			}
			name, ok := marker.(string)
			if !ok || !validE2EPlaceholderName(name) {
				return nil, fmt.Errorf("%s: invalid structural placeholder", jsonPath)
			}
			replacement, ok := replacements[name]
			if !ok {
				return nil, fmt.Errorf("%s: missing replacement %q", jsonPath, name)
			}
			used[name]++
			return replacement, nil
		}
		for key, child := range typed {
			expanded, err := expandE2EFixtureValue(child, replacements, used, jsonPath+"."+key)
			if err != nil {
				return nil, err
			}
			typed[key] = expanded
		}
		return typed, nil
	case []any:
		for index, child := range typed {
			expanded, err := expandE2EFixtureValue(child, replacements, used, fmt.Sprintf("%s[%d]", jsonPath, index))
			if err != nil {
				return nil, err
			}
			typed[index] = expanded
		}
		return typed, nil
	case string:
		matches := e2eStringPlaceholder.FindAllStringSubmatch(typed, -1)
		for _, match := range matches {
			name := match[1]
			replacement, ok := replacements[name]
			if !ok {
				return nil, fmt.Errorf("%s: missing replacement %q", jsonPath, name)
			}
			text, ok := replacement.(string)
			if !ok {
				if typed == match[0] && len(matches) == 1 {
					used[name]++
					return replacement, nil
				}
				return nil, fmt.Errorf("%s: embedded replacement %q must be a string", jsonPath, name)
			}
			typed = strings.ReplaceAll(typed, match[0], text)
			used[name]++
		}
		return typed, nil
	default:
		return value, nil
	}
}

func validE2EPlaceholderName(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		if character >= 'A' && character <= 'Z' || index > 0 && (character >= '0' && character <= '9' || character == '_') {
			continue
		}
		return false
	}
	return true
}

func e2eFixtureDummyReplacements(fixture e2eRequestFixture) map[string]any {
	replacements := make(map[string]any, len(fixture.Dynamic))
	for _, name := range fixture.Dynamic {
		switch name {
		case "CLIENT_TOOL_SEARCH_CALL_ID":
			replacements[name] = "call_fixture_tool_search"
		case "CLIENT_TOOL_SEARCH_CALL":
			replacements[name] = map[string]any{
				"type": "tool_search_call", "id": "tsc_fixture", "call_id": "call_fixture_tool_search",
				"arguments": map[string]any{}, "execution": "client", "status": "completed",
			}
		default:
			replacements[name] = "fixture_" + strings.ToLower(name)
		}
	}
	return replacements
}

func newE2EFixtureRequest(tb testing.TB, fixture e2eRequestFixture, body []byte, source string) *Request {
	tb.Helper()
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("X-Request-ID", "routemorph-e2e-"+strings.NewReplacer("/", "-", "_", "-").Replace(fixture.ID))
	if fixture.Stream {
		header.Set("Accept", "text/event-stream")
	}
	if source != "" {
		header.Set("X-Source", source)
	}
	request := &Request{Header: header, Body: bytes.NewReader(body)}
	request.URL = e2eFixtureRequestURL(tb, fixture)
	return request
}

func e2eFixtureRequestURL(tb testing.TB, fixture e2eRequestFixture) *url.URL {
	tb.Helper()
	if fixture.Protocol != ProtocolGenerateContent {
		return nil
	}
	method := "generateContent"
	if fixture.Stream {
		method = "streamGenerateContent"
	}
	value, err := url.Parse("/v1beta/models/" + url.PathEscape(liveClientModel) + ":" + method)
	if err != nil {
		tb.Fatal(err)
	}
	return value
}

const e2eFixtureProviderResponse = `{"id":"resp_fixture","object":"response","created_at":1,"model":"fixture-provider-model","status":"completed","service_tier":"default","output":[{"id":"msg_fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fixture-ok","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`

const e2eFixtureProviderStream = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_fixture","object":"response","created_at":1,"model":"fixture-provider-model","status":"in_progress","service_tier":"auto","output":[],"usage":null}}

event: response.in_progress
data: {"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_fixture","object":"response","created_at":1,"model":"fixture-provider-model","status":"in_progress","service_tier":"auto","output":[],"usage":null}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"msg_fixture","type":"message","role":"assistant","status":"in_progress","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","sequence_number":3,"item_id":"msg_fixture","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[],"logprobs":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":4,"item_id":"msg_fixture","output_index":0,"content_index":0,"delta":"fixture-ok","logprobs":[]}

event: response.output_text.done
data: {"type":"response.output_text.done","sequence_number":5,"item_id":"msg_fixture","output_index":0,"content_index":0,"text":"fixture-ok","logprobs":[]}

event: response.content_part.done
data: {"type":"response.content_part.done","sequence_number":6,"item_id":"msg_fixture","output_index":0,"content_index":0,"part":{"type":"output_text","text":"fixture-ok","annotations":[],"logprobs":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":7,"output_index":0,"item":{"id":"msg_fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fixture-ok","annotations":[],"logprobs":[]}]}}

event: response.completed
data: {"type":"response.completed","sequence_number":8,"response":{"id":"resp_fixture","object":"response","created_at":1,"model":"fixture-provider-model","status":"completed","service_tier":"default","output":[{"id":"msg_fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fixture-ok","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}

`
