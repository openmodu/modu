package subagent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Control notice event types — match pi-subagents' notifyOn vocabulary.
const (
	controlEventActiveLongRunning = "active_long_running"
	controlEventNeedsAttention    = "needs_attention"
)

// Control notification channels — match pi-subagents' notifyChannels.
const (
	controlChannelEvent    = "event"    // api.Notify
	controlChannelAsync    = "async"    // no-op until the host exposes a per-task notice field
	controlChannelIntercom = "intercom" // append to the run's intercom inbox
)

// controlOptions is the decoded form of the top-level `control` argument.
// Mirrors pi-subagents' ControlOverrides; runtime support is split between
// fields the extension can drive on its own and fields that need host
// hooks we don't have. See PARITY.md.
type controlOptions struct {
	enabled bool

	// activeNoticeAfterMs and needsAttentionAfterMs are clock-based: the
	// extension can drive them with timers without any host help.
	activeNoticeAfterMs   int
	needsAttentionAfterMs int

	// notifyOn filters which control event types actually fire. Empty
	// means "fire every event". Unknown values are silently ignored.
	notifyOn []string

	// notifyChannels selects delivery routes. Empty defaults to "event".
	notifyChannels []string

	// The following fields are parsed for schema compatibility with
	// pi-subagents but not yet honored at runtime:
	//   - activeNoticeAfterTurns / activeNoticeAfterTokens — need per-run
	//     turn / token counters the host doesn't expose to extensions.
	//   - failedToolAttemptsBeforeAttention — needs a tool-failure
	//     callback from the host's child agent.
	// We keep them on the struct so doctor or future host work can pick
	// them up; PARITY.md tracks the gap.
	activeNoticeAfterTurns            int
	activeNoticeAfterTokens           int
	failedToolAttemptsBeforeAttention int
}

func (c *controlOptions) wantsEvent(name string) bool {
	if c == nil {
		return false
	}
	if len(c.notifyOn) == 0 {
		return true
	}
	for _, n := range c.notifyOn {
		if strings.EqualFold(strings.TrimSpace(n), name) {
			return true
		}
	}
	return false
}

// effectiveChannels returns the channels to use for delivery, defaulting
// to ["event"] when the caller passed nothing.
func (c *controlOptions) effectiveChannels() []string {
	if c == nil || len(c.notifyChannels) == 0 {
		return []string{controlChannelEvent}
	}
	out := make([]string, 0, len(c.notifyChannels))
	for _, ch := range c.notifyChannels {
		out = append(out, strings.ToLower(strings.TrimSpace(ch)))
	}
	return out
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
	if err := decodePositiveIntField(obj, "activeNoticeAfterMs", &out.activeNoticeAfterMs); err != nil {
		return nil, err
	}
	if err := decodePositiveIntField(obj, "needsAttentionAfterMs", &out.needsAttentionAfterMs); err != nil {
		return nil, err
	}
	if err := decodePositiveIntField(obj, "activeNoticeAfterTurns", &out.activeNoticeAfterTurns); err != nil {
		return nil, err
	}
	if err := decodePositiveIntField(obj, "activeNoticeAfterTokens", &out.activeNoticeAfterTokens); err != nil {
		return nil, err
	}
	if err := decodePositiveIntField(obj, "failedToolAttemptsBeforeAttention", &out.failedToolAttemptsBeforeAttention); err != nil {
		return nil, err
	}
	if v, present := obj["notifyOn"]; present {
		names, err := decodeStringArray(v, "control.notifyOn")
		if err != nil {
			return nil, err
		}
		out.notifyOn = names
	}
	if v, present := obj["notifyChannels"]; present {
		names, err := decodeStringArray(v, "control.notifyChannels")
		if err != nil {
			return nil, err
		}
		out.notifyChannels = names
	}
	return out, nil
}

func decodePositiveIntField(obj map[string]any, key string, dst *int) error {
	raw, present := obj[key]
	if !present {
		return nil
	}
	n, ok := numericInt(raw)
	if !ok || n < 1 {
		return fmt.Errorf("control.%s must be a positive integer, got %v", key, raw)
	}
	*dst = n
	return nil
}

func decodeStringArray(raw any, label string) ([]string, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array, got %T", label, raw)
	}
	out := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be string, got %T", label, i, item)
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// controlActiveNoticeWatcher launches up to two single-shot timers that
// emit notices when a batch run is still in flight past the configured
// thresholds. The returned cancel func must be called by the goroutine
// that drives the batch on completion so any pending timers are
// suppressed.
//
// Delivery is shared across "event" (api.Notify) and "intercom" (append
// to the batch task's inbox JSONL). The "async" channel is parsed for
// schema compatibility but stays a no-op until the host exposes a way
// to attach notices to its task record.
func controlActiveNoticeWatcher(ext *Extension, ctrl *controlOptions, taskID, mode string) (cancel func()) {
	if ctrl == nil || ext == nil || ext.api == nil {
		return func() {}
	}
	if ctrl.activeNoticeAfterMs <= 0 && ctrl.needsAttentionAfterMs <= 0 {
		return func() {}
	}
	stop := make(chan struct{})
	var once sync.Once
	if ctrl.activeNoticeAfterMs > 0 {
		go runControlTimer(ext, ctrl, stop, ctrl.activeNoticeAfterMs, controlEventActiveLongRunning, fmt.Sprintf(
			"Subagent batch task %s (%s mode) is still running past %dms — consider `subagent action=status id=%s` to check progress.",
			taskID, mode, ctrl.activeNoticeAfterMs, taskID,
		), taskID)
	}
	if ctrl.needsAttentionAfterMs > 0 {
		go runControlTimer(ext, ctrl, stop, ctrl.needsAttentionAfterMs, controlEventNeedsAttention, fmt.Sprintf(
			"Subagent batch task %s (%s mode) appears stuck past %dms — inspect with `subagent action=status id=%s` or interrupt via `subagent action=interrupt id=%s`.",
			taskID, mode, ctrl.needsAttentionAfterMs, taskID, taskID,
		), taskID)
	}
	return func() {
		once.Do(func() { close(stop) })
	}
}

func runControlTimer(ext *Extension, ctrl *controlOptions, stop <-chan struct{}, afterMs int, eventName, text, taskID string) {
	select {
	case <-time.After(time.Duration(afterMs) * time.Millisecond):
		if !ctrl.wantsEvent(eventName) {
			return
		}
		deliverControlNotice(ext, ctrl, taskID, eventName, text)
	case <-stop:
	}
}

func deliverControlNotice(ext *Extension, ctrl *controlOptions, taskID, eventName, text string) {
	for _, ch := range ctrl.effectiveChannels() {
		switch ch {
		case controlChannelEvent:
			ext.api.Notify(ext.Name(), text)
		case controlChannelIntercom:
			// Append a structured intercom message addressed to the
			// batch task itself so a consumer polling its inbox sees
			// the same notice the event channel emitted.
			_ = appendIntercomMessage(ext, taskID, "control:"+eventName, text)
		case controlChannelAsync:
			// No-op: the host's background task record has no field
			// for accumulating control notices yet. See PARITY.md.
		}
	}
}
