// Package routekit contains protocol-neutral mechanics shared by direct route
// packages. Semantic compatibility decisions remain owned by each protocol
// pair so sharing these helpers cannot introduce an implicit conversion path.
package routekit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"path/filepath"
	"strings"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
	jsonx "github.com/2218342221/RouteMorphSDK/internal/jsonx"
	schemax "github.com/2218342221/RouteMorphSDK/internal/schema"
)

func DecodeJSON(protocol core.Protocol, data []byte, destination any) error {
	if err := jsonx.DecodeOne(data, destination); err != nil {
		return core.Invalid(protocol, "$", "%v", err)
	}
	return nil
}

func Marshal(protocol core.Protocol, value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, core.Invalid(protocol, "$", "cannot encode JSON: %v", err)
	}
	return data, nil
}

// MustJSON is reserved for internal values whose complete type graph is known
// to be JSON encodable. Panicking makes a broken internal invariant visible
// instead of silently emitting a nil or malformed protocol payload.
func MustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("routekit: internal value is not JSON encodable: %v", err))
	}
	return data
}

func MustJSONString(value string) string { return string(MustJSON(value)) }

func NormalizeArguments(protocol core.Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := jsonx.NormalizeObject(raw)
	if err != nil {
		return nil, core.Invalid(protocol, path, "tool arguments must be a JSON object: %v", err)
	}
	return normalized, nil
}

func NormalizeOpenAIToolArguments(protocol core.Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > 0 && raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, core.Invalid(protocol, path, "invalid argument string: %v", err)
		}
		if value == "" {
			return json.RawMessage(`{}`), nil
		}
	}
	return NormalizeArguments(protocol, path, raw)
}

func NormalizeFunctionParameters(protocol core.Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := schemax.NormalizeFunctionParameters(raw)
	if err != nil {
		return nil, core.Invalid(protocol, path, "function parameters must be a JSON object")
	}
	return normalized, nil
}

func RawString(raw json.RawMessage) string { return jsonx.RawString(raw) }

func RawObject(protocol core.Protocol, data []byte) (map[string]json.RawMessage, error) {
	object, err := jsonx.Object(data)
	if err != nil {
		return nil, core.Invalid(protocol, "$", "%v", err)
	}
	return object, nil
}

func ValuePresent(raw json.RawMessage) bool { return jsonx.Present(raw) }

func NonNullValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

func AppendDiagnostic(diagnostics []core.Diagnostic, severity, code, path, message string) []core.Diagnostic {
	return append(diagnostics, core.Diagnostic{Severity: severity, Code: code, Path: path, Message: message})
}

func RejectUnknownTopLevel(protocol core.Protocol, data []byte, allowed ...string) error {
	object, err := RawObject(protocol, data)
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range object {
		if _, ok := known[field]; !ok {
			return core.Unsupported(protocol, "$."+field, "field is not supported by this cross-protocol route")
		}
	}
	return nil
}

func ResolveExchangeStream(source bool, exchange core.ExchangeMetadata) bool {
	if exchange.StreamSet || exchange.Stream {
		return exchange.Stream
	}
	return source
}

func TextParts(text string) []core.Part {
	if text == "" {
		return nil
	}
	return []core.Part{{Kind: core.PartText, Text: text}}
}

func DataURL(media *core.Media) string {
	if media == nil {
		return ""
	}
	if media.URL != "" {
		return media.URL
	}
	if media.Data == "" {
		return ""
	}
	mimeType := media.MIMEType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + media.Data
}

func ChatAudioMIMEType(format string) (string, bool) {
	switch strings.ToLower(format) {
	case "wav":
		return "audio/wav", true
	case "mp3":
		return "audio/mpeg", true
	default:
		return "", false
	}
}

func ChatAudioFormat(mimeType string) (string, bool) {
	switch strings.ToLower(mimeType) {
	case "audio/wav", "audio/x-wav":
		return "wav", true
	case "audio/mp3", "audio/mpeg":
		return "mp3", true
	default:
		return "", false
	}
}

func ValidBase64(value string) bool {
	if value == "" {
		return false
	}
	if _, err := base64.StdEncoding.DecodeString(value); err == nil {
		return true
	}
	_, err := base64.RawStdEncoding.DecodeString(value)
	return err == nil
}

func ValidOpenAIImageMIMEType(value string) bool {
	switch strings.ToLower(value) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// ValidMIMEType reports whether value is a bare IANA-style media type. Gemini
// inlineData/fileData carry the media type separately and cannot preserve MIME
// parameters such as charset without changing the wire value.
func ValidMIMEType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(value))
	return err == nil && strings.Contains(mediaType, "/") && len(parameters) == 0
}

// MIMETypeFromFilename returns a normalized media type inferred from a file
// extension. An empty result means that the type cannot be inferred safely.
func MIMETypeFromFilename(filename string) string {
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if mediaType == "" {
		return ""
	}
	normalized, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return ""
	}
	return strings.ToLower(normalized)
}

// MIMETypeFromURL infers a media type from the URL path without treating query
// parameters or fragments as part of the filename.
func MIMETypeFromURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return MIMETypeFromFilename(parsed.Path)
}

// ValidHTTPURL reports whether value is an absolute HTTP(S) URL.
func ValidHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != ""
}

// OpenAIFileData preserves an explicit media type by emitting the documented
// data-URL form. Raw base64 remains raw when no media type is known.
func OpenAIFileData(media *core.Media) string {
	if media == nil || media.Data == "" {
		return ""
	}
	if media.MIMEType == "" {
		return media.Data
	}
	return DataURL(media)
}

// ParseFileData validates inline file data and carries a MIME type when it is
// encoded in a data URL or can be inferred from the filename.
func ParseFileData(value, filename string) (*core.Media, error) {
	if strings.HasPrefix(value, "data:") {
		media, err := ParseDataURL(value)
		if err != nil {
			return nil, err
		}
		media.Filename = filename
		return media, nil
	}
	if !ValidBase64(value) {
		return nil, fmt.Errorf("file data is not valid base64")
	}
	return &core.Media{Data: value, MIMEType: MIMETypeFromFilename(filename), Filename: filename}, nil
}

// GeminiFileURICompatible reports whether a URI is in one of the forms used by
// Gemini FileData: a Gemini Files API URI or a Google Cloud Storage URI.
func GeminiFileURICompatible(value string) bool {
	if strings.HasPrefix(value, "gs://") {
		return len(strings.TrimPrefix(value, "gs://")) > 0
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "storage.googleapis.com" || strings.HasSuffix(host, ".storage.googleapis.com") || host == "generativelanguage.googleapis.com"
}

// PortableGeminiFileURI reports whether a Gemini FileData URI is also a plain
// HTTPS URL that another provider can fetch without Gemini Files credentials.
func PortableGeminiFileURI(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "storage.googleapis.com" || strings.HasSuffix(host, ".storage.googleapis.com")
}

func ParseDataURL(value string) (*core.Media, error) {
	if !strings.HasPrefix(value, "data:") {
		if !ValidHTTPURL(value) {
			return nil, fmt.Errorf("URL must be an absolute HTTP(S) URL or a base64 data URL")
		}
		return &core.Media{URL: value}, nil
	}
	header, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok {
		return nil, fmt.Errorf("data URL is missing a comma")
	}
	mimeType, parameters, _ := strings.Cut(header, ";")
	if !ValidMIMEType(mimeType) || !strings.EqualFold(parameters, "base64") {
		return nil, fmt.Errorf("data URL must declare a media type and base64 encoding")
	}
	if !ValidBase64(data) {
		return nil, fmt.Errorf("data URL payload is not valid base64")
	}
	return &core.Media{MIMEType: mimeType, Data: data}, nil
}
