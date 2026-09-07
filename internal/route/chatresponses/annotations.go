package chatresponses

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

type urlCitation struct {
	Type       string `json:"type"`
	StartIndex int    `json:"start_index"`
	EndIndex   int    `json:"end_index"`
	Title      string `json:"title"`
	URL        string `json:"url"`
}

func decodeResponsesURLCitations(raw json.RawMessage, path string, messageOffset, messageTextLength int) ([]urlCitation, error) {
	if !jsonValuePresent(raw) {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, upstreamResponseError(ProtocolResponses, path, "annotations must be an array")
	}
	citations := make([]urlCitation, 0, len(entries))
	for index, entry := range entries {
		entryPath := fmt.Sprintf("%s[%d]", path, index)
		fields, err := responseAnnotationFields(ProtocolResponses, entryPath, entry, "type", "start_index", "end_index", "title", "url")
		if err != nil {
			return nil, err
		}
		var citation urlCitation
		if err := json.Unmarshal(entry, &citation); err != nil {
			return nil, upstreamResponseError(ProtocolResponses, entryPath, "invalid annotation")
		}
		if citation.Type != "url_citation" {
			return nil, unsupported(ProtocolResponses, entryPath+".type", "annotation %q has no Chat equivalent", citation.Type)
		}
		for _, required := range []string{"start_index", "end_index", "title", "url"} {
			if _, ok := fields[required]; !ok {
				return nil, upstreamResponseError(ProtocolResponses, entryPath+"."+required, "is required")
			}
		}
		if citation.URL == "" || citation.StartIndex < 0 || citation.EndIndex < citation.StartIndex {
			return nil, upstreamResponseError(ProtocolResponses, entryPath, "URL and a valid index range are required")
		}
		if citation.EndIndex > messageTextLength {
			return nil, upstreamResponseError(ProtocolResponses, entryPath+".end_index", "exceeds output message text length %d", messageTextLength)
		}
		citation.StartIndex += messageOffset
		citation.EndIndex += messageOffset
		citations = append(citations, citation)
	}
	return citations, nil
}

func encodeChatURLCitations(citations []urlCitation) json.RawMessage {
	if len(citations) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(citations))
	for _, citation := range citations {
		result = append(result, map[string]any{
			"type": "url_citation",
			"url_citation": map[string]any{
				"start_index": citation.StartIndex,
				"end_index":   citation.EndIndex,
				"title":       citation.Title,
				"url":         citation.URL,
			},
		})
	}
	return mustJSON(result)
}

func decodeChatURLCitations(raw json.RawMessage, path string, textLength int) ([]urlCitation, error) {
	if !jsonValuePresent(raw) {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, upstreamResponseError(ProtocolChat, path, "annotations must be an array")
	}
	citations := make([]urlCitation, 0, len(entries))
	for index, entry := range entries {
		entryPath := fmt.Sprintf("%s[%d]", path, index)
		fields, err := responseAnnotationFields(ProtocolChat, entryPath, entry, "type", "url_citation")
		if err != nil {
			return nil, err
		}
		var annotation struct {
			Type        string      `json:"type"`
			URLCitation urlCitation `json:"url_citation"`
		}
		if err := json.Unmarshal(entry, &annotation); err != nil {
			return nil, upstreamResponseError(ProtocolChat, entryPath, "invalid annotation")
		}
		if annotation.Type != "url_citation" {
			return nil, unsupported(ProtocolChat, entryPath+".type", "annotation %q has no Responses equivalent", annotation.Type)
		}
		if !jsonValuePresent(fields["url_citation"]) {
			return nil, upstreamResponseError(ProtocolChat, entryPath+".url_citation", "is required")
		}
		if _, err := responseAnnotationFields(ProtocolChat, entryPath+".url_citation", fields["url_citation"], "start_index", "end_index", "title", "url"); err != nil {
			return nil, err
		}
		var citationFields map[string]json.RawMessage
		if err := json.Unmarshal(fields["url_citation"], &citationFields); err != nil {
			return nil, upstreamResponseError(ProtocolChat, entryPath+".url_citation", "must be an object")
		}
		for _, required := range []string{"start_index", "end_index", "title", "url"} {
			if _, ok := citationFields[required]; !ok {
				return nil, upstreamResponseError(ProtocolChat, entryPath+".url_citation."+required, "is required")
			}
		}
		citation := annotation.URLCitation
		citation.Type = "url_citation"
		if citation.URL == "" || citation.StartIndex < 0 || citation.EndIndex < citation.StartIndex {
			return nil, upstreamResponseError(ProtocolChat, entryPath+".url_citation", "URL and a valid index range are required")
		}
		if citation.EndIndex > textLength {
			return nil, upstreamResponseError(ProtocolChat, entryPath+".url_citation.end_index", "exceeds message text length %d", textLength)
		}
		citations = append(citations, citation)
	}
	return citations, nil
}

func responseAnnotationFields(protocol Protocol, path string, raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, upstreamResponseError(protocol, path, "must be an object")
	}
	known := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		known[name] = struct{}{}
	}
	for name, value := range fields {
		if _, ok := known[name]; !ok && nonNullJSON(value) {
			return nil, unsupported(protocol, path+"."+name, "annotation field has no portable equivalent")
		}
	}
	return fields, nil
}

func encodeResponsesURLCitations(citations []urlCitation) json.RawMessage {
	if len(citations) == 0 {
		return json.RawMessage(`[]`)
	}
	return mustJSON(citations)
}

func textRuneCount(parts []portablePart) int {
	count := 0
	for _, part := range parts {
		if part.Kind == partText {
			count += utf8.RuneCountInString(part.Text)
		}
	}
	return count
}
