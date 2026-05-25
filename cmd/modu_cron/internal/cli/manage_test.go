package cli

import (
	"strings"
	"testing"
)

func TestFrameManagePromptAllowsCronManagementOnly(t *testing.T) {
	prompt := frameManagePrompt("帮我看看现在有哪些任务")
	for _, want := range []string{
		"cron_list",
		"cron_add",
		"cron_remove",
		"If the request is ambiguous",
		"Do not invent notification channel names",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "帮我看看现在有哪些任务") {
		t.Fatalf("prompt missing original request:\n%s", prompt)
	}
}
