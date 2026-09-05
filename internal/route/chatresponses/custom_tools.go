package chatresponses

import "encoding/json"

func chatCustomFormatToResponses(raw json.RawMessage, path string) (json.RawMessage, error) {
	if !nonNullJSON(raw) {
		return nil, nil
	}
	fields, err := rejectUnknownObjectFields(ProtocolChat, path, raw, "type", "grammar")
	if err != nil {
		return nil, err
	}
	formatType := rawString(fields["type"])
	switch formatType {
	case "text":
		if nonNullJSON(fields["grammar"]) {
			return nil, invalid(ProtocolChat, path+".grammar", "is only valid when type is grammar")
		}
		return mustJSON(map[string]any{"type": "text"}), nil
	case "grammar":
		if !nonNullJSON(fields["grammar"]) {
			return nil, invalid(ProtocolChat, path+".grammar", "is required when type is grammar")
		}
		grammar, err := decodeCustomGrammar(ProtocolChat, path+".grammar", fields["grammar"])
		if err != nil {
			return nil, err
		}
		return mustJSON(map[string]any{"type": "grammar", "definition": grammar.Definition, "syntax": grammar.Syntax}), nil
	default:
		return nil, unsupported(ProtocolChat, path+".type", "custom tool format %q is not supported", formatType)
	}
}

func responsesCustomFormatToChat(raw json.RawMessage, path string) (json.RawMessage, error) {
	if !nonNullJSON(raw) {
		return nil, nil
	}
	fields, err := rejectUnknownObjectFields(ProtocolResponses, path, raw, "type", "definition", "syntax")
	if err != nil {
		return nil, err
	}
	formatType := rawString(fields["type"])
	switch formatType {
	case "text":
		if nonNullJSON(fields["definition"]) || nonNullJSON(fields["syntax"]) {
			return nil, invalid(ProtocolResponses, path, "text format cannot contain grammar fields")
		}
		return mustJSON(map[string]any{"type": "text"}), nil
	case "grammar":
		grammar, err := decodeCustomGrammarFields(ProtocolResponses, path, fields["definition"], fields["syntax"])
		if err != nil {
			return nil, err
		}
		return mustJSON(map[string]any{"type": "grammar", "grammar": grammar}), nil
	default:
		return nil, unsupported(ProtocolResponses, path+".type", "custom tool format %q is not supported", formatType)
	}
}

type customGrammar struct {
	Definition string `json:"definition"`
	Syntax     string `json:"syntax"`
}

func decodeCustomGrammar(protocol Protocol, path string, raw json.RawMessage) (customGrammar, error) {
	fields, err := rejectUnknownObjectFields(protocol, path, raw, "definition", "syntax")
	if err != nil {
		return customGrammar{}, err
	}
	return decodeCustomGrammarFields(protocol, path, fields["definition"], fields["syntax"])
}

func decodeCustomGrammarFields(protocol Protocol, path string, definitionRaw, syntaxRaw json.RawMessage) (customGrammar, error) {
	var grammar customGrammar
	if err := json.Unmarshal(definitionRaw, &grammar.Definition); err != nil || grammar.Definition == "" {
		return customGrammar{}, invalid(protocol, path+".definition", "non-empty string is required")
	}
	if err := json.Unmarshal(syntaxRaw, &grammar.Syntax); err != nil || (grammar.Syntax != "lark" && grammar.Syntax != "regex") {
		return customGrammar{}, invalid(protocol, path+".syntax", "must be %q or %q", "lark", "regex")
	}
	return grammar, nil
}

func validateResponsesToolPortableFields(tool responsesTool, path string) error {
	if tool.Async != nil && *tool.Async {
		return unsupported(ProtocolResponses, path+".async", "asynchronous tool execution has no Chat equivalent")
	}
	if len(tool.AllowedCallers) > 0 {
		return unsupported(ProtocolResponses, path+".allowed_callers", "tool caller constraints have no Chat equivalent")
	}
	if nonNullJSON(tool.OutputSchema) {
		return unsupported(ProtocolResponses, path+".output_schema", "tool output schemas have no Chat equivalent")
	}
	if tool.DeferLoading {
		return unsupported(ProtocolResponses, path+".defer_loading", "deferred tool loading requires native Responses tool search")
	}
	if nonNullJSON(tool.PromptCacheBreakpoint) {
		return unsupported(ProtocolResponses, path+".prompt_cache_breakpoint", "tool prompt cache breakpoints require a native Responses provider")
	}
	return nil
}

func validateResponsesItemProvenance(item responsesItem, path string) error {
	if item.Async != nil && *item.Async {
		return unsupported(ProtocolResponses, path+".async", "asynchronous tool execution has no Chat equivalent")
	}
	if nonNullJSON(item.Caller) {
		fields, err := rejectUnknownObjectFields(ProtocolResponses, path+".caller", item.Caller, "type", "caller_id")
		if err != nil {
			return err
		}
		callerType := rawString(fields["type"])
		if callerType != "direct" {
			return unsupported(ProtocolResponses, path+".caller", "caller type %q has no Chat equivalent", callerType)
		}
		if nonNullJSON(fields["caller_id"]) {
			return invalid(ProtocolResponses, path+".caller.caller_id", "is only valid for program callers")
		}
	}
	if item.Namespace != "" {
		return unsupported(ProtocolResponses, path+".namespace", "tool namespaces have no Chat equivalent")
	}
	if item.CreatedBy != "" {
		return unsupported(ProtocolResponses, path+".created_by", "tool creator metadata has no Chat equivalent")
	}
	if item.Execution != "" {
		return unsupported(ProtocolResponses, path+".execution", "tool execution placement has no Chat equivalent")
	}
	if nonNullJSON(item.Action) {
		return unsupported(ProtocolResponses, path+".action", "hosted-tool actions have no Chat tool-call equivalent")
	}
	if nonNullJSON(item.Tools) {
		return unsupported(ProtocolResponses, path+".tools", "discovered tools have no Chat tool-call equivalent")
	}
	return nil
}

func customInput(raw json.RawMessage, protocol Protocol, path string) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", invalid(protocol, path, "custom tool input is required")
	}
	var input string
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", invalid(protocol, path, "custom tool input must be a string")
	}
	return input, nil
}
