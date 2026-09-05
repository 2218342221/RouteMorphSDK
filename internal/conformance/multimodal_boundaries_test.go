package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestChatInputAudioRequiresValidBase64WAVOrMP3(t *testing.T) {
	converter := newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})
	for _, test := range []struct {
		name        string
		data        string
		format      string
		wantMIME    string
		wantErr     error
		wantErrPath string
	}{
		{name: "wav", data: "UklGRg==", format: "wav", wantMIME: "audio/wav"},
		{name: "mp3", data: "SUQz", format: "mp3", wantMIME: "audio/mpeg"},
		{name: "invalid base64", data: "***", format: "wav", wantErr: ErrInvalidPayload, wantErrPath: "$.messages[0].content[0].input_audio.data"},
		{name: "ogg is not rewritten", data: "T2dnUw==", format: "ogg", wantErr: ErrUnsupported, wantErrPath: "$.messages[0].content[0].input_audio.format"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"model":"chat","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"` + test.data + `","format":"` + test.format + `"}}]}]}`)
			result, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{LossPolicy: allowDocumentedLoss, Exchange: exchangeMetadata{UpstreamModel: "gemini"}})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) || !strings.Contains(err.Error(), test.wantErrPath) {
					t.Fatalf("error = %v, want %v at %s", err, test.wantErr, test.wantErrPath)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var request geminiRequest
			if err := json.Unmarshal(result.Body, &request); err != nil {
				t.Fatal(err)
			}
			if len(request.Contents) != 1 || len(request.Contents[0].Parts) != 1 || request.Contents[0].Parts[0].InlineData == nil {
				t.Fatalf("converted request = %s", result.Body)
			}
			media := request.Contents[0].Parts[0].InlineData
			if media.MIMEType != test.wantMIME || media.Data != test.data {
				t.Fatalf("inlineData = %#v, want MIME %q and unchanged data %q", media, test.wantMIME, test.data)
			}
		})
	}
}

func TestChatImageUsesOfficialContentPartShape(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		part    string
		wantErr error
	}{
		{name: "responses type is not a chat type", part: `{"type":"input_image","image_url":{"url":"data:image/png;base64,aW1n"}}`, wantErr: ErrUnsupported},
		{name: "image_url must be an object", part: `{"type":"image_url","image_url":"data:image/png;base64,aW1n"}`, wantErr: ErrInvalidPayload},
	}
	for _, target := range []Protocol{ProtocolResponses, ProtocolMessages, ProtocolGenerateContent} {
		for _, test := range tests {
			t.Run(string(target)+"/"+test.name, func(t *testing.T) {
				body := []byte(`{"model":"chat","messages":[{"role":"user","content":[` + test.part + `]}]}`)
				_, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, target, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
				if !errors.Is(err, test.wantErr) || !strings.Contains(err.Error(), "$.messages[0].content[0]") {
					t.Fatalf("error = %v, want %v at content part", err, test.wantErr)
				}
			})
		}
	}
}

func TestChatMediaIsRejectedOutsideUserMessages(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []Protocol{ProtocolResponses, ProtocolMessages, ProtocolGenerateContent} {
		for _, role := range []string{"system", "developer", "assistant", "tool"} {
			for _, media := range []struct {
				name string
				part string
			}{
				{name: "image", part: `{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1n"}}`},
				{name: "audio", part: `{"type":"input_audio","input_audio":{"data":"UklGRg==","format":"wav"}}`},
				{name: "file", part: `{"type":"file","file":{"file_data":"JVBERg==","filename":"brief.pdf"}}`},
			} {
				t.Run(string(target)+"/"+role+"/"+media.name, func(t *testing.T) {
					body := []byte(`{"model":"chat","messages":[{"role":"` + role + `","content":[` + media.part + `]}]}`)
					wantPath := "$.messages[0].content[0]"
					if role == "tool" {
						body = []byte(`{"model":"chat","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":[` + media.part + `]}]}`)
						wantPath = "$.messages[1].content[0]"
					}
					_, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, target, body, conversionOptions{LossPolicy: allowDocumentedLoss, Exchange: exchangeMetadata{UpstreamModel: "provider"}})
					if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), wantPath) {
						t.Fatalf("error = %v, want ErrUnsupported at media content", err)
					}
				})
			}
		}
	}
}

func TestNonTextSystemInstructionsAreRejected(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	responsesSystemImage := []byte(`{"model":"responses","input":[{"type":"message","role":"system","content":[{"type":"input_image","image_url":"data:image/png;base64,aW1n"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
	for _, target := range []Protocol{ProtocolChat, ProtocolMessages, ProtocolGenerateContent} {
		t.Run("responses_to_"+string(target), func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, target, responsesSystemImage, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "$.input[0].content[0]") {
				t.Fatalf("error = %v, want ErrUnsupported at system image", err)
			}
		})
	}

	messagesSystemImage := []byte(`{"model":"claude","max_tokens":64,"system":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1n"}}],"messages":[{"role":"user","content":"hello"}]}`)
	_, err = harness.ToUpstreamRequest(context.Background(), ProtocolMessages, ProtocolChat, messagesSystemImage, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat"}})
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "$.system[0]") {
		t.Fatalf("error = %v, want ErrUnsupported at Messages system image", err)
	}
}

func TestResponsesCreateInputAudioIsRejectedAcrossRoutes(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	responsesAudio := []byte(`{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_audio","input_audio":{"data":"UklGRg==","format":"wav"}}]}]}`)
	for _, target := range []Protocol{ProtocolChat, ProtocolMessages, ProtocolGenerateContent} {
		t.Run("responses_to_"+string(target), func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, target, responsesAudio, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), ".type") {
				t.Fatalf("error = %v, want ErrUnsupported for Responses input_audio", err)
			}
		})
	}

	for _, test := range []struct {
		name string
		from Protocol
		body string
	}{
		{name: "chat_to_responses", from: ProtocolChat, body: `{"model":"chat","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"UklGRg==","format":"wav"}}]}]}`},
		{name: "gemini_to_responses", from: ProtocolGenerateContent, body: `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"audio/wav","data":"UklGRg=="}}]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), test.from, ProtocolResponses, []byte(test.body), conversionOptions{LossPolicy: allowDocumentedLoss, Exchange: exchangeMetadata{UpstreamModel: "responses"}})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestResponsesInputFileDetailIsNeverSilentlyDropped(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:application/pdf;base64,JVBERg==","filename":"brief.pdf","detail":"high"}]}]}`)
	for _, target := range []Protocol{ProtocolChat, ProtocolMessages, ProtocolGenerateContent} {
		t.Run(string(target), func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, target, body, conversionOptions{LossPolicy: allowDocumentedLoss, Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "detail") {
				t.Fatalf("error = %v, want ErrUnsupported mentioning detail", err)
			}
		})
	}
}

func TestResponsesOriginalImageDetailIsRejectedByChat(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,aW1n","detail":"original"}]}]}`)
	for _, policy := range []lossPolicy{rejectSemanticLoss, allowDocumentedLoss} {
		_, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolChat, body, conversionOptions{LossPolicy: policy, Exchange: exchangeMetadata{UpstreamModel: "chat"}})
		if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "detail") {
			t.Fatalf("policy=%v error=%v, want ErrUnsupported mentioning detail", policy, err)
		}
	}
}

func TestInlineFilesPreserveMIMEAcrossOpenAIAndGemini(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct {
		name string
		from Protocol
		body string
	}{
		{name: "chat", from: ProtocolChat, body: `{"model":"chat","messages":[{"role":"user","content":[{"type":"file","file":{"file_data":"data:application/pdf;base64,JVBERg=="}}]}]}`},
		{name: "responses", from: ProtocolResponses, body: `{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:application/pdf;base64,JVBERg=="}]}]}`},
	} {
		t.Run(source.name+"_to_gemini", func(t *testing.T) {
			result, err := harness.ToUpstreamRequest(context.Background(), source.from, ProtocolGenerateContent, []byte(source.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gemini"}})
			if err != nil {
				t.Fatal(err)
			}
			var request geminiRequest
			if err := json.Unmarshal(result.Result.Body, &request); err != nil {
				t.Fatal(err)
			}
			blob := request.Contents[0].Parts[0].InlineData
			if blob == nil || blob.MIMEType != "application/pdf" || blob.Data != "JVBERg==" || blob.DisplayName != "" {
				t.Fatalf("inlineData = %#v", blob)
			}
		})
	}

	gemini := []byte(`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"application/pdf","data":"JVBERg=="}}]}]}`)
	result, err := harness.ToUpstreamRequest(context.Background(), ProtocolGenerateContent, ProtocolChat, gemini, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat"}})
	if err != nil {
		t.Fatal(err)
	}
	var request chatRequest
	if err := json.Unmarshal(result.Result.Body, &request); err != nil {
		t.Fatal(err)
	}
	var parts []struct {
		Type string `json:"type"`
		File *struct {
			FileData string `json:"file_data"`
			Filename string `json:"filename"`
		} `json:"file"`
	}
	if len(request.Messages) != 1 || json.Unmarshal(request.Messages[0].Content, &parts) != nil || len(parts) != 1 || parts[0].File == nil {
		t.Fatalf("converted request = %s", result.Result.Body)
	}
	if parts[0].File.FileData != "data:application/pdf;base64,JVBERg==" || parts[0].File.Filename != "" {
		t.Fatalf("file = %#v", parts[0].File)
	}

	result, err = harness.ToUpstreamRequest(context.Background(), ProtocolGenerateContent, ProtocolResponses, gemini, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
	if err != nil {
		t.Fatal(err)
	}
	var responses responsesRequest
	if err := json.Unmarshal(result.Result.Body, &responses); err != nil {
		t.Fatal(err)
	}
	var items []responsesItem
	if err := json.Unmarshal(responses.Input, &items); err != nil || len(items) != 1 {
		t.Fatalf("converted request = %s", result.Result.Body)
	}
	var responseParts []responsesContentPart
	if err := json.Unmarshal(items[0].Content, &responseParts); err != nil || len(responseParts) != 1 {
		t.Fatalf("converted content = %s", items[0].Content)
	}
	if responseParts[0].FileData != "data:application/pdf;base64,JVBERg==" || responseParts[0].Filename != "" {
		t.Fatalf("Responses file = %#v", responseParts[0])
	}
}

func TestOpenAIFileDataPreservesDataURLAndFilename(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	chat := []byte(`{"model":"chat","messages":[{"role":"user","content":[{"type":"file","file":{"file_data":"data:application/pdf;base64,JVBERg==","filename":"brief.pdf"}}]}]}`)
	converted, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, ProtocolResponses, chat, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
	if err != nil {
		t.Fatal(err)
	}
	var responses responsesRequest
	if err := json.Unmarshal(converted.Result.Body, &responses); err != nil {
		t.Fatal(err)
	}
	var items []responsesItem
	var responseParts []responsesContentPart
	if json.Unmarshal(responses.Input, &items) != nil || len(items) != 1 || json.Unmarshal(items[0].Content, &responseParts) != nil || len(responseParts) != 1 {
		t.Fatalf("converted request = %s", converted.Result.Body)
	}
	if responseParts[0].FileData != "data:application/pdf;base64,JVBERg==" || responseParts[0].Filename != "brief.pdf" {
		t.Fatalf("Responses file = %#v", responseParts[0])
	}

	responsesBody := []byte(`{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:application/pdf;base64,JVBERg==","filename":"brief.pdf"}]}]}`)
	converted, err = harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolChat, responsesBody, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat"}})
	if err != nil {
		t.Fatal(err)
	}
	var chatOutput chatRequest
	if err := json.Unmarshal(converted.Result.Body, &chatOutput); err != nil {
		t.Fatal(err)
	}
	var chatParts []struct {
		File struct {
			FileData string `json:"file_data"`
			Filename string `json:"filename"`
		} `json:"file"`
	}
	if len(chatOutput.Messages) != 1 || json.Unmarshal(chatOutput.Messages[0].Content, &chatParts) != nil || len(chatParts) != 1 {
		t.Fatalf("converted request = %s", converted.Result.Body)
	}
	if chatParts[0].File.FileData != "data:application/pdf;base64,JVBERg==" || chatParts[0].File.Filename != "brief.pdf" {
		t.Fatalf("Chat file = %#v", chatParts[0].File)
	}
}

func TestMultimodalSourceValidationFailsClosed(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		from    Protocol
		to      Protocol
		body    string
		wantErr error
		path    string
	}{
		{name: "chat malformed file base64", from: ProtocolChat, to: ProtocolGenerateContent, body: `{"model":"chat","messages":[{"role":"user","content":[{"type":"file","file":{"file_data":"%%%","filename":"brief.pdf"}}]}]}`, wantErr: ErrInvalidPayload, path: ".file.file_data"},
		{name: "responses malformed file base64", from: ProtocolResponses, to: ProtocolGenerateContent, body: `{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_data":"%%%","filename":"brief.pdf"}]}]}`, wantErr: ErrInvalidPayload, path: ".file_data"},
		{name: "chat wrong image MIME", from: ProtocolChat, to: ProtocolResponses, body: `{"model":"chat","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:application/pdf;base64,JVBERg=="}}]}]}`, wantErr: ErrUnsupported, path: ".image_url.url"},
		{name: "responses wrong image MIME", from: ProtocolResponses, to: ProtocolChat, body: `{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:application/pdf;base64,JVBERg=="}]}]}`, wantErr: ErrUnsupported, path: ".image_url"},
		{name: "chat filename cannot target Gemini", from: ProtocolChat, to: ProtocolGenerateContent, body: `{"model":"chat","messages":[{"role":"user","content":[{"type":"file","file":{"file_data":"JVBERg==","filename":"brief.pdf"}}]}]}`, wantErr: ErrUnsupported, path: ".parts"},
		{name: "responses filename cannot target Gemini", from: ProtocolResponses, to: ProtocolGenerateContent, body: `{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_data":"JVBERg==","filename":"brief.pdf"}]}]}`, wantErr: ErrUnsupported, path: ".parts"},
		{name: "Gemini display name is not generateContent", from: ProtocolGenerateContent, to: ProtocolResponses, body: `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"application/pdf","data":"JVBERg==","displayName":"brief.pdf"}}]}]}`, wantErr: ErrUnsupported, path: ".inlineData.displayName"},
		{name: "gemini to responses unsupported image MIME", from: ProtocolGenerateContent, to: ProtocolResponses, body: `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/svg+xml","data":"PHN2Zz4="}}]}]}`, wantErr: ErrUnsupported, path: ".inlineData.mimeType"},
		{name: "gemini to responses image display name", from: ProtocolGenerateContent, to: ProtocolResponses, body: `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"aW1n","displayName":"image.png"}}]}]}`, wantErr: ErrUnsupported, path: ".inlineData.displayName"},
		{name: "gemini assistant media", from: ProtocolGenerateContent, to: ProtocolChat, body: `{"contents":[{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"aW1n"}}]}]}`, wantErr: ErrUnsupported, path: ".parts[0]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), test.from, test.to, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, test.wantErr) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want %v at %s", err, test.wantErr, test.path)
			}
		})
	}
}

func TestGeminiFileDataAcceptsOnlyCompatibleProviderURLs(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	sources := []struct {
		name     string
		from     Protocol
		body     string
		wantPath string
		filename string
	}{
		{name: "chat", from: ProtocolChat, body: `{"model":"chat","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"$URL"}}]}]}`, wantPath: "fileData.fileUri", filename: "image.png"},
		{name: "responses", from: ProtocolResponses, body: `{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_url":"$URL"}]}]}`, wantPath: "fileData.fileUri", filename: "brief.pdf"},
		{name: "messages", from: ProtocolMessages, body: `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[{"type":"document","source":{"type":"url","url":"$URL"}}]}]}`, wantPath: "source.url", filename: "brief.pdf"},
	}
	URLs := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{name: "ordinary_public_URL_rejected", baseURL: "https://example.com/", wantErr: true},
		{name: "GCS_HTTPS_accepted", baseURL: "https://storage.googleapis.com/example-bucket/"},
	}
	for _, source := range sources {
		for _, test := range URLs {
			t.Run(source.name+"/"+test.name, func(t *testing.T) {
				value := test.baseURL + source.filename
				body := []byte(strings.Replace(source.body, "$URL", value, 1))
				result, err := harness.ToUpstreamRequest(context.Background(), source.from, ProtocolGenerateContent, body, conversionOptions{LossPolicy: allowDocumentedLoss, Exchange: exchangeMetadata{UpstreamModel: "gemini"}})
				if test.wantErr {
					if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), source.wantPath) {
						t.Fatalf("error = %v, want ErrUnsupported at %s", err, source.wantPath)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var request geminiRequest
				if err := json.Unmarshal(result.Result.Body, &request); err != nil {
					t.Fatal(err)
				}
				if len(request.Contents) != 1 || len(request.Contents[0].Parts) != 1 || request.Contents[0].Parts[0].FileData == nil || request.Contents[0].Parts[0].FileData.FileURI != value {
					t.Fatalf("converted request = %s", result.Result.Body)
				}
			})
		}
	}
}

func TestGeminiFileDataIsClassifiedForResponses(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		mimeType string
		wantType string
		wantErr  error
	}{
		{name: "image", mimeType: "image/png", wantType: "input_image"},
		{name: "document", mimeType: "application/pdf", wantType: "input_file"},
		{name: "audio", mimeType: "audio/wav", wantErr: ErrUnsupported},
		{name: "unsupported image", mimeType: "image/svg+xml", wantErr: ErrUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"contents":[{"role":"user","parts":[{"fileData":{"mimeType":"` + test.mimeType + `","fileUri":"https://storage.googleapis.com/example-bucket/media"}}]}]}`)
			result, err := harness.ToUpstreamRequest(context.Background(), ProtocolGenerateContent, ProtocolResponses, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) || !strings.Contains(err.Error(), ".fileData.mimeType") {
					t.Fatalf("error = %v, want %v at fileData.mimeType", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var request responsesRequest
			if err := json.Unmarshal(result.Result.Body, &request); err != nil {
				t.Fatal(err)
			}
			var items []responsesItem
			var parts []responsesContentPart
			if json.Unmarshal(request.Input, &items) != nil || len(items) != 1 || json.Unmarshal(items[0].Content, &parts) != nil || len(parts) != 1 {
				t.Fatalf("converted request = %s", result.Result.Body)
			}
			if parts[0].Type != test.wantType {
				t.Fatalf("content type = %q, want %q: %s", parts[0].Type, test.wantType, items[0].Content)
			}
			if test.wantType == "input_image" && parts[0].ImageURL == "" {
				t.Fatalf("image URL missing: %s", items[0].Content)
			}
			if test.wantType == "input_file" && parts[0].FileURL == "" {
				t.Fatalf("file URL missing: %s", items[0].Content)
			}
		})
	}
}

func TestMessagesGeminiSystemRoleAndMediaSourceValidation(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"claude","max_tokens":64,"system":"root policy",
		"messages":[{"role":"system","content":"message policy"},{"role":"user","content":"hello"}]
	}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gemini"}})
	if err != nil {
		t.Fatal(err)
	}
	var request geminiRequest
	if err := json.Unmarshal(result.Body, &request); err != nil {
		t.Fatal(err)
	}
	if request.SystemInstruction == nil || len(request.SystemInstruction.Parts) != 2 || len(request.Contents) != 1 {
		t.Fatalf("converted request = %s", result.Body)
	}

	for _, test := range []struct {
		name    string
		body    string
		wantErr error
		path    string
	}{
		{name: "interleaved system", body: `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hello"},{"role":"system","content":"late policy"}]}`, wantErr: ErrUnsupported, path: "$.messages[1].role"},
		{name: "system media", body: `{"model":"claude","max_tokens":64,"messages":[{"role":"system","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1n"}}]},{"role":"user","content":"hello"}]}`, wantErr: ErrUnsupported, path: "$.messages[0].content[0]"},
		{name: "mixed base64 source", body: `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1n","url":"https://example.com/image.png"}}]}]}`, wantErr: ErrInvalidPayload, path: ".source"},
		{name: "invalid base64", body: `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%%%"}}]}]}`, wantErr: ErrInvalidPayload, path: ".source.data"},
		{name: "provider file id", body: `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"file","file_id":"file_1"}}]}]}`, wantErr: ErrUnsupported, path: ".source.file_id"},
		{name: "unknown image URL MIME", body: `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://storage.googleapis.com/bucket/image"}}]}]}`, wantErr: ErrUnsupported, path: ".source.url"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{})
			if !errors.Is(err, test.wantErr) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want %v at %s", err, test.wantErr, test.path)
			}
		})
	}

	for _, test := range []struct {
		name     string
		block    string
		wantMIME string
	}{
		{name: "GCS image", block: `{"type":"image","source":{"type":"url","url":"https://storage.googleapis.com/bucket/image.png"}}`, wantMIME: "image/png"},
		{name: "GCS PDF", block: `{"type":"document","source":{"type":"url","url":"https://storage.googleapis.com/bucket/report.pdf"}}`, wantMIME: "application/pdf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[` + test.block + `]}]}`)
			result, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var request geminiRequest
			if json.Unmarshal(result.Body, &request) != nil || len(request.Contents) != 1 || len(request.Contents[0].Parts) != 1 || request.Contents[0].Parts[0].FileData == nil {
				t.Fatalf("converted request = %s", result.Body)
			}
			if request.Contents[0].Parts[0].FileData.MIMEType != test.wantMIME {
				t.Fatalf("fileData = %#v", request.Contents[0].Parts[0].FileData)
			}
		})
	}
}

func TestResponsesURLDocumentsRequirePDFForMessages(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolResponses, To: ProtocolMessages})
	for _, test := range []struct {
		name    string
		url     string
		wantErr error
	}{
		{name: "PDF", url: "https://example.com/report.pdf"},
		{name: "CSV", url: "https://example.com/report.csv", wantErr: ErrUnsupported},
		{name: "unknown", url: "https://example.com/report", wantErr: ErrUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"model":"responses","input":[{"type":"message","role":"user","content":[{"type":"input_file","file_url":"` + test.url + `"}]}]}`)
			result, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(result.Body), `"type":"document"`) || !strings.Contains(string(result.Body), `"type":"url"`) {
				t.Fatalf("converted request = %s", result.Body)
			}
		})
	}
}
