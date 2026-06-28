package monitoring

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger wraps zap logger with structured logging
type Logger struct {
	*zap.Logger
}

// NewLogger creates a new structured logger
func NewLogger(level string) (*Logger, error) {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zap.InfoLevel
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(zapLevel)
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	zapLogger, err := cfg.Build()
	if err != nil {
		return nil, err
	}

	return &Logger{zapLogger}, nil
}

// Info overrides the embedded Info method to accept a map of fields instead of variadic zap.Fields
func (l *Logger) Info(msg string, fields map[string]interface{}) {
	l.Logger.Info(msg, convertFields(fields)...)
}

// Warn overrides the embedded Warn method to accept a map of fields instead of variadic zap.Fields
func (l *Logger) Warn(msg string, fields map[string]interface{}) {
	l.Logger.Warn(msg, convertFields(fields)...)
}

// Error overrides the embedded Error method to accept a map of fields instead of variadic zap.Fields
func (l *Logger) Error(msg string, fields map[string]interface{}) {
	l.Logger.Error(msg, convertFields(fields)...)
}

// InfoMarket logs market-related info
func (l *Logger) InfoMarket(msg string, fields map[string]interface{}) {
	zapFields := convertFields(fields)
	l.Logger.Info("[MARKET] "+msg, zapFields...)
}

// InfoTrade logs trade-related info
func (l *Logger) InfoTrade(msg string, fields map[string]interface{}) {
	zapFields := convertFields(fields)
	l.Logger.Info("[TRADE] "+msg, zapFields...)
}

// ErrorTrade logs trade-related errors
func (l *Logger) ErrorTrade(msg string, err error, fields map[string]interface{}) {
	zapFields := append(convertFields(fields), zap.Error(err))
	l.Logger.Error("[TRADE] "+msg, zapFields...)
}

// CriticalRisk logs critical risk events
func (l *Logger) CriticalRisk(msg string, fields map[string]interface{}) {
	zapFields := convertFields(fields)
	l.Logger.Error("[RISK] CRITICAL: "+msg, zapFields...)
}

// TraceOrder logs order state changes
func (l *Logger) TraceOrder(orderID string, action string, details map[string]interface{}) {
	zapFields := append(convertFields(details), zap.String("order_id", orderID))
	l.Logger.Info("[ORDER] "+action, zapFields...)
}

// convertFields helper to convert map to zap fields
func convertFields(fields map[string]interface{}) []zap.Field {
	if fields == nil {
		return nil
	}
	zapFields := make([]zap.Field, 0, len(fields))
	for k, v := range fields {
		zapFields = append(zapFields, zap.Any(k, v))
	}
	return zapFields
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.Logger.Sync()
}