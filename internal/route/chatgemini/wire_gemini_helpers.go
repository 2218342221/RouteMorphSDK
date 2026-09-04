package chatgemini

func appendGeminiContent(target *geminiRequest, role string, parts ...geminiPart) {
	if len(parts) == 0 {
		return
	}
	if len(target.Contents) > 0 && target.Contents[len(target.Contents)-1].Role == role {
		target.Contents[len(target.Contents)-1].Parts = append(target.Contents[len(target.Contents)-1].Parts, parts...)
		return
	}
	target.Contents = append(target.Contents, geminiContent{Role: role, Parts: parts})
}

func checkGeminiSignature(signature, path string, policy lossPolicy, diagnostics *[]Diagnostic) error {
	if signature == "" || signature == geminiThoughtSignatureBypass {
		return nil
	}
	if policy == rejectSemanticLoss {
		return unsupported(ProtocolGenerateContent, path, "Gemini thought signature cannot be represented by Chat")
	}
	if diagnostics != nil {
		*diagnostics = appendDiagnostic(*diagnostics, "warning", "gemini_thought_signature_not_representable", path, "Gemini thought signature was omitted")
	}
	return nil
}
