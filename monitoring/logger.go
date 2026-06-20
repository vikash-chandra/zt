package monitoring

import (
	"fmt"

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

	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zapLevel)
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	zapLogger, err := config.Build()
	if err != nil {
		return nil, err
	}

	return &Logger{zapLogger}, nil
}

// InfoMarket logs market-related info
func (l *Logger) InfoMarket(msg string, fields ...zap.Field) {
	l.Info("[MARKET] "+msg, fields...)
}

// InfoTrade logs trade-related info
func (l *Logger) InfoTrade(msg string, fields ...zap.Field) {
	l.Info("[TRADE] "+msg, fields...)
}

// ErrorTrade logs trade-related errors
func (l *Logger) ErrorTrade(msg string, fields ...zap.Field) {
	l.Error("[TRADE] "+msg, fields...)
}

// CriticalRisk logs critical risk events
func (l *Logger) CriticalRisk(msg string, fields ...zap.Field) {
	l.Error("[RISK] CRITICAL: "+msg, fields...)
}

// TraceOrder logs order state changes
func (l *Logger) TraceOrder(orderID string, action string, details map[string]interface{}) {
	fields := []zap.Field{zap.String("order_id", orderID)}
	for k, v := range details {
		fields = append(fields, zap.Any(k, v))
	}
	l.Info(fmt.Sprintf("[ORDER] %s", action), fields...)
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.Logger.Sync()
}
