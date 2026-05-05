package tokenkit

import (
	"regexp"
	"strings"
	"sync"
)

type ModelPrice struct {
	InputPerMillion       float64
	CachedInputPerMillion *float64
	OutputPerMillion      float64
}

type CostInput struct {
	Model             string
	Provider          string
	MeasurementMethod string
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
}

var builtinPrices = map[string]ModelPrice{
	"GPT-5.5":           price(5.00, 0.50, 30.00),
	"GPT-5.5 Pro":       priceNoCache(30.00, 180.00),
	"GPT-5.4":           price(2.50, 0.25, 15.00),
	"GPT-5.4 Mini":      price(0.75, 0.075, 4.50),
	"GPT-5.3 Codex":     price(1.75, 0.175, 14.00),
	"GPT-5.2":           price(1.75, 0.175, 14.00),
	"GPT-5":             price(1.25, 0.125, 10.00),
	"GPT-5 Codex":       price(1.25, 0.125, 10.00),
	"GPT-5 Mini":        price(0.25, 0.025, 2.00),
	"GPT-4.1":           price(2.00, 0.50, 8.00),
	"Claude Sonnet 4.6": price(3.00, 0.30, 15.00),
	"Claude Sonnet 4.5": price(3.00, 0.30, 15.00),
	"Claude Haiku 4.5":  price(1.00, 0.10, 5.00),
	"Claude Opus 4.7":   price(5.00, 0.50, 25.00),
	"Claude Opus 4.6":   price(5.00, 0.50, 25.00),
	"Claude Opus 4.5":   price(5.00, 0.50, 25.00),
	"Claude Sonnet 4":   price(3.00, 0.30, 15.00),
	"Claude Opus 4":     price(15.00, 1.50, 75.00),
	"Gemini 3 Pro":      price(2.00, 0.20, 12.00),
	"Gemini 2.5 Pro":    price(1.25, 0.125, 10.00),
	"Gemini 2.5 Flash":  price(0.30, 0.03, 2.50),
}

var (
	parenSuffixRE = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
	claudeAPIRE   = regexp.MustCompile(`(?i)^claude[- ]?(sonnet|opus|haiku)[- ]?(\d+(?:[-.]\d+)?)(?:[- ]\d{8})?(.*)$`)
	claudeTextRE  = regexp.MustCompile(`(?i)^claude\s+(sonnet|opus|haiku)\s+([0-9.]+)(.*)$`)
	gptRE         = regexp.MustCompile(`(?i)^gpt[- ]?([0-9.]+)(?:[- ]?(mini|nano|codex|pro))?(.*)$`)
	geminiRE      = regexp.MustCompile(`(?i)^gemini[- ]?([0-9.]+)(?:[- ]?(pro|flash|flash-lite))?(.*)$`)
)

var (
	normCache = make(map[string]string)
	normMu    sync.RWMutex
)

func price(input, cachedInput, output float64) ModelPrice {
	return ModelPrice{InputPerMillion: input, CachedInputPerMillion: &cachedInput, OutputPerMillion: output}
}

func priceNoCache(input, output float64) ModelPrice {
	return ModelPrice{InputPerMillion: input, OutputPerMillion: output}
}

func EstimateCostUSD(input CostInput) *float64 {
	if input.MeasurementMethod != "" && input.MeasurementMethod != MethodExact {
		return nil
	}
	if input.InputTokens <= 0 && input.CachedInputTokens <= 0 && input.OutputTokens <= 0 {
		return nil
	}
	model := NormalizeModelDisplay(input.Model)
	model = strings.TrimSpace(parenSuffixRE.ReplaceAllString(model, ""))
	profile, ok := builtinPrices[model]
	if !ok {
		return nil
	}
	cachedRate := profile.InputPerMillion
	if profile.CachedInputPerMillion != nil {
		cachedRate = *profile.CachedInputPerMillion
	}

	uncachedInput := input.InputTokens
	cachedInput := input.CachedInputTokens
	if !usesDisjointCachedInput(input.Provider, model) {
		if cachedInput > uncachedInput {
			cachedInput = uncachedInput
		}
		uncachedInput -= cachedInput
	}
	cost := float64(uncachedInput)/1_000_000*profile.InputPerMillion +
		float64(cachedInput)/1_000_000*cachedRate +
		float64(input.OutputTokens)/1_000_000*profile.OutputPerMillion
	return &cost
}

func NormalizeModelDisplay(model string) string {
	raw := strings.TrimSpace(model)
	if raw == "" {
		return "unknown"
	}

	normMu.RLock()
	if cached, ok := normCache[raw]; ok {
		normMu.RUnlock()
		return cached
	}
	normMu.RUnlock()

	normalized := normalizeModel(raw)

	normMu.Lock()
	normCache[raw] = normalized
	normMu.Unlock()

	return normalized
}

func normalizeModel(raw string) string {
	suffix := ""
	if match := parenSuffixRE.FindString(raw); match != "" {
		suffix = " " + strings.TrimSpace(match)
		raw = strings.TrimSpace(strings.TrimSuffix(raw, match))
	}
	if match := claudeAPIRE.FindStringSubmatch(strings.ReplaceAll(raw, "_", "-")); len(match) == 4 {
		return "Claude " + titleLower(match[1]) + " " + strings.ReplaceAll(match[2], "-", ".") + strings.TrimSpace(match[3]) + suffix
	}
	if match := claudeTextRE.FindStringSubmatch(raw); len(match) == 4 {
		return "Claude " + titleLower(match[1]) + " " + match[2] + strings.TrimSpace(match[3]) + suffix
	}
	if match := gptRE.FindStringSubmatch(strings.ReplaceAll(raw, "_", "-")); len(match) == 4 {
		label := "GPT-" + match[1]
		if match[2] != "" {
			label += " " + titleLower(match[2])
		}
		return strings.TrimSpace(label+match[3]) + suffix
	}
	if match := geminiRE.FindStringSubmatch(strings.ReplaceAll(raw, "_", "-")); len(match) == 4 {
		label := "Gemini " + match[1]
		if match[2] != "" {
			label += " " + titleWords(match[2])
		}
		return strings.TrimSpace(label+match[3]) + suffix
	}
	return raw
}

func usesDisjointCachedInput(provider, model string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	return p == "anthropic" || p == "claude" || strings.HasPrefix(model, "Claude ")
}

func titleLower(value string) string {
	value = strings.ToLower(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func titleWords(value string) string {
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i := range parts {
		parts[i] = titleLower(parts[i])
	}
	return strings.Join(parts, " ")
}
