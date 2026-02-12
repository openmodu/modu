package utils

import (
	"regexp"

	"github.com/crosszan/modu/pkg/llm"
)

var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`prompt is too long`),
	regexp.MustCompile(`input is too long for requested model`),
	regexp.MustCompile(`exceeds the context window`),
	regexp.MustCompile(`input token count.*exceeds the maximum`),
	regexp.MustCompile(`maximum prompt length is \d+`),
	regexp.MustCompile(`reduce the length of the messages`),
	regexp.MustCompile(`maximum context length is \d+ tokens`),
	regexp.MustCompile(`exceeds the limit of \d+`),
	regexp.MustCompile(`exceeds the available context size`),
	regexp.MustCompile(`greater than the context length`),
	regexp.MustCompile(`context window exceeds limit`),
	regexp.MustCompile(`exceeded model token limit`),
	regexp.MustCompile(`context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`too many tokens`),
	regexp.MustCompile(`token limit exceeded`),
}

func IsContextOverflow(message *llm.AssistantMessage, contextWindow int) bool {
	if message == nil {
		return false
	}
	if message.StopReason == "error" && message.ErrorMessage != "" {
		for _, p := range overflowPatterns {
			if p.MatchString(message.ErrorMessage) {
				return true
			}
		}
		if regexp.MustCompile(`^4(00|13)\s*(status code)?\s*\(no body\)`).MatchString(message.ErrorMessage) {
			return true
		}
	}
	if contextWindow > 0 && message.StopReason == "stop" {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		if inputTokens > contextWindow {
			return true
		}
	}
	return false
}

func GetOverflowPatterns() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(overflowPatterns))
	out = append(out, overflowPatterns...)
	return out
}
