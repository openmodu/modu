package subagent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// controlOptions is the decoded form of the top-level `control` argument.
// Mirrors pi-subagents' ControlOverrides shape; only the subset we actually
// implement today is honored at runtime. The rest is accepted to keep
// configs forward-compatible — see PARITY.md for the deferred fields.
type controlOptions struct {
	enabled bool
	// activeNoticeAfterMs fires one api.Notify when a batch async run is
	// still running past this many milliseconds. We don't track turns or
	// tokens yet, so the *AfterTurns / *AfterTokens fields are parsed for
	// schema compatibility but currently ignored.
	activeNoticeAfterMs int
	// notifyChannels is parsed for schema compatibility. Today only the
	// "event" channel (api.Notify) is wired; the "async" / "intercom"
	// channels are deferred.
	notifyChannels []string
}

// decodeControlOptions parses the top-level args["control"] map. Returns
// nil (no controls) when the arg is missing or `enabled: false`.
func decodeControlOptions(raw any) (*controlOptions, error) {
	if raw == nil {
		return nil, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("control must be an object, got %T", raw)
	}
	out := &controlOptions{enabled: true}
	if v, present := obj["enabled"]; present {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("control.enabled must be boolean, got %T", v)
		}
		out.enabled = b
	}
	if !out.enabled {
		return nil, nil
	}
	if v, present := obj["activeNoticeAfterMs"]; present {
		n, ok := numericInt(v)
		if !ok || n < 1 {
			return nil, fmt.Errorf("control.activeNoticeAfterMs must be a positive integer, got %v", v)
		}
		out.activeNoticeAfterMs = n
	}
	if v, present := obj["notifyChannels"]; present {
		arr, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("control.notifyChannels must be an array, got %T", v)
		}
		for i, item := range arr {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("control.notifyChannels[%d] must be string, got %T", i, item)
			}
			out.notifyChannels = append(out.notifyChannels, s)
		}
	}
	return out, nil
}

// controlActiveNoticeWatcher launches a single-shot timer that emits a
// "long running" notification when the batch run still isn't done after
// the configured threshold. The returned cancel func must be called by the
// goroutine that drives the batch on completion (success or failure) so
// the timer is reclaimed and the notification suppressed.
//
// The notifier is invoked synchronously inside the goroutine. We do not
// touch the host's notification channels beyond api.Notify — adding
// "async" / "intercom" deliveries waits on broader host design.
func controlActiveNoticeWatcher(ext *Extension, ctrl *controlOptions, taskID, mode string) (cancel func()) {
	if ctrl == nil || ctrl.activeNoticeAfterMs <= 0 || ext == nil || ext.api == nil {
		return func() {}
	}
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-time.After(time.Duration(ctrl.activeNoticeAfterMs) * time.Millisecond):
			ext.api.Notify(ext.Name(), fmt.Sprintf(
				"Subagent batch task %s (%s mode) is still running past %dms — consider `subagent action=status id=%s` to check progress.",
				taskID, mode, ctrl.activeNoticeAfterMs, taskID,
			))
		case <-stop:
		}
	}()
	return func() {
		once.Do(func() { close(stop) })
	}
}

// stringNotifyChannelsContains is a cheap allowlist check the timer body
// could grow into when more channels land. Kept exported in spirit (lower
// case helper, but available to future additions) so notifyChannels has at
// least one read-site.
func stringNotifyChannelsContains(channels []string, want string) bool {
	for _, c := range channels {
		if strings.EqualFold(strings.TrimSpace(c), want) {
			return true
		}
	}
	return false
}
