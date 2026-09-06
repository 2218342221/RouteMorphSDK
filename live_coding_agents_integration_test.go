//go:build integration && linux

package routemorph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	liveCodingAgentDummyCredential = "routemorph-local-dummy-credential"
	liveCodingAgentTimeout         = 5 * time.Minute
)

type liveCodingAgentConfig struct {
	baseURL string
	apiKey  string
	model   string
	source  string
}

type liveCodingAgentGateway struct {
	server   *httptest.Server
	failures atomic.Int64
	mu       sync.Mutex
	records  []liveCodingAgentRequest
}

type liveCodingAgentRequest struct {
	path        string
	body        []byte
	diagnostics []Diagnostic
	done        chan struct{}
}

type liveCodingAgentAdapterCall func(context.Context, *Request) (*Response, error)

type liveCodingAgentCLI struct {
	name   string
	binary string
	run    func(*testing.T, string, string, string, string, string, bool) []byte
}

type liveCodingAgentScenario string

const (
	liveCodingAgentText      liveCodingAgentScenario = "text"
	liveCodingAgentReadFile  liveCodingAgentScenario = "read_file"
	liveCodingAgentToolError liveCodingAgentScenario = "tool_error"
)

func TestLiveCodingAgentCLICompatibility(t *testing.T) {
	config := requireLiveCodingAgentConfig(t)
	adapter, err := NewOpenAIResponsesAdapter(
		config.baseURL,
		config.apiKey,
		WithModel(config.model),
		WithCodingAgentCompatibility(),
	)
	if err != nil {
		t.Fatal("create live Responses adapter failed")
	}
	gateway := newLiveCodingAgentGateway(t, adapter, config.source)

	agents := []liveCodingAgentCLI{
		{name: "claude", binary: "claude", run: runLiveClaudeCode},
		{name: "gemini", binary: "gemini", run: runLiveGeminiCLI},
		{name: "codex", binary: "codex", run: runLiveCodexCLI},
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Fatal("bwrap is required for the live coding-agent suite")
	}
	binaries := make(map[string]string, len(agents))
	for _, agent := range agents {
		binary, err := exec.LookPath(agent.binary)
		if err != nil {
			t.Fatalf("%s binary is required for the live coding-agent suite", agent.name)
		}
		binaries[agent.name] = binary
		t.Logf("client=%s version=%q", agent.name, liveCodingAgentVersion(t, binary))
	}
	for _, agent := range agents {
		agent := agent
		t.Run(agent.name, func(t *testing.T) {
			binary := binaries[agent.name]

			t.Run("text", func(t *testing.T) {
				marker := "RM_LIVE_CLI_TEXT_" + strings.ToUpper(agent.name)
				prompt := "Do not call any tool. Reply with exactly this ASCII token and nothing else: " + marker
				assertLiveCodingAgentCase(t, gateway, config.model, agent, binary, prompt, marker, liveCodingAgentText)
			})

			t.Run("read_file", func(t *testing.T) {
				marker := "RM_LIVE_CLI_TOOL_" + strings.ToUpper(agent.name)
				prompt := "Use your filesystem reading tool to read ./fixture.txt. " +
					"Do not infer or guess its contents. Reply with exactly the complete file contents and nothing else."
				assertLiveCodingAgentCase(t, gateway, config.model, agent, binary, prompt, marker, liveCodingAgentReadFile)
			})

			t.Run("tool_error", func(t *testing.T) {
				marker := "RM_LIVE_CLI_ERROR_" + strings.ToUpper(agent.name)
				prompt := "Use your filesystem reading tool to read ./missing-routemorph-fixture.txt. " +
					"After that tool reports the missing file, reply with exactly this ASCII token and nothing else: " + marker
				assertLiveCodingAgentCase(t, gateway, config.model, agent, binary, prompt, marker, liveCodingAgentToolError)
			})
		})
	}
}

func liveCodingAgentVersion(t *testing.T, binary string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("query CLI version failed: timed_out=%v output_%s", ctx.Err() != nil, liveCodingAgentFingerprint(output))
	}
	version := strings.TrimSpace(string(output))
	if version == "" || strings.ContainsAny(version, "\r\n") || len(version) > 128 {
		t.Fatalf("CLI returned an invalid version string: %s", liveCodingAgentFingerprint(output))
	}
	return version
}

func requireLiveCodingAgentConfig(t *testing.T) liveCodingAgentConfig {
	t.Helper()
	if os.Getenv("ROUTEMORPH_LIVE_CLI") != "1" {
		t.Skip("set ROUTEMORPH_LIVE_CLI=1 to run billable real-CLI tests")
	}
	config := liveCodingAgentConfig{
		baseURL: strings.TrimSpace(os.Getenv("ROUTEMORPH_LIVE_BASE_URL")),
		apiKey:  os.Getenv("ROUTEMORPH_LIVE_API_KEY"),
		model:   strings.TrimSpace(os.Getenv("ROUTEMORPH_LIVE_MODEL")),
		source:  strings.TrimSpace(os.Getenv("ROUTEMORPH_LIVE_X_SOURCE")),
	}
	if config.baseURL == "" || config.apiKey == "" {
		t.Fatal("ROUTEMORPH_LIVE_BASE_URL and ROUTEMORPH_LIVE_API_KEY are required")
	}
	endpoint, err := url.Parse(config.baseURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		t.Fatal("ROUTEMORPH_LIVE_BASE_URL must be an HTTPS URL without user info, query, or fragment")
	}
	if strings.ContainsAny(config.source, "\r\n") {
		t.Fatal("ROUTEMORPH_LIVE_X_SOURCE must not contain line breaks")
	}
	if config.model == "" {
		config.model = "gpt-5.4"
	}
	return config
}

func newLiveCodingAgentGateway(t *testing.T, adapter *Adapter, source string) *liveCodingAgentGateway {
	t.Helper()
	gateway := &liveCodingAgentGateway{}
	mux := http.NewServeMux()
	relay := func(call liveCodingAgentAdapterCall) http.HandlerFunc {
		return func(writer http.ResponseWriter, request *http.Request) {
			body, readErr := io.ReadAll(request.Body)
			_ = request.Body.Close()
			if readErr != nil {
				gateway.failures.Add(1)
				writeLiveCodingAgentGatewayError(writer, http.StatusBadRequest)
				return
			}
			recordIndex := gateway.recordRequest(request.URL.Path, body)
			defer gateway.completeRequest(recordIndex)
			t.Logf("local gateway request[%d] %s%s", recordIndex, request.URL.Path, liveCodingAgentSafeRequestShape(request.URL.Path, body))
			header := request.Header.Clone()
			if source != "" {
				header.Set("X-Source", source)
			}
			response, err := call(request.Context(), &Request{
				Header: header,
				URL:    request.URL,
				Body:   bytes.NewReader(body),
			})
			if err != nil || response == nil {
				gateway.failures.Add(1)
				t.Logf("local gateway conversion failed on %s: %v", request.URL.Path, err)
				writeLiveCodingAgentGatewayError(writer, http.StatusBadGateway)
				return
			}
			writeErr := response.WriteTo(writer)
			gateway.recordDiagnostics(recordIndex, response.Meta.Diagnostics())
			t.Logf("local gateway request[%d] completed with %d diagnostics", recordIndex, len(response.Meta.Diagnostics()))
			if writeErr != nil && !expectedLiveCodingAgentDisconnect(writeErr) {
				gateway.failures.Add(1)
				t.Logf("local gateway response write failed on %s: %T", request.URL.Path, writeErr)
			}
		}
	}
	mux.HandleFunc("POST /v1/chat/completions", relay(adapter.OpenAIChatCompletions))
	mux.HandleFunc("POST /v1/responses", relay(adapter.OpenAIResponses))
	mux.HandleFunc("POST /v1/messages", relay(adapter.AnthropicMessages))
	mux.HandleFunc("POST /v1/models/", relay(adapter.GeminiGenerateContent))
	mux.HandleFunc("POST /v1beta/models/", relay(adapter.GeminiGenerateContent))
	mux.HandleFunc("/", func(writer http.ResponseWriter, _ *http.Request) {
		writeLiveCodingAgentGatewayError(writer, http.StatusNotFound)
	})
	gateway.server = httptest.NewServer(mux)
	t.Cleanup(gateway.server.Close)

	endpoint, err := url.Parse(gateway.server.URL)
	if err != nil || net.ParseIP(endpoint.Hostname()) == nil || !net.ParseIP(endpoint.Hostname()).IsLoopback() {
		t.Fatal("httptest gateway did not bind to a loopback address")
	}
	return gateway
}

func liveCodingAgentSafeRequestShape(path string, body []byte) string {
	if path != "/v1/messages" {
		return ""
	}
	var envelope struct {
		Thinking *struct {
			Type    string `json:"type"`
			Display string `json:"display"`
		} `json:"thinking"`
		OutputConfig *struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Thinking == nil {
		return " messages_thinking=absent"
	}
	effort := ""
	if envelope.OutputConfig != nil {
		effort = envelope.OutputConfig.Effort
	}
	return fmt.Sprintf(" messages_thinking_type=%q display=%q output_effort=%q", envelope.Thinking.Type, envelope.Thinking.Display, effort)
}

func (g *liveCodingAgentGateway) recordRequest(path string, body []byte) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.records = append(g.records, liveCodingAgentRequest{path: path, body: append([]byte(nil), body...), done: make(chan struct{})})
	return len(g.records) - 1
}

func (g *liveCodingAgentGateway) completeRequest(index int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index < 0 || index >= len(g.records) || g.records[index].done == nil {
		return
	}
	close(g.records[index].done)
	g.records[index].done = nil
}

func (g *liveCodingAgentGateway) waitForRecordsSince(index int, timeout time.Duration) bool {
	g.mu.Lock()
	if index < 0 || index > len(g.records) {
		g.mu.Unlock()
		return false
	}
	pending := make([]<-chan struct{}, 0, len(g.records)-index)
	for _, record := range g.records[index:] {
		if record.done != nil {
			pending = append(pending, record.done)
		}
	}
	g.mu.Unlock()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for _, done := range pending {
		select {
		case <-done:
		case <-deadline.C:
			return false
		}
	}
	return true
}

func (g *liveCodingAgentGateway) recordDiagnostics(index int, diagnostics []Diagnostic) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index < 0 || index >= len(g.records) {
		return
	}
	g.records[index].diagnostics = append([]Diagnostic(nil), diagnostics...)
}

func (g *liveCodingAgentGateway) recordCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.records)
}

func (g *liveCodingAgentGateway) recordsSince(index int) []liveCodingAgentRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index < 0 || index > len(g.records) {
		return nil
	}
	result := make([]liveCodingAgentRequest, len(g.records)-index)
	for offset, record := range g.records[index:] {
		result[offset] = liveCodingAgentRequest{
			path:        record.path,
			body:        append([]byte(nil), record.body...),
			diagnostics: append([]Diagnostic(nil), record.diagnostics...),
		}
	}
	return result
}

func expectedLiveCodingAgentDisconnect(writeErr error) bool {
	return errors.Is(writeErr, context.Canceled) ||
		errors.Is(writeErr, io.ErrClosedPipe) ||
		errors.Is(writeErr, syscall.EPIPE) ||
		errors.Is(writeErr, syscall.ECONNRESET)
}

func TestExpectedLiveCodingAgentDisconnect(t *testing.T) {
	if expectedLiveCodingAgentDisconnect(errors.New("stream validation failed")) {
		t.Fatal("arbitrary stream error was classified as a client disconnect")
	}
	for _, err := range []error{context.Canceled, io.ErrClosedPipe, syscall.EPIPE, syscall.ECONNRESET} {
		if !expectedLiveCodingAgentDisconnect(err) {
			t.Fatalf("disconnect error %v was not recognized", err)
		}
	}
}

func writeLiveCodingAgentGatewayError(writer http.ResponseWriter, status int) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, `{"error":{"type":"gateway_error","message":"local gateway request failed"}}`)
}

func assertLiveCodingAgentCase(
	t *testing.T,
	gateway *liveCodingAgentGateway,
	model string,
	agent liveCodingAgentCLI,
	binary string,
	prompt string,
	marker string,
	scenario liveCodingAgentScenario,
) {
	t.Helper()
	workDir := t.TempDir()
	tool := scenario != liveCodingAgentText
	if tool {
		if err := os.WriteFile(filepath.Join(workDir, "fixture.txt"), []byte(marker), 0o600); err != nil {
			t.Fatal("create temporary tool fixture failed")
		}
	}
	recordsBefore := gateway.recordCount()
	failuresBefore := gateway.failures.Load()
	output := agent.run(t, binary, gateway.server.URL, model, workDir, prompt, tool)
	if !gateway.waitForRecordsSince(recordsBefore, 15*time.Second) {
		t.Fatal("timed out waiting for local gateway handlers to finish")
	}

	want := marker
	got := strings.TrimSpace(string(output))
	if got != want {
		t.Fatalf("final text mismatch: %s", liveCodingAgentFingerprint(output))
	}
	records := gateway.recordsSince(recordsBefore)
	requests := int64(len(records))
	if requests < 1 {
		t.Fatal("CLI completed without making a gateway request")
	}
	if tool && requests < 2 {
		t.Fatalf("tool loop made %d gateway requests, want at least 2", requests)
	}
	assertLiveCodingAgentTranscript(t, agent.name, scenario, marker, records)
	if failures := gateway.failures.Load() - failuresBefore; failures != 0 {
		t.Fatalf("local gateway recorded %d failed requests", failures)
	}
	t.Logf(
		"verified agent=%s scenario=%s requests=%d output_%s paths=%v diagnostics=%v correlated_tool_evidence=%v",
		agent.name,
		scenario,
		requests,
		liveCodingAgentFingerprint(output),
		uniqueLiveCodingAgentPaths(records),
		uniqueLiveCodingAgentDiagnosticCodes(records),
		tool,
	)
}

type liveCodingAgentToolFact struct {
	kind      string
	callID    string
	name      string
	arguments string
	payload   string
	isError   bool
}

func assertLiveCodingAgentTranscript(t *testing.T, agent string, scenario liveCodingAgentScenario, marker string, records []liveCodingAgentRequest) {
	t.Helper()
	wantPath := map[string]string{
		"claude": "/v1/messages",
		"gemini": "/v1beta/models/",
		"codex":  "/v1/responses",
	}[agent]
	if wantPath == "" {
		t.Fatalf("no transcript validator for agent %q", agent)
	}
	var facts []liveCodingAgentToolFact
	var diagnosticCodes []string
	requestKeys := make(map[string]bool)
	for index, record := range records {
		if (agent == "gemini" && !strings.HasPrefix(record.path, wantPath)) || (agent != "gemini" && record.path != wantPath) {
			t.Fatalf("request[%d] path = %q, want %q", index, record.path, wantPath)
		}
		var value any
		if err := json.Unmarshal(record.body, &value); err != nil {
			t.Fatalf("request[%d] is not valid JSON: %v", index, err)
		}
		collectLiveCodingAgentToolFacts(value, &facts)
		collectLiveCodingAgentJSONKeys(value, requestKeys)
		for _, diagnostic := range record.diagnostics {
			diagnosticCodes = append(diagnosticCodes, diagnostic.Code)
		}
	}

	switch agent {
	case "claude":
		requireLiveCodingAgentDiagnostic(t, diagnosticCodes, "thinking_policy_approximated")
		requireLiveCodingAgentDiagnostic(t, diagnosticCodes, "cache_control_not_representable")
	case "gemini":
		compatibilityFields := map[string]string{
			"topK":            "gemini_top_k_ignored",
			"includeThoughts": "gemini_include_thoughts_not_preserved",
			"thinkingBudget":  "gemini_thinking_budget_not_preserved",
		}
		observedCompatibilityField := false
		for field, diagnostic := range compatibilityFields {
			if !requestKeys[field] {
				continue
			}
			observedCompatibilityField = true
			requireLiveCodingAgentDiagnostic(t, diagnosticCodes, diagnostic)
		}
		if !observedCompatibilityField {
			t.Fatal("Gemini request did not exercise a coding-agent compatibility field")
		}
	}
	if scenario == liveCodingAgentText {
		for _, fact := range facts {
			if strings.HasSuffix(fact.kind, "_call") || strings.HasSuffix(fact.kind, "_result") {
				t.Fatalf("text-only scenario unexpectedly contained %s evidence", fact.kind)
			}
		}
		return
	}

	target := "fixture.txt"
	if scenario == liveCodingAgentToolError {
		target = "missing-routemorph-fixture.txt"
	}
	wantToolNames := map[string]map[string]bool{
		"claude": {"Read": true},
		"gemini": {"read_file": true},
		"codex":  {"exec_command": true},
	}[agent]
	callIDs := make(map[string]liveCodingAgentToolFact)
	for _, fact := range facts {
		if fact.kind == agent+"_call" && fact.callID != "" && wantToolNames[fact.name] && strings.Contains(fact.arguments, target) {
			callIDs[fact.callID] = fact
		}
	}
	if len(callIDs) == 0 {
		t.Fatalf("no %s call targeted %q with an expected tool name; observed facts=%s", agent, target, fingerprintLiveCodingAgentToolFacts(facts))
	}
	for _, fact := range facts {
		if fact.kind != agent+"_result" || fact.callID == "" {
			continue
		}
		if _, ok := callIDs[fact.callID]; !ok {
			continue
		}
		if scenario == liveCodingAgentReadFile && strings.Contains(fact.payload, marker) {
			return
		}
		if scenario == liveCodingAgentToolError {
			switch agent {
			case "claude":
				if fact.isError {
					requireLiveCodingAgentDiagnostic(t, diagnosticCodes, "tool_result_error_state_not_representable")
					return
				}
			case "gemini":
				if fact.isError {
					requireLiveCodingAgentDiagnostic(t, diagnosticCodes, "gemini_function_error_state_not_representable")
					return
				}
			case "codex":
				failureText := strings.ToLower(fact.payload)
				if strings.Contains(fact.payload, "missing-routemorph-fixture.txt") &&
					(strings.Contains(failureText, "no such file") || strings.Contains(failureText, "not found") || strings.Contains(failureText, "does not exist") || strings.Contains(failureText, "exited with code 1")) {
					return
				}
			}
		}
	}
	t.Fatalf("no correlated %s tool call/result evidence found in %d %s request bodies", scenario, len(records), agent)
}

func collectLiveCodingAgentJSONKeys(value any, keys map[string]bool) {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			collectLiveCodingAgentJSONKeys(child, keys)
		}
	case map[string]any:
		for key, child := range value {
			keys[key] = true
			collectLiveCodingAgentJSONKeys(child, keys)
		}
	}
}

func collectLiveCodingAgentToolFacts(value any, facts *[]liveCodingAgentToolFact) {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			collectLiveCodingAgentToolFacts(child, facts)
		}
	case map[string]any:
		typeName, _ := value["type"].(string)
		switch typeName {
		case "tool_use":
			*facts = append(*facts, liveCodingAgentToolFact{
				kind:      "claude_call",
				callID:    stringValue(value["id"]),
				name:      stringValue(value["name"]),
				arguments: compactLiveCodingAgentJSON(value["input"]),
			})
		case "tool_result":
			*facts = append(*facts, liveCodingAgentToolFact{kind: "claude_result", callID: stringValue(value["tool_use_id"]), payload: compactLiveCodingAgentJSON(value["content"]), isError: boolValue(value["is_error"])})
		case "function_call":
			*facts = append(*facts, liveCodingAgentToolFact{
				kind:      "codex_call",
				callID:    stringValue(value["call_id"]),
				name:      stringValue(value["name"]),
				arguments: compactLiveCodingAgentJSON(value["arguments"]),
			})
		case "function_call_output":
			*facts = append(*facts, liveCodingAgentToolFact{kind: "codex_result", callID: stringValue(value["call_id"]), payload: compactLiveCodingAgentJSON(value["output"])})
		}
		if call, ok := value["functionCall"].(map[string]any); ok {
			*facts = append(*facts, liveCodingAgentToolFact{
				kind:      "gemini_call",
				callID:    stringValue(call["id"]),
				name:      stringValue(call["name"]),
				arguments: compactLiveCodingAgentJSON(call["args"]),
			})
		}
		if result, ok := value["functionResponse"].(map[string]any); ok {
			response, _ := result["response"].(map[string]any)
			_, hasError := response["error"]
			*facts = append(*facts, liveCodingAgentToolFact{kind: "gemini_result", callID: stringValue(result["id"]), payload: compactLiveCodingAgentJSON(response), isError: hasError})
		}
		for _, child := range value {
			collectLiveCodingAgentToolFacts(child, facts)
		}
	}
}

func fingerprintLiveCodingAgentToolFacts(facts []liveCodingAgentToolFact) string {
	type safeFact struct {
		Kind           string `json:"kind"`
		Name           string `json:"name,omitempty"`
		HasCallID      bool   `json:"has_call_id"`
		ArgumentsBytes int    `json:"arguments_bytes,omitempty"`
		PayloadBytes   int    `json:"payload_bytes,omitempty"`
		IsError        bool   `json:"is_error,omitempty"`
	}
	safe := make([]safeFact, 0, len(facts))
	for _, fact := range facts {
		safe = append(safe, safeFact{
			Kind:           fact.kind,
			Name:           fact.name,
			HasCallID:      fact.callID != "",
			ArgumentsBytes: len(fact.arguments),
			PayloadBytes:   len(fact.payload),
			IsError:        fact.isError,
		})
	}
	encoded, _ := json.Marshal(safe)
	return string(encoded)
}

func requireLiveCodingAgentDiagnostic(t *testing.T, codes []string, want string) {
	t.Helper()
	for _, code := range codes {
		if code == want {
			return
		}
	}
	t.Fatalf("missing conversion diagnostic %q; observed=%v", want, codes)
}

func uniqueLiveCodingAgentPaths(records []liveCodingAgentRequest) []string {
	values := make(map[string]struct{}, len(records))
	for _, record := range records {
		values[record.path] = struct{}{}
	}
	return sortedLiveCodingAgentValues(values)
}

func uniqueLiveCodingAgentDiagnosticCodes(records []liveCodingAgentRequest) []string {
	values := make(map[string]struct{})
	for _, record := range records {
		for _, diagnostic := range record.diagnostics {
			values[diagnostic.Code] = struct{}{}
		}
	}
	return sortedLiveCodingAgentValues(values)
}

func sortedLiveCodingAgentValues(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func compactLiveCodingAgentJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func runLiveClaudeCode(t *testing.T, binary, gatewayURL, model, workDir, prompt string, tool bool) []byte {
	t.Helper()
	stateDir, environment := isolatedLiveCodingAgentEnvironment(t, workDir)
	environment = append(environment,
		"ANTHROPIC_BASE_URL="+gatewayURL,
		"ANTHROPIC_API_KEY=sk-ant-"+liveCodingAgentDummyCredential,
		"CLAUDE_CONFIG_DIR="+filepath.Join(stateDir, "claude"),
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_DISABLE_AUTOUPDATER=1",
		"DISABLE_TELEMETRY=1",
		"DISABLE_ERROR_REPORTING=1",
	)
	args := []string{
		"--bare",
		"--no-session-persistence",
		"--output-format", "text",
		"--model", model,
		"--print",
		"--permission-mode", "dontAsk",
	}
	if tool {
		args = append(args, "--tools", "Read", "--allowedTools", "Read")
	} else {
		args = append(args, "--tools", "")
	}
	// --tools/--allowedTools accept a variable number of values; a following
	// flag terminates that list so the final argument is parsed as the prompt.
	args = append(args, "--system-prompt", "Follow the user instruction exactly.")
	args = append(args, prompt)
	return executeLiveCodingAgentCLI(t, binary, args, environment, workDir, stateDir)
}

func runLiveGeminiCLI(t *testing.T, binary, gatewayURL, model, workDir, prompt string, _ bool) []byte {
	t.Helper()
	stateDir, environment := isolatedLiveCodingAgentEnvironment(t, workDir)
	environment = append(environment,
		"GOOGLE_GEMINI_BASE_URL="+gatewayURL,
		"GEMINI_API_KEY="+liveCodingAgentDummyCredential,
		"GEMINI_CLI_SYSTEM_SETTINGS_PATH="+filepath.Join(stateDir, "gemini-system-settings.json"),
		"GEMINI_CLI_SYSTEM_DEFAULTS_PATH="+filepath.Join(stateDir, "gemini-system-defaults.json"),
		"GOOGLE_GENAI_USE_VERTEXAI=false",
		"GOOGLE_GENAI_USE_GCA=false",
		"GEMINI_CLI_NO_RELAUNCH=true",
		"OTEL_SDK_DISABLED=true",
	)
	args := []string{
		"--model", model,
		"--output-format", "text",
		"--extensions", "none",
		"--allowed-mcp-server-names", "routemorph-no-mcp-server",
		"--approval-mode", "yolo",
		"--allowed-tools", "read_file",
	}
	args = append(args, "--prompt", prompt)
	return executeLiveCodingAgentCLI(t, binary, args, environment, workDir, stateDir)
}

func runLiveCodexCLI(t *testing.T, binary, gatewayURL, model, workDir, prompt string, _ bool) []byte {
	t.Helper()
	stateDir, environment := isolatedLiveCodingAgentEnvironment(t, workDir)
	environment = append(environment,
		"ROUTEMORPH_DUMMY_API_KEY="+liveCodingAgentDummyCredential,
	)
	provider := "model_providers.routemorph={" +
		"name=\"RouteMorph local test\"," +
		"base_url=" + strconv.Quote(gatewayURL+"/v1") + "," +
		"env_key=\"ROUTEMORPH_DUMMY_API_KEY\"," +
		"wire_api=\"responses\"," +
		"requires_openai_auth=false," +
		"supports_websockets=false}"
	lastMessage := filepath.Join(stateDir, "codex-last-message.txt")
	args := []string{
		"exec",
		"--ignore-user-config",
		"--ignore-rules",
		"--ephemeral",
		"--skip-git-repo-check",
		"--color", "never",
		"--sandbox", "read-only",
		"--cd", workDir,
		"--model", model,
		"--output-last-message", lastMessage,
		"--config", `model_provider="routemorph"`,
		"--config", provider,
		"--config", `approval_policy="never"`,
		"--config", `model_reasoning_effort="low"`,
		prompt,
	}
	_ = executeLiveCodingAgentCLI(t, binary, args, environment, workDir, stateDir)
	output, err := os.ReadFile(lastMessage)
	if err != nil {
		t.Fatal("Codex did not produce its final-message file")
	}
	return output
}

func isolatedLiveCodingAgentEnvironment(t *testing.T, workDir string) (string, []string) {
	t.Helper()
	stateDir := t.TempDir()
	paths := []string{
		filepath.Join(stateDir, "tmp"),
		filepath.Join(stateDir, "config"),
		filepath.Join(stateDir, "cache"),
		filepath.Join(stateDir, "data"),
		filepath.Join(stateDir, "claude"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal("create isolated CLI state failed")
		}
	}
	settings := []byte(`{
  "general": {"disableAutoUpdate": true, "disableUpdateNag": true},
  "privacy": {"usageStatisticsEnabled": false},
  "telemetry": {"enabled": false},
  "security": {"folderTrust": {"enabled": false}},
  "context": {"fileName": "ROUTEMORPH_LIVE_NO_CONTEXT.md"},
  "tools": {"core": ["read_file"], "allowed": ["read_file"], "enableHooks": false, "useRipgrep": false}
}
`)
	for _, path := range []string{
		filepath.Join(stateDir, "gemini-system-settings.json"),
		filepath.Join(stateDir, "gemini-system-defaults.json"),
	} {
		if err := os.WriteFile(path, settings, 0o600); err != nil {
			t.Fatal("create isolated Gemini settings failed")
		}
	}
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"PWD=" + workDir,
		"TMPDIR=" + filepath.Join(stateDir, "tmp"),
		"XDG_CONFIG_HOME=" + filepath.Join(stateDir, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(stateDir, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(stateDir, "data"),
		"SHELL=/bin/sh",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TERM=dumb",
		"CI=1",
		"NO_COLOR=1",
		"NO_PROXY=127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
	}
	return stateDir, environment
}

func executeLiveCodingAgentCLI(t *testing.T, binary string, args, environment []string, workDir, stateDir string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveCodingAgentTimeout)
	defer cancel()
	bubblewrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Fatal("bwrap is required to isolate real CLI tests from the user's home directory")
	}
	home := os.Getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		t.Fatal("HOME must be a non-empty absolute path for the live CLI sandbox")
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil || !filepath.IsAbs(realHome) {
		t.Fatal("resolve HOME for the live CLI sandbox failed")
	}
	sandboxArgs := []string{
		"--die-with-parent",
		"--ro-bind", "/", "/",
		"--dev-bind", "/dev", "/dev",
		"--proc", "/proc",
		"--bind", stateDir, stateDir,
		"--bind", workDir, workDir,
		"--bind", stateDir, realHome,
		"--chdir", workDir,
		"--",
		binary,
	}
	sandboxArgs = append(sandboxArgs, args...)
	command := exec.CommandContext(ctx, bubblewrap, sandboxArgs...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 5 * time.Second
	command.Dir = workDir
	command.Env = environment
	command.Stdin = strings.NewReader("")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if err != nil {
		exitCode := -1
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}
		t.Fatalf(
			"CLI process failed: timed_out=%v exit_code=%d stdout_%s stderr_%s",
			ctx.Err() != nil,
			exitCode,
			liveCodingAgentFingerprint(stdout.Bytes()),
			liveCodingAgentFingerprint(stderr.Bytes()),
		)
	}
	return append([]byte(nil), stdout.Bytes()...)
}

func liveCodingAgentFingerprint(value []byte) string {
	digest := sha256.Sum256(value)
	return "bytes=" + strconv.Itoa(len(value)) + " sha256=" + hex.EncodeToString(digest[:8])
}
