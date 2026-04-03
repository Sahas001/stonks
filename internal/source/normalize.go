package source

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func strField(row map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := row[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				return trimmed
			}
		default:
			rendered := strings.TrimSpace(fmt.Sprint(typed))
			if rendered != "" && rendered != "<nil>" {
				return rendered
			}
		}
	}
	return ""
}

func numField(row map[string]any, keys ...string) float64 {
	for _, key := range keys {
		value, ok := row[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed
		case float32:
			return float64(typed)
		case int:
			return float64(typed)
		case int64:
			return float64(typed)
		case jsonNumber:
			if parsed, err := strconv.ParseFloat(string(typed), 64); err == nil {
				return parsed
			}
		case string:
			cleaned := strings.ReplaceAll(strings.TrimSpace(typed), ",", "")
			if parsed, err := strconv.ParseFloat(cleaned, 64); err == nil {
				return parsed
			}
		}
	}
	return 0
}

type jsonNumber string

func timeField(row map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		value, ok := row[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			for _, layout := range []string{
				time.RFC3339,
				"2006-01-02 15:04:05",
				"2006-01-02",
				"02-01-2006 15:04:05",
				"02-01-2006",
			} {
				if parsed, err := time.Parse(layout, strings.TrimSpace(typed)); err == nil {
					return parsed
				}
			}
		}
	}
	if contract := strField(row, "cn", "contractNumber"); len(contract) >= 14 {
		if parsed, err := time.Parse("20060102150405", contract[:14]); err == nil {
			return parsed
		}
	}
	return time.Now().UTC()
}
