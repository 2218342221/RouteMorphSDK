// Package gemini owns the JSON wire types for Gemini generateContent.
package gemini

import "encoding/json"

type Request struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	Tools             []Tool            `json:"tools,omitempty"`
	ToolConfig        *ToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	SafetySettings    json.RawMessage   `json:"safetySettings,omitempty"`
	CachedContent     string            `json:"cachedContent,omitempty"`
}

type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

type Part struct {
	Text                string            `json:"text,omitempty"`
	InlineData          *Blob             `json:"inlineData,omitempty"`
	FileData            *FileData         `json:"fileData,omitempty"`
	FunctionCall        *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse    *FunctionResponse `json:"functionResponse,omitempty"`
	Thought             bool              `json:"thought,omitempty"`
	ThoughtSignature    string            `json:"thoughtSignature,omitempty"`
	MediaResolution     json.RawMessage   `json:"mediaResolution,omitempty"`
	VideoMetadata       json.RawMessage   `json:"videoMetadata,omitempty"`
	ExecutableCode      json.RawMessage   `json:"executableCode,omitempty"`
	CodeExecutionResult json.RawMessage   `json:"codeExecutionResult,omitempty"`
}

type Blob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}
type FileData struct {
	MIMEType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}
type FunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}
type FunctionResponse struct {
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name"`
	Response     json.RawMessage `json:"response"`
	WillContinue json.RawMessage `json:"willContinue,omitempty"`
	Scheduling   json.RawMessage `json:"scheduling,omitempty"`
	Parts        json.RawMessage `json:"parts,omitempty"`
}

type Tool struct {
	FunctionDeclarations  []FunctionDeclaration `json:"functionDeclarations,omitempty"`
	CodeExecution         json.RawMessage       `json:"codeExecution,omitempty"`
	GoogleSearch          json.RawMessage       `json:"googleSearch,omitempty"`
	GoogleSearchRetrieval json.RawMessage       `json:"googleSearchRetrieval,omitempty"`
	URLContext            json.RawMessage       `json:"urlContext,omitempty"`
}
type FunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}
type ToolConfig struct {
	FunctionCallingConfig struct {
		Mode                 string   `json:"mode,omitempty"`
		AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
	} `json:"functionCallingConfig"`
	RetrievalConfig                  json.RawMessage `json:"retrievalConfig,omitempty"`
	IncludeServerSideToolInvocations *bool           `json:"includeServerSideToolInvocations,omitempty"`
}
type ThinkingConfig struct {
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
}
type GenerationConfig struct {
	MaxOutputTokens            *int            `json:"maxOutputTokens,omitempty"`
	Temperature                *float64        `json:"temperature,omitempty"`
	TopP                       *float64        `json:"topP,omitempty"`
	TopK                       *float64        `json:"topK,omitempty"`
	CandidateCount             *int            `json:"candidateCount,omitempty"`
	StopSequences              []string        `json:"stopSequences,omitempty"`
	ResponseMIMEType           string          `json:"responseMimeType,omitempty"`
	ResponseSchema             json.RawMessage `json:"responseSchema,omitempty"`
	ResponseJSONSchema         json.RawMessage `json:"responseJsonSchema,omitempty"`
	PresencePenalty            *float64        `json:"presencePenalty,omitempty"`
	FrequencyPenalty           *float64        `json:"frequencyPenalty,omitempty"`
	ResponseLogprobs           *bool           `json:"responseLogprobs,omitempty"`
	Logprobs                   *int            `json:"logprobs,omitempty"`
	EnableEnhancedCivicAnswers *bool           `json:"enableEnhancedCivicAnswers,omitempty"`
	MediaResolution            json.RawMessage `json:"mediaResolution,omitempty"`
	Seed                       *int64          `json:"seed,omitempty"`
	ResponseModalities         []string        `json:"responseModalities,omitempty"`
	SpeechConfig               json.RawMessage `json:"speechConfig,omitempty"`
	ImageConfig                json.RawMessage `json:"imageConfig,omitempty"`
	ThinkingConfig             *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

type Candidate struct {
	Content            Content         `json:"content"`
	FinishReason       string          `json:"finishReason"`
	FinishMessage      string          `json:"finishMessage,omitempty"`
	Index              int64           `json:"index,omitempty"`
	AvgLogprobs        *float64        `json:"avgLogprobs,omitempty"`
	LogprobsResult     json.RawMessage `json:"logprobsResult,omitempty"`
	SafetyRatings      json.RawMessage `json:"safetyRatings,omitempty"`
	CitationMetadata   json.RawMessage `json:"citationMetadata,omitempty"`
	GroundingMetadata  json.RawMessage `json:"groundingMetadata,omitempty"`
	URLContextMetadata json.RawMessage `json:"urlContextMetadata,omitempty"`
}
type Response struct {
	Candidates    []Candidate `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount           int64           `json:"promptTokenCount"`
		ToolUsePromptTokenCount    int64           `json:"toolUsePromptTokenCount"`
		CandidatesTokenCount       int64           `json:"candidatesTokenCount"`
		TotalTokenCount            int64           `json:"totalTokenCount"`
		CachedContentTokenCount    int64           `json:"cachedContentTokenCount"`
		ThoughtsTokenCount         int64           `json:"thoughtsTokenCount"`
		PromptTokensDetails        json.RawMessage `json:"promptTokensDetails,omitempty"`
		ToolUsePromptTokensDetails json.RawMessage `json:"toolUsePromptTokensDetails,omitempty"`
		CandidatesTokensDetails    json.RawMessage `json:"candidatesTokensDetails,omitempty"`
	} `json:"usageMetadata"`
	PromptFeedback json.RawMessage `json:"promptFeedback,omitempty"`
	ModelVersion   string          `json:"modelVersion"`
	ResponseID     string          `json:"responseId"`
}
