package routekit

import (
	"encoding/json"
	"errors"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestRejectUnknownTopLevelReportsCanonicalPath(t *testing.T) {
	err := RejectUnknownTopLevel(core.ProtocolChat, []byte(`{"model":"x","future":true}`), "model")
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
	var conversion *core.ConversionError
	if !errors.As(err, &conversion) || conversion.Path != "$.future" {
		t.Fatalf("conversion error = %#v", conversion)
	}
}

func TestMediaAndPresenceHelpers(t *testing.T) {
	media, err := ParseDataURL("data:text/plain;base64,aGk=")
	if err != nil {
		t.Fatalf("ParseDataURL() error = %v", err)
	}
	if media.MIMEType != "text/plain" || media.Data != "aGk=" || DataURL(media) != "data:text/plain;base64,aGk=" {
		t.Fatalf("media = %#v", media)
	}
	if ValuePresent(json.RawMessage(` {}`)) || NonNullValue(json.RawMessage(` null `)) {
		t.Fatal("empty JSON values unexpectedly reported as present")
	}
}

func TestParseDataURLRejectsMalformedInput(t *testing.T) {
	for _, value := range []string{
		"data:image/png;base64",
		"data:image/png,not-base64",
		"data:image/png;base64,%%%",
	} {
		if _, err := ParseDataURL(value); err == nil {
			t.Fatalf("ParseDataURL(%q) unexpectedly succeeded", value)
		}
	}
}

func TestParseFileDataCarriesMediaType(t *testing.T) {
	media, err := ParseFileData("JVBERg", "brief.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if media.Data != "JVBERg" || media.MIMEType != "application/pdf" || media.Filename != "brief.pdf" {
		t.Fatalf("media = %#v", media)
	}
	media, err = ParseFileData("data:text/plain;base64,aGk=", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if media.Data != "aGk=" || media.MIMEType != "text/plain" || media.Filename != "note.txt" {
		t.Fatalf("data URL media = %#v", media)
	}
}

func TestValidateResponsesDiscriminatedUnionsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name    string
		run     func() error
		path    string
		wantErr error
	}{
		{
			name: "unknown content field",
			run: func() error {
				return ValidateResponsesContentArray(core.ProtocolResponses, json.RawMessage(`[{"type":"input_text","text":"ok","vendor_state":true}]`), "$.input[0].content")
			},
			path:    "$.input[0].content[0].vendor_state",
			wantErr: core.ErrUnsupported,
		},
		{
			name: "object tool output",
			run: func() error {
				return ValidateResponsesToolOutput(core.ProtocolResponses, json.RawMessage(`{"ok":true}`), "$.input[1].output")
			},
			path:    "$.input[1].output",
			wantErr: core.ErrInvalidPayload,
		},
		{
			name: "mixed item fields",
			run: func() error {
				return ValidateResponsesInputItems(core.ProtocolResponses, json.RawMessage(`[{"type":"function_call_output","call_id":"call","output":"ok","arguments":"{}"}]`), "$.input")
			},
			path:    "$.input[0].arguments",
			wantErr: core.ErrUnsupported,
		},
		{
			name: "unknown tool field",
			run: func() error {
				return ValidateResponsesTools(core.ProtocolResponses, []byte(`{"tools":[{"type":"function","name":"f","parameters":{},"vendor_state":true}]}`))
			},
			path:    "$.tools[0].vendor_state",
			wantErr: core.ErrUnsupported,
		},
		{
			name: "unknown output part field",
			run: func() error {
				return ValidateResponsesOutputItems(core.ProtocolResponses, []byte(`{"output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","vendor_state":true}]}]}`))
			},
			path:    "$.output[0].content[0].vendor_state",
			wantErr: core.ErrUpstreamResponse,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v, want %v", err, test.wantErr)
			}
			var conversion *core.ConversionError
			if !errors.As(err, &conversion) || conversion.Path != test.path {
				t.Fatalf("conversion error=%#v, want path %s", conversion, test.path)
			}
		})
	}
}

func TestValidateResponsesContentRequiredFieldsAndTypes(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		path string
	}{
		{name: "input text missing text", raw: `[{
			"type":"input_text"
		}]`, path: "$.content[0].text"},
		{name: "input text null text", raw: `[{"type":"input_text","text":null}]`, path: "$.content[0].text"},
		{name: "input text numeric text", raw: `[{"type":"input_text","text":1}]`, path: "$.content[0].text"},
		{name: "output text null annotations", raw: `[{"type":"output_text","text":"ok","annotations":null}]`, path: "$.content[0].annotations"},
		{name: "output text object annotations", raw: `[{"type":"output_text","text":"ok","annotations":{}}]`, path: "$.content[0].annotations"},
		{name: "input image missing source", raw: `[{"type":"input_image","detail":"auto"}]`, path: "$.content[0]"},
		{name: "input image numeric source", raw: `[{"type":"input_image","image_url":3}]`, path: "$.content[0].image_url"},
		{name: "input image relative URL", raw: `[{"type":"input_image","image_url":"images/cat.png"}]`, path: "$.content[0].image_url"},
		{name: "input image malformed data", raw: `[{"type":"input_image","image_url":"data:image/png;base64,%%%"}]`, path: "$.content[0].image_url"},
		{name: "input file multiple sources", raw: `[{"type":"input_file","file_id":"f","file_url":"https://example.test/a"}]`, path: "$.content[0]"},
		{name: "input file relative URL", raw: `[{"type":"input_file","file_url":"files/a.pdf"}]`, path: "$.content[0].file_url"},
		{name: "input file malformed base64", raw: `[{"type":"input_file","file_data":"%%%","filename":"a.pdf"}]`, path: "$.content[0].file_data"},
		{name: "audio missing format", raw: `[{"type":"input_audio","input_audio":{"data":"YQ=="}}]`, path: "$.content[0].input_audio.format"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateResponsesContentArray(core.ProtocolResponses, json.RawMessage(test.raw), "$.content")
			assertConversionError(t, err, core.ErrInvalidPayload, test.path)
		})
	}

	if err := ValidateResponsesContentArray(core.ProtocolResponses, json.RawMessage(`[{"type":"output_text","text":"ok","annotations":[]}]`), "$.content", "output_text"); err != nil {
		t.Fatalf("valid output_text error = %v", err)
	}
	if err := ValidateResponsesContentArray(core.ProtocolResponses, json.RawMessage(`[{"type":"output_text","text":"history"}]`), "$.content", "output_text"); err != nil {
		t.Fatalf("input-history output_text without annotations error = %v", err)
	}
	if err := ValidateResponsesContentArray(core.ProtocolResponses, json.RawMessage(`[{"type":"input_image","image_url":"data:image/png;base64,YQ=="},{"type":"input_file","file_data":"YQ==","filename":"a.pdf"}]`), "$.content"); err != nil {
		t.Fatalf("valid inline media error = %v", err)
	}
}

func TestValidateResponsesToolOutputRequiredFieldsAndTypes(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		path string
	}{
		{name: "null", raw: `null`, path: "$.output"},
		{name: "part missing text", raw: `[{"type":"input_text"}]`, path: "$.output[0].text"},
		{name: "part null text", raw: `[{"type":"input_text","text":null}]`, path: "$.output[0].text"},
		{name: "part wrong source type", raw: `[{"type":"input_file","file_data":false}]`, path: "$.output[0].file_data"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateResponsesToolOutput(core.ProtocolResponses, json.RawMessage(test.raw), "$.output")
			assertConversionError(t, err, core.ErrInvalidPayload, test.path)
		})
	}
}

func TestValidateResponsesOutputRequiresArray(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "missing", raw: `{}`},
		{name: "null", raw: `{"output":null}`},
		{name: "object", raw: `{"output":{}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateResponsesOutputItems(core.ProtocolResponses, []byte(test.raw))
			assertConversionError(t, err, core.ErrUpstreamResponse, "$.output")
		})
	}
	if err := ValidateResponsesOutputItems(core.ProtocolResponses, []byte(`{"output":[]}`)); err != nil {
		t.Fatalf("empty output array error = %v", err)
	}
}

func TestValidateResponsesOutputItemOfficialToolVariants(t *testing.T) {
	response := []byte(`{
		"output":[
			{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{}","caller":{"type":"direct"}},
			{"type":"function_call_output","id":"fco_1","status":"completed","output":"sunny","created_by":"client"},
			{"type":"custom_tool_call","call_id":"call_2","name":"shell","input":"pwd","caller":{"type":"program","caller_id":"prog_1"}},
			{"type":"custom_tool_call_output","id":"ctco_1","call_id":"call_2","output":[{"type":"input_text","text":"/tmp"}],"status":"completed","created_by":"client"},
			{"type":"tool_search_call","id":"tsc_1","call_id":"call_3","arguments":{},"execution":"server","status":"completed","created_by":"model"},
			{"type":"tool_search_output","id":"tso_1","call_id":"call_3","execution":"server","status":"completed","tools":[{"type":"function","name":"weather","parameters":{},"strict":true}],"created_by":"server"},
			{"type":"additional_tools","id":"at_1","role":"developer","tools":[]}
		]
	}`)
	if err := ValidateResponsesOutputItems(core.ProtocolResponses, response); err != nil {
		t.Fatalf("official tool variants error = %v", err)
	}
}

func TestValidateResponsesOutputItemRequiredFieldsAndTypes(t *testing.T) {
	for _, test := range []struct {
		name string
		item string
		path string
	}{
		{name: "message content null", item: `{"type":"message","id":"m","role":"assistant","status":"completed","content":null}`, path: "$.output[0].content"},
		{name: "message annotations missing", item: `{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}`, path: "$.output[0].content[0].annotations"},
		{name: "function arguments object", item: `{"type":"function_call","id":"f","call_id":"c","name":"n","arguments":{},"status":"completed"}`, path: "$.output[0].arguments"},
		{name: "function created by is not in schema", item: `{"type":"function_call","call_id":"c","name":"n","arguments":"{}","created_by":"model"}`, path: "$.output[0].created_by"},
		{name: "function output null", item: `{"type":"function_call_output","id":"f","status":"completed","output":null}`, path: "$.output[0].output"},
		{name: "custom status is not in schema", item: `{"type":"custom_tool_call","call_id":"c","name":"n","input":"raw","status":"completed"}`, path: "$.output[0].status"},
		{name: "custom created by is not in schema", item: `{"type":"custom_tool_call","call_id":"c","name":"n","input":"raw","created_by":"model"}`, path: "$.output[0].created_by"},
		{name: "custom output call id missing", item: `{"type":"custom_tool_call_output","id":"c","status":"completed","output":"ok"}`, path: "$.output[0].call_id"},
		{name: "created by wrong type", item: `{"type":"tool_search_call","id":"t","call_id":"c","arguments":{},"execution":"server","status":"completed","created_by":1}`, path: "$.output[0].created_by"},
		{name: "tool search tools null", item: `{"type":"tool_search_output","id":"t","call_id":"c","execution":"server","status":"completed","tools":null}`, path: "$.output[0].tools"},
		{name: "tool search function missing schema", item: `{"type":"tool_search_output","id":"t","call_id":"c","execution":"server","status":"completed","tools":[{"type":"function","name":"lookup"}]}`, path: "$.output[0].tools[0].parameters"},
		{name: "tool search unknown tool", item: `{"type":"tool_search_output","id":"t","call_id":"c","execution":"server","status":"completed","tools":[{"type":"vendor_future"}]}`, path: "$.output[0].tools[0].type"},
		{name: "web action missing", item: `{"type":"web_search_call","id":"w","status":"completed"}`, path: "$.output[0].action"},
		{name: "function output invalid image source", item: `{"type":"function_call_output","id":"f","status":"completed","output":[{"type":"input_image","image_url":"relative.png"}]}`, path: "$.output[0].output[0].image_url"},
		{name: "custom output invalid file data", item: `{"type":"custom_tool_call_output","id":"c","call_id":"call_1","status":"completed","output":[{"type":"input_file","file_data":"%%%"}]}`, path: "$.output[0].output[0].file_data"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := []byte(`{"output":[` + test.item + `]}`)
			err := ValidateResponsesOutputItems(core.ProtocolResponses, response)
			assertConversionError(t, err, core.ErrUpstreamResponse, test.path)
		})
	}
}

func TestValidateResponsesProviderToolDefinitionsV356(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"function","name":"lookup","parameters":{},"strict":true,"allowed_callers":["direct","programmatic"],"async":true,"defer_loading":false,"description":null,"output_schema":{"type":"string"},"vendor_extension":{"kept":true}},
		{"type":"custom","name":"query","format":{"type":"grammar","definition":"start: WORD","syntax":"lark"},"description":"free form"},
		{"type":"namespace","description":"CRM tools","name":"crm","tools":[
			{"type":"function","name":"find","parameters":null,"strict":null,"description":null,"output_schema":null},
			{"type":"custom","name":"dsl","format":{"type":"text"}}
		]},
		{"type":"file_search","vector_store_ids":["vs_1"],"filters":{"type":"and","filters":[{"type":"eq","key":"category","value":"docs"}]},"max_num_results":50,"ranking_options":{"ranker":"auto","score_threshold":0.5,"hybrid_search":{"embedding_weight":0.7,"text_weight":0.3}}},
		{"type":"computer_use_preview","display_height":768,"display_width":1024,"environment":"browser"},
		{"type":"mcp","server_label":"docs","server_url":"https://example.test/mcp","allowed_callers":null,"allowed_tools":{"read_only":true,"tool_names":["search"]},"authorization":"token","defer_loading":true,"headers":{"X-Test":"yes"},"require_approval":{"always":{"tool_names":["write"]}},"server_description":"docs"},
		{"type":"code_interpreter","container":{"type":"auto","file_ids":["file_1"],"memory_limit":"4g","network_policy":{"type":"allowlist","allowed_domains":["example.test"],"domain_secrets":[{"domain":"example.test","name":"TOKEN","value":"secret"}]}},"allowed_callers":["direct"]},
		{"type":"web_search","external_web_access":false,"filters":{"allowed_domains":["example.test"]},"search_context_size":"high","user_location":{"type":"approximate","city":"Paris","country":null}},
		{"type":"web_search_preview_2025_03_11","search_content_types":["text","image"],"search_context_size":"low","user_location":{"type":"approximate","timezone":"UTC"}},
		{"type":"image_generation","action":"edit","background":"transparent","input_fidelity":null,"input_image_mask":{"file_id":"file_1"},"model":"gpt-image-2","moderation":"low","output_compression":90,"output_format":"png","partial_images":3,"quality":"high","size":"1536x864"},
		{"type":"shell","allowed_callers":["programmatic"],"environment":{"type":"container_reference","container_id":"container_1"}},
		{"type":"tool_search","description":null,"execution":"client","parameters":{"type":"object"}},
		{"type":"apply_patch","allowed_callers":["direct"]},
		{"type":"computer"},
		{"type":"programmatic_tool_calling"},
		{"type":"local_shell"}
	]`)
	if err := ValidateResponsesProviderToolDefinitions(core.ProtocolResponses, raw, "$.tools"); err != nil {
		t.Fatalf("v3.56 tool union error = %v", err)
	}
}

func TestValidateResponsesProviderToolDefinitionsRejectMalformedKnownFields(t *testing.T) {
	for _, test := range []struct {
		name string
		tool string
		path string
	}{
		{name: "function async type", tool: `{"type":"function","name":"f","parameters":{},"strict":true,"async":"yes"}`, path: "$.tools[0].async"},
		{name: "custom grammar syntax", tool: `{"type":"custom","name":"dsl","format":{"type":"grammar","definition":"x","syntax":"peg"}}`, path: "$.tools[0].format.syntax"},
		{name: "namespace nested strict type", tool: `{"type":"namespace","description":"n","name":"n","tools":[{"type":"function","name":"f","strict":"yes"}]}`, path: "$.tools[0].tools[0].strict"},
		{name: "mcp endpoint missing", tool: `{"type":"mcp","server_label":"docs"}`, path: "$.tools[0]"},
		{name: "mcp endpoints overlap", tool: `{"type":"mcp","server_label":"docs","server_url":"https://example.test","tunnel_id":"tun_1"}`, path: "$.tools[0]"},
		{name: "mcp connector enum", tool: `{"type":"mcp","server_label":"docs","connector_id":"connector_future"}`, path: "$.tools[0].connector_id"},
		{name: "mcp header value", tool: `{"type":"mcp","server_label":"docs","server_url":"https://example.test","headers":{"X-Test":1}}`, path: "$.tools[0].headers.X-Test"},
		{name: "code interpreter container type", tool: `{"type":"code_interpreter","container":{"type":"manual"}}`, path: "$.tools[0].container.type"},
		{name: "file search max", tool: `{"type":"file_search","vector_store_ids":["vs_1"],"max_num_results":51}`, path: "$.tools[0].max_num_results"},
		{name: "file search filter", tool: `{"type":"file_search","vector_store_ids":["vs_1"],"filters":{"type":"future"}}`, path: "$.tools[0].filters.type"},
		{name: "file search ranker", tool: `{"type":"file_search","vector_store_ids":["vs_1"],"ranking_options":{"ranker":"future"}}`, path: "$.tools[0].ranking_options.ranker"},
		{name: "image action", tool: `{"type":"image_generation","action":"transform"}`, path: "$.tools[0].action"},
		{name: "shell environment", tool: `{"type":"shell","environment":{"type":"remote"}}`, path: "$.tools[0].environment.type"},
		{name: "tool search execution", tool: `{"type":"tool_search","execution":"edge"}`, path: "$.tools[0].execution"},
		{name: "web search boolean", tool: `{"type":"web_search","external_web_access":"yes"}`, path: "$.tools[0].external_web_access"},
		{name: "web preview content type", tool: `{"type":"web_search_preview","search_content_types":["video"]}`, path: "$.tools[0].search_content_types[0]"},
		{name: "apply patch caller", tool: `{"type":"apply_patch","allowed_callers":["background"]}`, path: "$.tools[0].allowed_callers[0]"},
		{name: "unknown discriminator", tool: `{"type":"future_tool"}`, path: "$.tools[0].type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateResponsesProviderToolDefinitions(core.ProtocolResponses, json.RawMessage(`[`+test.tool+`]`), "$.tools")
			assertConversionError(t, err, core.ErrUpstreamResponse, test.path)
		})
	}
}

func assertConversionError(t *testing.T, err error, kind error, path string) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("error = %v, want %v", err, kind)
	}
	var conversion *core.ConversionError
	if !errors.As(err, &conversion) || conversion.Path != path {
		t.Fatalf("conversion error = %#v, want path %s", conversion, path)
	}
}
