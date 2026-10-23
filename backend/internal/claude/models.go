package claude

import "agentrove/internal/models"

// ContextWindow is the model's context size; unknown ids get a conservative default.
func ContextWindow(id string) int {
	if m, ok := models.LookupModel(id); ok {
		return m.ContextWindow
	}
	return 200_000
}

// Effort validates a thinking mode; "" means the CLI default.
func Effort(mode *string) (string, bool) {
	if mode == nil || *mode == "" {
		return "", true
	}
	for _, e := range models.EffortLevels {
		if e == *mode {
			return e, true
		}
	}
	return "", false
}
