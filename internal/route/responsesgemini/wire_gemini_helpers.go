package responsesgemini

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
