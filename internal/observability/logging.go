package observability

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// contextKey is a type for context keys used by the logging package.
type contextKey string

const (
	// Context keys for request-scoped logging fields
	ctxKeyRequestID contextKey = "request_id"
	ctxKeyNamespace contextKey = "namespace"
	ctxKeyThreadID  contextKey = "thread_id"
	ctxKeyRunID     contextKey = "run_id"
	ctxKeyEntityID  contextKey = "entity_id"
)

// Logger wraps zap.Logger with context-aware logging methods.
type Logger struct {
	*zap.Logger
}

// DefaultLogger is the global logger instance.
var DefaultLogger *Logger

// InitLogger initializes the default logger with the given configuration.
// If structured is true, logs are emitted in JSON format.
// Level should be one of: debug, info, warn, error.
func InitLogger(level string, structured bool) error {
	var cfg zap.Config

	if structured {
		cfg = zap.NewProductionConfig()
		cfg.EncoderConfig.TimeKey = "timestamp"
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	// Parse log level
	var zapLevel zapcore.Level
	switch level {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}
	cfg.Level = zap.NewAtomicLevelAt(zapLevel)

	logger, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return err
	}

	DefaultLogger = &Logger{Logger: logger}
	return nil
}

// InitTestLogger initializes a no-op logger for testing.
func InitTestLogger() {
	DefaultLogger = &Logger{Logger: zap.NewNop()}
}

// Sync flushes any buffered log entries.
func (l *Logger) Sync() error {
	return l.Logger.Sync()
}

// WithContext returns a logger with fields extracted from the context.
func (l *Logger) WithContext(ctx context.Context) *zap.Logger {
	fields := extractContextFields(ctx)
	if len(fields) == 0 {
		return l.Logger
	}
	return l.Logger.With(fields...)
}

// WithFields returns a logger with additional fields.
func (l *Logger) WithFields(fields ...zap.Field) *Logger {
	return &Logger{Logger: l.Logger.With(fields...)}
}

// extractContextFields extracts logging fields from context.
func extractContextFields(ctx context.Context) []zap.Field {
	var fields []zap.Field

	if requestID, ok := ctx.Value(ctxKeyRequestID).(string); ok && requestID != "" {
		fields = append(fields, zap.String("request_id", requestID))
	}
	if namespace, ok := ctx.Value(ctxKeyNamespace).(string); ok && namespace != "" {
		fields = append(fields, zap.String("namespace", namespace))
	}
	if threadID, ok := ctx.Value(ctxKeyThreadID).(string); ok && threadID != "" {
		fields = append(fields, zap.String("thread_id", threadID))
	}
	if runID, ok := ctx.Value(ctxKeyRunID).(string); ok && runID != "" {
		fields = append(fields, zap.String("run_id", runID))
	}
	if entityID, ok := ctx.Value(ctxKeyEntityID).(string); ok && entityID != "" {
		fields = append(fields, zap.String("entity_id", entityID))
	}

	return fields
}

// Context helpers for adding logging fields to context.

// WithRequestID adds a request ID to the context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, requestID)
}

// WithNamespace adds a namespace to the context.
func WithNamespace(ctx context.Context, namespace string) context.Context {
	return context.WithValue(ctx, ctxKeyNamespace, namespace)
}

// WithThreadID adds a thread ID to the context.
func WithThreadID(ctx context.Context, threadID string) context.Context {
	return context.WithValue(ctx, ctxKeyThreadID, threadID)
}

// WithRunID adds a run ID to the context.
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, ctxKeyRunID, runID)
}

// WithEntityID adds an entity ID to the context.
func WithEntityID(ctx context.Context, entityID string) context.Context {
	return context.WithValue(ctx, ctxKeyEntityID, entityID)
}

// Logging helper functions that use the default logger.

// Debug logs a debug message with context fields.
func Debug(ctx context.Context, msg string, fields ...zap.Field) {
	if DefaultLogger == nil {
		return
	}
	DefaultLogger.WithContext(ctx).Debug(msg, fields...)
}

// Info logs an info message with context fields.
func Info(ctx context.Context, msg string, fields ...zap.Field) {
	if DefaultLogger == nil {
		return
	}
	DefaultLogger.WithContext(ctx).Info(msg, fields...)
}

// Warn logs a warning message with context fields.
func Warn(ctx context.Context, msg string, fields ...zap.Field) {
	if DefaultLogger == nil {
		return
	}
	DefaultLogger.WithContext(ctx).Warn(msg, fields...)
}

// Error logs an error message with context fields.
func Error(ctx context.Context, msg string, fields ...zap.Field) {
	if DefaultLogger == nil {
		return
	}
	DefaultLogger.WithContext(ctx).Error(msg, fields...)
}

// LogOperation logs an operation with timing and context.
func LogOperation(ctx context.Context, primitive, action string, start time.Time, err error, fields ...zap.Field) {
	if DefaultLogger == nil {
		return
	}

	duration := time.Since(start)
	allFields := append([]zap.Field{
		zap.String("primitive", primitive),
		zap.String("action", action),
		zap.Duration("duration", duration),
		zap.Float64("duration_ms", float64(duration.Milliseconds())),
	}, fields...)

	if err != nil {
		allFields = append(allFields, zap.Error(err))
		DefaultLogger.WithContext(ctx).Error("operation failed", allFields...)
	} else {
		DefaultLogger.WithContext(ctx).Info("operation completed", allFields...)
	}
}

// Fatal logs a fatal message and exits.
func Fatal(msg string, fields ...zap.Field) {
	if DefaultLogger == nil {
		os.Exit(1)
	}
	DefaultLogger.Logger.Fatal(msg, fields...)
}

// Field creates a zap field. This is a convenience wrapper around zap.Any.
func Field(key string, value interface{}) zap.Field {
	return zap.Any(key, value)
}

// String creates a string zap field.
func String(key, value string) zap.Field {
	return zap.String(key, value)
}

// Int creates an int zap field.
func Int(key string, value int) zap.Field {
	return zap.Int(key, value)
}

// Duration creates a duration zap field.
func Duration(key string, value time.Duration) zap.Field {
	return zap.Duration(key, value)
}

// Err creates an error zap field.
func Err(err error) zap.Field {
	return zap.Error(err)
}
