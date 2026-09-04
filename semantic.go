package routemorph

import core "github.com/2218342221/RouteMorphSDK/internal/core"

// Diagnostic describes a non-fatal approximation or omission made during
// protocol conversion.
type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
}

func fromCoreDiagnostics(values []core.Diagnostic) []Diagnostic {
	if values == nil {
		return nil
	}
	converted := make([]Diagnostic, len(values))
	for index, value := range values {
		converted[index] = Diagnostic{
			Severity: value.Severity,
			Code:     value.Code,
			Path:     value.Path,
			Message:  value.Message,
		}
	}
	return converted
}
