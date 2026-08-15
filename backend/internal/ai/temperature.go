package ai

import (
	"errors"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"paperless-go/backend/internal/aiprovider"
)

func completionTemperature(model string, value float64) param.Opt[float64] {
	if !aiprovider.AllowsCustomTemperature(model) {
		return param.Opt[float64]{}
	}
	return openai.Float(value)
}

func isUnsupportedTemperatureError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if strings.EqualFold(strings.TrimSpace(apiErr.Param), "temperature") {
			return true
		}
		msg := strings.ToLower(apiErr.Message)
		if strings.Contains(msg, "temperature") && strings.Contains(msg, "unsupported") {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "temperature") &&
		(strings.Contains(msg, "does not support") || strings.Contains(msg, "unsupported"))
}
