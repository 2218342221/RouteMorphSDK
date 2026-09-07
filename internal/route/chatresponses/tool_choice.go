package chatresponses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type toolChoiceReference struct {
	Kind string
	Name string
	Path string
}

type toolChoice struct {
	Mode          toolChoiceMode
	Name          string
	Kind          string
	ReferencePath string
	AllowedMode   toolChoiceMode
	AllowedTools  []toolChoiceReference
}

func validateChatToolDeclarationNames(tools []chatTool) error {
	seen := make(map[string]struct{}, len(tools))
	for index, tool := range tools {
		path := fmt.Sprintf("$.tools[%d]", index)
		var kind, name, namePath string
		switch tool.Type {
		case "":
			return invalid(ProtocolChat, path+".type", "tool type is required")
		case "function":
			kind, name, namePath = "function", tool.Function.Name, path+".function.name"
		case "custom":
			kind, name, namePath = "custom", "", path+".custom.name"
			if tool.Custom != nil {
				name = tool.Custom.Name
			}
		default:
			continue
		}
		if name == "" {
			return invalid(ProtocolChat, namePath, "name is required")
		}
		key := kind + "\x00" + name
		if _, duplicate := seen[key]; duplicate {
			return invalid(ProtocolChat, namePath, "duplicate %s tool name %q", kind, name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func decodeChatToolChoice(raw json.RawMessage) (toolChoice, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return toolChoice{}, nil
	}
	if trimmed[0] == '"' {
		value, err := decodeToolChoiceString(ProtocolChat, "$.tool_choice", trimmed)
		if err != nil {
			return toolChoice{}, err
		}
		return toolChoice{Mode: value}, nil
	}

	fields, err := toolChoiceObject(ProtocolChat, "$.tool_choice", trimmed)
	if err != nil {
		return toolChoice{}, err
	}
	kind, err := requiredToolChoiceString(ProtocolChat, "$.tool_choice.type", fields["type"])
	if err != nil {
		return toolChoice{}, err
	}
	switch kind {
	case "function", "custom":
		fields, err = rejectUnknownObjectFields(ProtocolChat, "$.tool_choice", trimmed, "type", "function", "custom")
		if err != nil {
			return toolChoice{}, err
		}
		other := "custom"
		if kind == "custom" {
			other = "function"
		}
		if nonNullJSON(fields[other]) {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice."+other, "is not valid for a %s choice", kind)
		}
		namePath := "$.tool_choice." + kind + ".name"
		name, err := decodeChatToolChoiceName(fields[kind], "$.tool_choice."+kind)
		if err != nil {
			return toolChoice{}, err
		}
		return toolChoice{Mode: toolChoiceNamed, Name: name, Kind: kind, ReferencePath: namePath}, nil
	case "allowed_tools":
		fields, err = rejectUnknownObjectFields(ProtocolChat, "$.tool_choice", trimmed, "type", "allowed_tools")
		if err != nil {
			return toolChoice{}, err
		}
		mode, references, err := decodeChatAllowedTools(fields["allowed_tools"])
		if err != nil {
			return toolChoice{}, err
		}
		return toolChoice{Mode: toolChoiceAllowed, AllowedMode: mode, AllowedTools: references}, nil
	default:
		return toolChoice{}, unsupported(ProtocolChat, "$.tool_choice.type", "tool choice type %q has no portable Responses equivalent", kind)
	}
}

func decodeResponsesToolChoice(raw json.RawMessage) (toolChoice, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return toolChoice{}, nil
	}
	if trimmed[0] == '"' {
		value, err := decodeToolChoiceString(ProtocolResponses, "$.tool_choice", trimmed)
		if err != nil {
			return toolChoice{}, err
		}
		return toolChoice{Mode: value}, nil
	}

	fields, err := toolChoiceObject(ProtocolResponses, "$.tool_choice", trimmed)
	if err != nil {
		return toolChoice{}, err
	}
	kind, err := requiredToolChoiceString(ProtocolResponses, "$.tool_choice.type", fields["type"])
	if err != nil {
		return toolChoice{}, err
	}
	switch kind {
	case "function", "custom":
		fields, err = rejectUnknownObjectFields(ProtocolResponses, "$.tool_choice", trimmed, "type", "name")
		if err != nil {
			return toolChoice{}, err
		}
		name, err := requiredToolChoiceString(ProtocolResponses, "$.tool_choice.name", fields["name"])
		if err != nil {
			return toolChoice{}, err
		}
		return toolChoice{Mode: toolChoiceNamed, Name: name, Kind: kind, ReferencePath: "$.tool_choice.name"}, nil
	case "allowed_tools":
		fields, err = rejectUnknownObjectFields(ProtocolResponses, "$.tool_choice", trimmed, "type", "mode", "tools")
		if err != nil {
			return toolChoice{}, err
		}
		mode, err := decodeAllowedToolsMode(ProtocolResponses, "$.tool_choice.mode", fields["mode"])
		if err != nil {
			return toolChoice{}, err
		}
		references, err := decodeResponsesAllowedToolReferences(fields["tools"], "$.tool_choice.tools")
		if err != nil {
			return toolChoice{}, err
		}
		return toolChoice{Mode: toolChoiceAllowed, AllowedMode: mode, AllowedTools: references}, nil
	default:
		return toolChoice{}, unsupported(ProtocolResponses, "$.tool_choice.type", "tool choice type %q has no Chat equivalent", kind)
	}
}

func decodeToolChoiceString(protocol Protocol, path string, raw json.RawMessage) (toolChoiceMode, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", invalid(protocol, path, "must be a string or object")
	}
	switch toolChoiceMode(value) {
	case toolChoiceAuto, toolChoiceNone, toolChoiceRequired:
		return toolChoiceMode(value), nil
	default:
		return "", invalid(protocol, path, "unknown tool choice %q", value)
	}
}

func toolChoiceObject(protocol Protocol, path string, raw json.RawMessage) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, invalid(protocol, path, "must be a string or object")
	}
	return fields, nil
}

func requiredToolChoiceString(protocol Protocol, path string, raw json.RawMessage) (string, error) {
	var value string
	if !nonNullJSON(raw) || json.Unmarshal(raw, &value) != nil || value == "" {
		return "", invalid(protocol, path, "non-empty string is required")
	}
	return value, nil
}

func decodeChatToolChoiceName(raw json.RawMessage, path string) (string, error) {
	if !nonNullJSON(raw) {
		return "", invalid(ProtocolChat, path, "is required")
	}
	fields, err := rejectUnknownObjectFields(ProtocolChat, path, raw, "name")
	if err != nil {
		return "", err
	}
	return requiredToolChoiceString(ProtocolChat, path+".name", fields["name"])
}

func decodeChatAllowedTools(raw json.RawMessage) (toolChoiceMode, []toolChoiceReference, error) {
	const path = "$.tool_choice.allowed_tools"
	if !nonNullJSON(raw) {
		return "", nil, invalid(ProtocolChat, path, "is required")
	}
	fields, err := rejectUnknownObjectFields(ProtocolChat, path, raw, "mode", "tools")
	if err != nil {
		return "", nil, err
	}
	mode, err := decodeAllowedToolsMode(ProtocolChat, path+".mode", fields["mode"])
	if err != nil {
		return "", nil, err
	}
	references, err := decodeChatAllowedToolReferences(fields["tools"], path+".tools")
	if err != nil {
		return "", nil, err
	}
	return mode, references, nil
}

func decodeAllowedToolsMode(protocol Protocol, path string, raw json.RawMessage) (toolChoiceMode, error) {
	value, err := requiredToolChoiceString(protocol, path, raw)
	if err != nil {
		return "", err
	}
	mode := toolChoiceMode(value)
	if mode != toolChoiceAuto && mode != toolChoiceRequired {
		return "", invalid(protocol, path, "must be %q or %q", toolChoiceAuto, toolChoiceRequired)
	}
	return mode, nil
}

func decodeChatAllowedToolReferences(raw json.RawMessage, path string) ([]toolChoiceReference, error) {
	items, err := decodeAllowedToolReferenceArray(ProtocolChat, path, raw)
	if err != nil {
		return nil, err
	}
	references := make([]toolChoiceReference, 0, len(items))
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		fields, err := toolChoiceObject(ProtocolChat, itemPath, item)
		if err != nil {
			return nil, err
		}
		kind, err := requiredToolChoiceString(ProtocolChat, itemPath+".type", fields["type"])
		if err != nil {
			return nil, err
		}
		if kind != "function" && kind != "custom" {
			return nil, unsupported(ProtocolChat, itemPath+".type", "allowed tool type %q has no portable Responses equivalent", kind)
		}
		fields, err = rejectUnknownObjectFields(ProtocolChat, itemPath, item, "type", "function", "custom")
		if err != nil {
			return nil, err
		}
		other := "custom"
		if kind == "custom" {
			other = "function"
		}
		if nonNullJSON(fields[other]) {
			return nil, invalid(ProtocolChat, itemPath+"."+other, "is not valid for a %s allowed tool", kind)
		}
		namePath := itemPath + "." + kind + ".name"
		name, err := decodeChatToolChoiceName(fields[kind], itemPath+"."+kind)
		if err != nil {
			return nil, err
		}
		references = append(references, toolChoiceReference{Kind: kind, Name: name, Path: namePath})
	}
	if err := rejectDuplicateAllowedToolReferences(ProtocolChat, references); err != nil {
		return nil, err
	}
	return references, nil
}

func decodeResponsesAllowedToolReferences(raw json.RawMessage, path string) ([]toolChoiceReference, error) {
	items, err := decodeAllowedToolReferenceArray(ProtocolResponses, path, raw)
	if err != nil {
		return nil, err
	}
	references := make([]toolChoiceReference, 0, len(items))
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		fields, err := toolChoiceObject(ProtocolResponses, itemPath, item)
		if err != nil {
			return nil, err
		}
		kind, err := requiredToolChoiceString(ProtocolResponses, itemPath+".type", fields["type"])
		if err != nil {
			return nil, err
		}
		if kind != "function" && kind != "custom" {
			return nil, unsupported(ProtocolResponses, itemPath+".type", "allowed tool type %q has no Chat equivalent", kind)
		}
		fields, err = rejectUnknownObjectFields(ProtocolResponses, itemPath, item, "type", "name")
		if err != nil {
			return nil, err
		}
		namePath := itemPath + ".name"
		name, err := requiredToolChoiceString(ProtocolResponses, namePath, fields["name"])
		if err != nil {
			return nil, err
		}
		references = append(references, toolChoiceReference{Kind: kind, Name: name, Path: namePath})
	}
	if err := rejectDuplicateAllowedToolReferences(ProtocolResponses, references); err != nil {
		return nil, err
	}
	return references, nil
}

func decodeAllowedToolReferenceArray(protocol Protocol, path string, raw json.RawMessage) ([]json.RawMessage, error) {
	if !nonNullJSON(raw) {
		return nil, invalid(protocol, path, "non-empty array is required")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil, invalid(protocol, path, "non-empty array is required")
	}
	return items, nil
}

func rejectDuplicateAllowedToolReferences(protocol Protocol, references []toolChoiceReference) error {
	seen := make(map[string]struct{}, len(references))
	for _, reference := range references {
		key := reference.Kind + "\x00" + reference.Name
		if _, duplicate := seen[key]; duplicate {
			return invalid(protocol, reference.Path, "duplicate %s tool reference %q", reference.Kind, reference.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateToolChoiceReferences(protocol Protocol, choice toolChoice, tools []responsesTool) error {
	if choice.Mode == toolChoiceRequired && len(tools) == 0 {
		return invalid(protocol, "$.tool_choice", "required tool choice requires at least one declared tool")
	}
	if choice.Mode != toolChoiceNamed && choice.Mode != toolChoiceAllowed {
		return nil
	}
	declared := make(map[string]struct{})
	declaredKinds := make(map[string]map[string]struct{})
	for _, tool := range tools {
		if tool.Type != "function" && tool.Type != "custom" {
			continue
		}
		declared[tool.Type+"\x00"+tool.Name] = struct{}{}
		if declaredKinds[tool.Name] == nil {
			declaredKinds[tool.Name] = make(map[string]struct{})
		}
		declaredKinds[tool.Name][tool.Type] = struct{}{}
	}

	references := choice.AllowedTools
	if choice.Mode == toolChoiceNamed {
		references = []toolChoiceReference{{Kind: choice.Kind, Name: choice.Name, Path: choice.ReferencePath}}
	}
	for _, reference := range references {
		if _, ok := declared[reference.Kind+"\x00"+reference.Name]; ok {
			continue
		}
		if kinds := declaredKinds[reference.Name]; len(kinds) > 0 {
			values := make([]string, 0, len(kinds))
			for kind := range kinds {
				values = append(values, kind)
			}
			sort.Strings(values)
			return invalid(protocol, reference.Path, "%s tool %q is not declared; the name is declared as %s", reference.Kind, reference.Name, strings.Join(values, ", "))
		}
		return invalid(protocol, reference.Path, "%s tool %q is not declared", reference.Kind, reference.Name)
	}
	return nil
}

func encodeChatToolChoice(choice toolChoice) json.RawMessage {
	switch choice.Mode {
	case toolChoiceNamed:
		data, _ := json.Marshal(map[string]any{"type": choice.Kind, choice.Kind: map[string]any{"name": choice.Name}})
		return data
	case toolChoiceAllowed:
		tools := make([]map[string]any, 0, len(choice.AllowedTools))
		for _, reference := range choice.AllowedTools {
			tools = append(tools, map[string]any{"type": reference.Kind, reference.Kind: map[string]any{"name": reference.Name}})
		}
		data, _ := json.Marshal(map[string]any{
			"type":          "allowed_tools",
			"allowed_tools": map[string]any{"mode": string(choice.AllowedMode), "tools": tools},
		})
		return data
	default:
		data, _ := json.Marshal(string(choice.Mode))
		return data
	}
}

func encodeResponsesToolChoice(choice toolChoice) json.RawMessage {
	switch choice.Mode {
	case toolChoiceNamed:
		data, _ := json.Marshal(map[string]any{"type": choice.Kind, "name": choice.Name})
		return data
	case toolChoiceAllowed:
		tools := make([]map[string]any, 0, len(choice.AllowedTools))
		for _, reference := range choice.AllowedTools {
			tools = append(tools, map[string]any{"type": reference.Kind, "name": reference.Name})
		}
		data, _ := json.Marshal(map[string]any{"type": "allowed_tools", "mode": string(choice.AllowedMode), "tools": tools})
		return data
	default:
		data, _ := json.Marshal(string(choice.Mode))
		return data
	}
}
