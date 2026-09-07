package chatmessages

import "fmt"

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

func declaredChatFunctions(tools []chatTool) map[string]struct{} {
	declared := make(map[string]struct{})
	for _, tool := range tools {
		if tool.Type == "function" && tool.Function.Name != "" {
			declared[tool.Function.Name] = struct{}{}
		}
	}
	return declared
}

func declaredMessagesFunctions(tools []messagesTool) map[string]struct{} {
	declared := make(map[string]struct{})
	for _, tool := range tools {
		if tool.Name != "" {
			declared[tool.Name] = struct{}{}
		}
	}
	return declared
}

func normalizeChatChoiceForMessages(choice toolChoice, tools []chatTool) (toolChoice, error) {
	declared := declaredChatFunctions(tools)
	switch choice.Mode {
	case toolChoiceRequired:
		if len(tools) == 0 {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice", "required tool choice requires at least one declared function")
		}
	case toolChoiceNamed:
		if _, ok := declared[choice.Name]; !ok {
			return toolChoice{}, invalid(ProtocolChat, "$.tool_choice.function.name", "function %q is not declared", choice.Name)
		}
	case toolChoiceAllowed:
		for index, name := range choice.AllowedNames {
			if _, ok := declared[name]; !ok {
				return toolChoice{}, invalid(ProtocolChat, fmt.Sprintf("$.tool_choice.allowed_tools.tools[%d].function.name", index), "function %q is not declared", name)
			}
		}
		allDeclared := len(choice.AllowedNames) == len(declared)
		switch choice.AllowedMode {
		case toolChoiceAuto:
			if !allDeclared {
				return toolChoice{}, unsupported(ProtocolChat, "$.tool_choice.allowed_tools.tools", "Messages cannot preserve an auto choice restricted to a strict subset of functions")
			}
			return toolChoice{Mode: toolChoiceAuto}, nil
		case toolChoiceRequired:
			if allDeclared {
				return toolChoice{Mode: toolChoiceRequired}, nil
			}
			if len(choice.AllowedNames) == 1 {
				return toolChoice{Mode: toolChoiceNamed, Name: choice.AllowedNames[0]}, nil
			}
			return toolChoice{}, unsupported(ProtocolChat, "$.tool_choice.allowed_tools.tools", "Messages cannot require one of a strict subset of multiple functions")
		}
	}
	return choice, nil
}

func validateMessagesChoice(protocol Protocol, choice toolChoice, tools []messagesTool) error {
	declared := declaredMessagesFunctions(tools)
	switch choice.Mode {
	case toolChoiceRequired:
		if len(tools) == 0 {
			return invalid(protocol, "$.tool_choice", "any tool choice requires at least one declared function")
		}
	case toolChoiceNamed:
		if _, ok := declared[choice.Name]; !ok {
			return invalid(protocol, "$.tool_choice.name", "function %q is not declared", choice.Name)
		}
	}
	return nil
}
