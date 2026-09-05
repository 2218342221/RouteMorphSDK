package responsesmessages

import "fmt"

func declaredResponsesFunctionNames(tools []responsesTool) map[string]struct{} {
	declared := make(map[string]struct{})
	for _, tool := range tools {
		if tool.Type == "function" && tool.Name != "" {
			declared[tool.Name] = struct{}{}
		}
	}
	return declared
}

func declaredMessagesFunctionNames(tools []messagesTool) map[string]struct{} {
	declared := make(map[string]struct{})
	for _, tool := range tools {
		if tool.Name != "" {
			declared[tool.Name] = struct{}{}
		}
	}
	return declared
}

func normalizeResponsesChoiceForMessages(choice toolChoice, tools []responsesTool) (toolChoice, error) {
	declared := declaredResponsesFunctionNames(tools)
	switch choice.Mode {
	case toolChoiceRequired:
		if len(tools) == 0 {
			return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice", "required tool choice requires at least one declared function")
		}
	case toolChoiceNamed:
		if _, ok := declared[choice.Name]; !ok {
			return toolChoice{}, invalid(ProtocolResponses, "$.tool_choice.name", "function %q is not declared", choice.Name)
		}
	case toolChoiceAllowed:
		for index, name := range choice.AllowedNames {
			if _, ok := declared[name]; !ok {
				return toolChoice{}, invalid(ProtocolResponses, fmt.Sprintf("$.tool_choice.tools[%d].name", index), "function %q is not declared", name)
			}
		}
		allDeclared := len(choice.AllowedNames) == len(declared)
		switch choice.AllowedMode {
		case toolChoiceAuto:
			if !allDeclared {
				return toolChoice{}, unsupported(ProtocolResponses, "$.tool_choice.tools", "Messages cannot preserve an auto choice restricted to a strict subset of functions")
			}
			return toolChoice{Mode: toolChoiceAuto}, nil
		case toolChoiceRequired:
			if allDeclared {
				return toolChoice{Mode: toolChoiceRequired}, nil
			}
			if len(choice.AllowedNames) == 1 {
				return toolChoice{Mode: toolChoiceNamed, Name: choice.AllowedNames[0]}, nil
			}
			return toolChoice{}, unsupported(ProtocolResponses, "$.tool_choice.tools", "Messages cannot require one of a strict subset of multiple functions")
		}
	}
	return choice, nil
}

func validateMessagesChoiceDeclarations(choice toolChoice, tools []messagesTool) error {
	declared := declaredMessagesFunctionNames(tools)
	switch choice.Mode {
	case toolChoiceRequired:
		if len(tools) == 0 {
			return invalid(ProtocolMessages, "$.tool_choice", "any tool choice requires at least one declared function")
		}
	case toolChoiceNamed:
		if _, ok := declared[choice.Name]; !ok {
			return invalid(ProtocolMessages, "$.tool_choice.name", "function %q is not declared", choice.Name)
		}
	}
	return nil
}
