// Package logger provides structured, AI-friendly logging for modu.
//
// It outputs human-readable text to stderr and optionally JSONL to a
// log file. The API follows the slog key-value convention so callers
// write:
//
//	logger.Info("request handled", "method", "GET", "status", 200)
//	logger.Error("failed to connect", "host", host, "err", err)
//
// For component-scoped logging, create a sub-logger:
//
//	log := logger.With("component", "moms")
//	log.Info("bot started", "chat_id", 42)
//
// The JSON log file (if enabled via EnableFileLogging) writes one JSON
// object per line, making it easy for AI agents to grep or parse:
//
//	{"level":"INFO","ts":"2026-02-27T14:00:00Z","msg":"bot started","component":"moms","chat_id":42}
package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Level represents a log severity level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

var levelNames = [...]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
	LevelFatal: "FATAL",
}

func (l Level) String() string {
	if int(l) < len(levelNames) {
		return levelNames[l]
	}
	return fmt.Sprintf("LEVEL(%d)", l)
}

// ParseLevel parses a level name (case-insensitive). Returns LevelInfo
// if the name is not recognized.
func ParseLevel(s string) Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return LevelDebug
	case "INFO":
		return LevelInfo
	case "WARN", "WARNING":
		return LevelWarn
	case "ERROR":
		return LevelError
	case "FATAL":
		return LevelFatal
	default:
		return LevelInfo
	}
}

// ------------------------------------------------------------------
// Global state
// ------------------------------------------------------------------

var (
	mu           sync.RWMutex
	globalLevel  = LevelInfo
	fileWriter   io.WriteCloser
	callerDepth  = 3 // default for package-level functions
)

// SetLevel sets the global minimum log level.
func SetLevel(l Level) {
	mu.Lock()
	globalLevel = l
	mu.Unlock()
}

// GetLevel returns the current global log level.
func GetLevel() Level {
	mu.RLock()
	defer mu.RUnlock()
	return globalLevel
}

// EnableFileLogging opens (or replaces) the JSON log file.
func EnableFileLogging(path string) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("logger: open %q: %w", path, err)
	}
	if fileWriter != nil {
		fileWriter.Close()
	}
	fileWriter = f
	return nil
}

// DisableFileLogging closes the JSON log file.
func DisableFileLogging() {
	mu.Lock()
	defer mu.Unlock()
	if fileWriter != nil {
		fileWriter.Close()
		fileWriter = nil
	}
}

// ------------------------------------------------------------------
// Logger (can carry preset key-value pairs)
// ------------------------------------------------------------------

// Logger is an immutable structured logger. Create one via With() to
// attach persistent fields, or use the package-level functions directly.
type Logger struct {
	// preset is a list of key-value pairs that are prepended to every
	// log entry produced by this Logger.
	preset []any
}

// With returns a new Logger that includes the given key-value pairs in
// every log entry. Keys must be strings; values can be any type.
//
//	log := logger.With("component", "moms", "version", "1.0")
func With(keysAndValues ...any) *Logger {
	return &Logger{preset: keysAndValues}
}

// With returns a child Logger with additional preset fields.
func (l *Logger) With(keysAndValues ...any) *Logger {
	merged := make([]any, 0, len(l.preset)+len(keysAndValues))
	merged = append(merged, l.preset...)
	merged = append(merged, keysAndValues...)
	return &Logger{preset: merged}
}

// Debug logs at DEBUG level.
func (l *Logger) Debug(msg string, keysAndValues ...any) {
	l.log(LevelDebug, msg, keysAndValues)
}

// Info logs at INFO level.
func (l *Logger) Info(msg string, keysAndValues ...any) {
	l.log(LevelInfo, msg, keysAndValues)
}

// Warn logs at WARN level.
func (l *Logger) Warn(msg string, keysAndValues ...any) {
	l.log(LevelWarn, msg, keysAndValues)
}

// Error logs at ERROR level.
func (l *Logger) Error(msg string, keysAndValues ...any) {
	l.log(LevelError, msg, keysAndValues)
}

// Fatal logs at FATAL level and terminates the process.
func (l *Logger) Fatal(msg string, keysAndValues ...any) {
	l.log(LevelFatal, msg, keysAndValues)
	os.Exit(1)
}

func (l *Logger) log(level Level, msg string, kv []any) {
	mu.RLock()
	if level < globalLevel {
		mu.RUnlock()
		return
	}
	mu.RUnlock()

	now := time.Now().UTC()
	caller := getCaller(callerDepth)

	// Merge preset + call-site kv.
	allKV := l.preset
	if len(kv) > 0 {
		merged := make([]any, 0, len(l.preset)+len(kv))
		merged = append(merged, l.preset...)
		merged = append(merged, kv...)
		allKV = merged
	}

	// Write human-readable to stderr.
	writeText(level, now, msg, allKV)

	// Write JSON to file (if enabled).
	mu.RLock()
	fw := fileWriter
	mu.RUnlock()
	if fw != nil {
		writeJSON(fw, level, now, caller, msg, allKV)
	}
}

// ------------------------------------------------------------------
// Package-level convenience functions
// ------------------------------------------------------------------

// Debug logs at DEBUG level using the global logger.
func Debug(msg string, keysAndValues ...any) {
	logGlobal(LevelDebug, msg, keysAndValues)
}

// Info logs at INFO level using the global logger.
func Info(msg string, keysAndValues ...any) {
	logGlobal(LevelInfo, msg, keysAndValues)
}

// Warn logs at WARN level using the global logger.
func Warn(msg string, keysAndValues ...any) {
	logGlobal(LevelWarn, msg, keysAndValues)
}

// Error logs at ERROR level using the global logger.
func Error(msg string, keysAndValues ...any) {
	logGlobal(LevelError, msg, keysAndValues)
}

// Fatal logs at FATAL level and terminates the process.
func Fatal(msg string, keysAndValues ...any) {
	logGlobal(LevelFatal, msg, keysAndValues)
	os.Exit(1)
}

func logGlobal(level Level, msg string, kv []any) {
	mu.RLock()
	if level < globalLevel {
		mu.RUnlock()
		return
	}
	mu.RUnlock()

	now := time.Now().UTC()
	caller := getCaller(callerDepth)

	writeText(level, now, msg, kv)

	mu.RLock()
	fw := fileWriter
	mu.RUnlock()
	if fw != nil {
		writeJSON(fw, level, now, caller, msg, kv)
	}
}

// ------------------------------------------------------------------
// Output formatters
// ------------------------------------------------------------------

func writeText(level Level, ts time.Time, msg string, kv []any) {
	var b strings.Builder
	b.WriteString(ts.Format("15:04:05.000"))
	b.WriteByte(' ')

	// Level with colour hint (works in most terminals).
	switch level {
	case LevelDebug:
		b.WriteString("\033[90mDBG\033[0m")
	case LevelInfo:
		b.WriteString("\033[36mINF\033[0m")
	case LevelWarn:
		b.WriteString("\033[33mWRN\033[0m")
	case LevelError:
		b.WriteString("\033[31mERR\033[0m")
	case LevelFatal:
		b.WriteString("\033[35mFTL\033[0m")
	}

	b.WriteByte(' ')
	b.WriteString(msg)

	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprintf("\033[90m%v\033[0m=%v", kv[i], kv[i+1]))
	}
	b.WriteByte('\n')
	fmt.Fprint(os.Stderr, b.String())
}

func writeJSON(w io.Writer, level Level, ts time.Time, caller string, msg string, kv []any) {
	entry := make(map[string]any, 4+len(kv)/2)
	entry["level"] = level.String()
	entry["ts"] = ts.Format(time.RFC3339)
	entry["msg"] = msg
	if caller != "" {
		entry["caller"] = caller
	}

	// Flatten key-value pairs into the entry map.
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", kv[i])
		}
		entry[key] = kv[i+1]
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	mu.Lock()
	w.Write(data)
	w.Write([]byte{'\n'})
	mu.Unlock()
}

func getCaller(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return ""
	}
	// Shorten path: keep only the last two segments.
	short := file
	if idx := strings.LastIndex(file, "/"); idx > 0 {
		if idx2 := strings.LastIndex(file[:idx], "/"); idx2 > 0 {
			short = file[idx2+1:]
		}
	}
	return fmt.Sprintf("%s:%d", short, line)
}
