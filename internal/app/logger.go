package app

import (
	"fmt"
	"os"

	"github.com/LumivoxAI/webrelay/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// NewLogger creates the configured production logger after startup validation.
func NewLogger(logging config.LoggingConfig) (*zap.Logger, error) {
	level := zap.NewAtomicLevel()
	if err := level.UnmarshalText([]byte(logging.Level)); err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	var encoder zapcore.Encoder
	if logging.Format == "console" {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	outputs := make([]zapcore.WriteSyncer, 0, 2)
	if logging.File != "" {
		outputs = append(outputs, zapcore.AddSync(&lumberjack.Logger{
			Filename:   logging.File,
			MaxSize:    logging.Rotation.MaxSizeMB,
			MaxBackups: logging.Rotation.MaxBackups,
			MaxAge:     logging.Rotation.MaxAgeDays,
			Compress:   logging.Rotation.Compress,
		}))
	}
	if logging.Console {
		outputs = append(outputs, zapcore.AddSync(os.Stderr))
	}

	core := zapcore.NewCore(encoder, zapcore.NewMultiWriteSyncer(outputs...), level)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), nil
}
