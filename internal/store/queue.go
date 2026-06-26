package store

import (
	"encoding/json"
	"strconv"
)

func queueResult(priority int, attempt int, maxAttempts int, reason string) map[string]any {
	if attempt < 1 {
		attempt = 1
	}
	if maxAttempts < attempt {
		maxAttempts = attempt
	}
	return map[string]any{
		"status":         "queued",
		"queue_priority": priority,
		"retry_attempt":  attempt,
		"max_attempts":   maxAttempts,
		"queued_reason":  reason,
	}
}

func intFromMap(values map[string]any, key string, fallback int) int {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}
