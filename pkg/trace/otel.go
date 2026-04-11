package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type OTelOptions struct {
	Provider       oteltrace.TracerProvider
	Exporter       string
	Endpoint       string
	Headers        map[string]string
	Insecure       bool
	ServiceName    string
	ServiceVersion string
	InstanceID     string
	SamplingRatio  float64
	SessionID      string
	Cwd            string
	ModelProvider  string
	ModelID        string
}

type activeSpan struct {
	ctx  context.Context
	span oteltrace.Span
}

type OTelBridge struct {
	mu            sync.Mutex
	tracer        oteltrace.Tracer
	shutdown      func(context.Context) error
	ownsProvider  bool
	sessionCtx    context.Context
	sessionSpan   oteltrace.Span
	currentTurn   int
	turnCtx       context.Context
	turnSpan      oteltrace.Span
	llmCtx        context.Context
	llmSpan       oteltrace.Span
	toolSpans     map[string]activeSpan
	modelProvider string
	modelID       string
}

func NewOTelBridge(ctx context.Context, opts OTelOptions) (*OTelBridge, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	provider := opts.Provider
	var shutdown func(context.Context) error
	var ownsProvider bool
	if provider == nil {
		tp, tpShutdown, err := newTracerProvider(ctx, opts)
		if err != nil {
			return nil, err
		}
		provider = tp
		shutdown = tpShutdown
		ownsProvider = true
	}

	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		serviceName = "modu-coding-agent"
	}
	tracer := provider.Tracer(serviceName)
	sessionCtx, sessionSpan := tracer.Start(ctx, "coding_agent.session",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithAttributes(
			attribute.String("agent.session_id", strings.TrimSpace(opts.SessionID)),
			attribute.String("agent.cwd", strings.TrimSpace(opts.Cwd)),
			attribute.String("llm.provider", strings.TrimSpace(opts.ModelProvider)),
			attribute.String("llm.model", strings.TrimSpace(opts.ModelID)),
		),
	)

	return &OTelBridge{
		tracer:        tracer,
		shutdown:      shutdown,
		ownsProvider:  ownsProvider,
		sessionCtx:    sessionCtx,
		sessionSpan:   sessionSpan,
		toolSpans:     make(map[string]activeSpan),
		modelProvider: strings.TrimSpace(opts.ModelProvider),
		modelID:       strings.TrimSpace(opts.ModelID),
	}, nil
}

func newTracerProvider(ctx context.Context, opts OTelOptions) (*sdktrace.TracerProvider, func(context.Context) error, error) {
	exporterName := strings.ToLower(strings.TrimSpace(opts.Exporter))
	if exporterName == "" {
		exporterName = "otlphttp"
	}

	var (
		exp sdktrace.SpanExporter
		err error
	)
	switch exporterName {
	case "otlphttp":
		var otlpOpts []otlptracehttp.Option
		if endpoint := strings.TrimSpace(opts.Endpoint); endpoint != "" {
			otlpOpts = append(otlpOpts, otlptracehttp.WithEndpoint(endpoint))
		}
		if len(opts.Headers) > 0 {
			otlpOpts = append(otlpOpts, otlptracehttp.WithHeaders(copyStringMap(opts.Headers)))
		}
		if opts.Insecure {
			otlpOpts = append(otlpOpts, otlptracehttp.WithInsecure())
		}
		exp, err = otlptracehttp.New(ctx, otlpOpts...)
	case "stdout":
		exp, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	default:
		return nil, nil, fmt.Errorf("unsupported otel exporter: %s", exporterName)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("init otel exporter: %w", err)
	}

	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		serviceName = "modu-coding-agent"
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", serviceName),
	}
	if version := strings.TrimSpace(opts.ServiceVersion); version != "" {
		attrs = append(attrs, attribute.String("service.version", version))
	}
	if instanceID := strings.TrimSpace(opts.InstanceID); instanceID != "" {
		attrs = append(attrs, attribute.String("service.instance.id", instanceID))
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
	if err != nil {
		return nil, nil, fmt.Errorf("init otel resource: %w", err)
	}

	ratio := opts.SamplingRatio
	if ratio <= 0 {
		ratio = 1
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)
	return tp, tp.Shutdown, nil
}

func (b *OTelBridge) RecordSessionEvent(eventType string, meta map[string]any) {
	if b == nil || b.sessionSpan == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if provider, _ := meta["provider"].(string); strings.TrimSpace(provider) != "" {
		b.modelProvider = strings.TrimSpace(provider)
		b.sessionSpan.SetAttributes(attribute.String("llm.provider", b.modelProvider))
	}
	if modelID, _ := meta["modelId"].(string); strings.TrimSpace(modelID) != "" {
		b.modelID = strings.TrimSpace(modelID)
		b.sessionSpan.SetAttributes(attribute.String("llm.model", b.modelID))
	}
	if sessionID, _ := meta["sessionId"].(string); strings.TrimSpace(sessionID) != "" {
		b.sessionSpan.SetAttributes(attribute.String("agent.session_id", sessionID))
	}
	if cwd, _ := meta["cwd"].(string); strings.TrimSpace(cwd) != "" {
		b.sessionSpan.SetAttributes(attribute.String("agent.cwd", cwd))
	}
	b.sessionSpan.AddEvent(eventType, oteltrace.WithAttributes(attributesFromMap(meta)...))
}

func (b *OTelBridge) RecordAgentEvent(event agent.AgentEvent) {
	if b == nil || b.sessionSpan == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	switch event.Type {
	case agent.EventTypeAgentStart:
		b.sessionSpan.AddEvent("agent_start")
	case agent.EventTypeTurnStart:
		b.startTurnLocked()
	case agent.EventTypeMessageEnd:
		b.recordMessageEndLocked(event.Message)
	case agent.EventTypeToolExecutionStart:
		b.startToolLocked(event)
	case agent.EventTypeToolExecutionEnd:
		b.endToolLocked(event)
	case agent.EventTypeInterrupt:
		meta := interruptMeta(event)
		b.activeSpanLocked().AddEvent("interrupt", oteltrace.WithAttributes(attributesFromMap(meta)...))
	case agent.EventTypeTurnEnd:
		if len(event.ToolResults) > 0 {
			b.activeSpanLocked().AddEvent("turn_end", oteltrace.WithAttributes(
				attribute.Int("tool.results", len(event.ToolResults)),
			))
		}
		b.endTurnLocked()
	case agent.EventTypeAgentEnd:
		if event.IsError {
			b.sessionSpan.SetStatus(codes.Error, "agent_end")
		}
		b.sessionSpan.AddEvent("agent_end")
	}
}

func (b *OTelBridge) Close(ctx context.Context, reason string) error {
	if b == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	b.mu.Lock()
	if b.sessionSpan != nil {
		if strings.TrimSpace(reason) != "" {
			b.sessionSpan.AddEvent("session_end", oteltrace.WithAttributes(attribute.String("reason", reason)))
		}
		for id := range b.toolSpans {
			active := b.toolSpans[id]
			active.span.End()
			delete(b.toolSpans, id)
		}
		if b.llmSpan != nil {
			b.llmSpan.End()
			b.llmSpan = nil
		}
		if b.turnSpan != nil {
			b.turnSpan.End()
			b.turnSpan = nil
		}
		b.sessionSpan.End()
		b.sessionSpan = nil
	}
	shutdown := b.shutdown
	ownsProvider := b.ownsProvider
	b.mu.Unlock()

	if ownsProvider && shutdown != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}

func (b *OTelBridge) startTurnLocked() {
	if b.turnSpan != nil {
		b.endTurnLocked()
	}
	b.currentTurn++
	b.turnCtx, b.turnSpan = b.tracer.Start(b.sessionCtx, "coding_agent.turn",
		oteltrace.WithAttributes(
			attribute.Int("agent.turn", b.currentTurn),
			attribute.String("llm.provider", b.modelProvider),
			attribute.String("llm.model", b.modelID),
		),
	)
	b.llmCtx, b.llmSpan = b.tracer.Start(b.turnCtx, "coding_agent.llm",
		oteltrace.WithAttributes(
			attribute.Int("agent.turn", b.currentTurn),
			attribute.String("llm.provider", b.modelProvider),
			attribute.String("llm.model", b.modelID),
		),
	)
}

func (b *OTelBridge) endTurnLocked() {
	if b.llmSpan != nil {
		b.llmSpan.End()
		b.llmSpan = nil
	}
	if b.turnSpan != nil {
		b.turnSpan.End()
		b.turnSpan = nil
	}
	b.turnCtx = nil
	b.llmCtx = nil
}

func (b *OTelBridge) recordMessageEndLocked(msg agent.AgentMessage) {
	role, preview, usage, stopReason := summarizeMessage(msg, defaultPreviewLimit)
	attrs := []attribute.KeyValue{
		attribute.String("message.role", role),
		attribute.String("message.preview", preview),
	}
	if stopReason != "" {
		attrs = append(attrs, attribute.String("llm.stop_reason", stopReason))
	}

	target := b.activeSpanLocked()
	target.AddEvent("message_end", oteltrace.WithAttributes(attrs...))

	if role != string(agent.RoleAssistant) {
		return
	}

	usageAttrs := usageAttributes(usage)
	if stopReason != "" {
		usageAttrs = append(usageAttrs, attribute.String("llm.stop_reason", stopReason))
	}
	if preview != "" {
		usageAttrs = append(usageAttrs, attribute.String("llm.output_preview", preview))
	}

	if b.llmSpan != nil {
		b.llmSpan.SetAttributes(usageAttrs...)
		b.llmSpan.AddEvent("assistant_message", oteltrace.WithAttributes(attrs...))
		b.llmSpan.End()
		b.llmSpan = nil
	}
	if b.turnSpan != nil {
		b.turnSpan.SetAttributes(usageAttrs...)
	}
}

func (b *OTelBridge) startToolLocked(event agent.AgentEvent) {
	parent := b.turnCtx
	if parent == nil {
		parent = b.sessionCtx
	}
	name := strings.TrimSpace(event.ToolName)
	if name == "" {
		name = "unknown"
	}
	ctx, span := b.tracer.Start(parent, "coding_agent.tool."+name,
		oteltrace.WithAttributes(
			attribute.Int("agent.turn", b.currentTurn),
			attribute.String("tool.name", name),
			attribute.String("tool.call_id", event.ToolCallID),
			attribute.Bool("tool.parallel", event.Parallel),
			attribute.String("tool.args_json", marshalJSON(cloneAnyMap(event.Args))),
		),
	)
	b.toolSpans[event.ToolCallID] = activeSpan{ctx: ctx, span: span}
}

func (b *OTelBridge) endToolLocked(event agent.AgentEvent) {
	active, ok := b.toolSpans[event.ToolCallID]
	if !ok {
		return
	}
	preview, details := summarizeToolResult(event.Result, defaultPreviewLimit)
	attrs := []attribute.KeyValue{
		attribute.String("tool.name", event.ToolName),
		attribute.String("tool.call_id", event.ToolCallID),
		attribute.Bool("tool.parallel", event.Parallel),
		attribute.Bool("tool.is_error", event.IsError),
	}
	if preview != "" {
		attrs = append(attrs, attribute.String("tool.result_preview", preview))
	}
	if details != nil {
		attrs = append(attrs, attribute.String("tool.details_json", marshalJSON(details)))
	}
	active.span.SetAttributes(attrs...)
	if event.IsError {
		active.span.SetStatus(codes.Error, preview)
	}
	active.span.End()
	delete(b.toolSpans, event.ToolCallID)
}

func (b *OTelBridge) activeSpanLocked() oteltrace.Span {
	if b.turnSpan != nil {
		return b.turnSpan
	}
	return b.sessionSpan
}

func usageAttributes(usage types.AgentUsage) []attribute.KeyValue {
	total := usage.TotalTokens
	if total == 0 {
		total = usage.Input + usage.Output
	}
	return []attribute.KeyValue{
		attribute.Int("llm.usage.input_tokens", usage.Input),
		attribute.Int("llm.usage.output_tokens", usage.Output),
		attribute.Int("llm.usage.cache_read_tokens", usage.CacheRead),
		attribute.Int("llm.usage.cache_write_tokens", usage.CacheWrite),
		attribute.Int("llm.usage.total_tokens", total),
	}
}

func attributesFromMap(meta map[string]any) []attribute.KeyValue {
	if len(meta) == 0 {
		return nil
	}
	attrs := make([]attribute.KeyValue, 0, len(meta))
	for key, value := range meta {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		switch v := value.(type) {
		case string:
			attrs = append(attrs, attribute.String(key, v))
		case bool:
			attrs = append(attrs, attribute.Bool(key, v))
		case int:
			attrs = append(attrs, attribute.Int(key, v))
		case int64:
			attrs = append(attrs, attribute.Int64(key, v))
		case float64:
			attrs = append(attrs, attribute.Float64(key, v))
		default:
			attrs = append(attrs, attribute.String(key, marshalJSON(v)))
		}
	}
	return attrs
}

func marshalJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
