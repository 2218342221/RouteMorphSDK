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
	Model             string            `json:"model,omitempty"`
	ServiceTier       string            `json:"serviceTier,omitempty"`
	Store             *bool             `json:"store,omitempty"`
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
	ToolCall            json.RawMessage   `json:"toolCall,omitempty"`
	ToolResponse        json.RawMessage   `json:"toolResponse,omitempty"`
	PartMetadata        json.RawMessage   `json:"partMetadata,omitempty"`
	AudioTranscription  json.RawMessage   `json:"audioTranscription,omitempty"`
	MediaProcessing     string            `json:"mediaProcessing,omitempty"`
}

type Blob struct {
	MIMEType    string `json:"mimeType"`
	Data        string `json:"data"`
	DisplayName string `json:"displayName,omitempty"`
}
type FileData struct {
	MIMEType    string `json:"mimeType,omitempty"`
	FileURI     string `json:"fileUri"`
	DisplayName string `json:"displayName,omitempty"`
}
type FunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}
type FunctionResponse struct {
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name"`
	Response     json.RawMessage        `json:"response"`
	WillContinue json.RawMessage        `json:"willContinue,omitempty"`
	Scheduling   json.RawMessage        `json:"scheduling,omitempty"`
	Parts        []FunctionResponsePart `json:"parts,omitempty"`
}

// FunctionResponsePart is the media-only union nested under
// FunctionResponse.parts. The Gemini Developer API supports inlineData here;
// fileData is retained in the wire type so cross-protocol routes can reject the
// SDK-exposed, Vertex-only shape without silently dropping it.
type FunctionResponsePart struct {
	InlineData *Blob     `json:"inlineData,omitempty"`
	FileData   *FileData `json:"fileData,omitempty"`
}

type Tool struct {
	FunctionDeclarations  []FunctionDeclaration `json:"functionDeclarations,omitempty"`
	CodeExecution         json.RawMessage       `json:"codeExecution,omitempty"`
	GoogleSearch          json.RawMessage       `json:"googleSearch,omitempty"`
	GoogleSearchRetrieval json.RawMessage       `json:"googleSearchRetrieval,omitempty"`
	URLContext            json.RawMessage       `json:"urlContext,omitempty"`
	ComputerUse           json.RawMessage       `json:"computerUse,omitempty"`
	FileSearch            json.RawMessage       `json:"fileSearch,omitempty"`
	GoogleMaps            json.RawMessage       `json:"googleMaps,omitempty"`
	MCPServers            json.RawMessage       `json:"mcpServers,omitempty"`
}
type FunctionDeclaration struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description,omitempty"`
	Parameters           json.RawMessage `json:"parameters,omitempty"`
	ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema,omitempty"`
	Response             json.RawMessage `json:"response,omitempty"`
	ResponseJSONSchema   json.RawMessage `json:"responseJsonSchema,omitempty"`
	Behavior             string          `json:"behavior,omitempty"`
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
	TopK                       *int            `json:"topK,omitempty"`
	CandidateCount             *int            `json:"candidateCount,omitempty"`
	StopSequences              []string        `json:"stopSequences,omitempty"`
	ResponseMIMEType           string          `json:"responseMimeType,omitempty"`
	ResponseSchema             json.RawMessage `json:"responseSchema,omitempty"`
	ResponseJSONSchema         json.RawMessage `json:"responseJsonSchema,omitempty"`
	InternalResponseJSONSchema json.RawMessage `json:"_responseJsonSchema,omitempty"`
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
	EnableAffectiveDialog      *bool           `json:"enableAffectiveDialog,omitempty"`
	ResponseFormat             json.RawMessage `json:"responseFormat,omitempty"`
	TranslationConfig          json.RawMessage `json:"translationConfig,omitempty"`
	AudioTranscriptionConfig   json.RawMessage `json:"audioTranscriptionConfig,omitempty"`
}

type Candidate struct {
	Content               Content         `json:"content"`
	FinishReason          string          `json:"finishReason"`
	FinishMessage         string          `json:"finishMessage,omitempty"`
	Index                 int64           `json:"index,omitempty"`
	AvgLogprobs           *float64        `json:"avgLogprobs,omitempty"`
	LogprobsResult        json.RawMessage `json:"logprobsResult,omitempty"`
	SafetyRatings         json.RawMessage `json:"safetyRatings,omitempty"`
	CitationMetadata      json.RawMessage `json:"citationMetadata,omitempty"`
	GroundingMetadata     json.RawMessage `json:"groundingMetadata,omitempty"`
	URLContextMetadata    json.RawMessage `json:"urlContextMetadata,omitempty"`
	GroundingAttributions json.RawMessage `json:"groundingAttributions,omitempty"`
	TokenCount            *int64          `json:"tokenCount,omitempty"`
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
		CacheTokensDetails         json.RawMessage `json:"cacheTokensDetails,omitempty"`
		ServiceTier                string          `json:"serviceTier,omitempty"`
	} `json:"usageMetadata"`
	PromptFeedback json.RawMessage `json:"promptFeedback,omitempty"`
	ModelVersion   string          `json:"modelVersion"`
	ResponseID     string          `json:"responseId"`
	ModelStatus    json.RawMessage `json:"modelStatus,omitempty"`
}
