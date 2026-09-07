package messagesgemini

func declaredMessagesFunctionNames(tools []messagesTool) map[string]struct{} {
	declared := make(map[string]struct{})
	for _, tool := range tools {
		if tool.Name != "" {
			declared[tool.Name] = struct{}{}
		}
	}
	return declared
}

func validateMessagesToolChoiceDeclarations(choice toolChoice, tools []messagesTool) error {
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

func sameFunctionNameSet(names []string, declared map[string]struct{}) bool {
	if len(names) != len(declared) {
		return false
	}
	for _, name := range names {
		if _, ok := declared[name]; !ok {
			return false
		}
	}
	return true
}
