package util

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"os"
	"path/filepath"
)

func InitLogger() (*zap.Logger, error) {
	if dir, err := os.Getwd(); err != nil {
		return nil, err
	} else if fi, err := os.Stat(filepath.Join(dir, "logs")); os.IsNotExist(err) || !fi.IsDir() {
		_ = os.MkdirAll(filepath.Join(dir, "logs"), 0755)
	}

	if os.Getenv("LOG_MODE") == "console" {
		cfg := zap.NewDevelopmentConfig()
		cfg.Encoding = "console"
		cfg.OutputPaths = []string{"stdout"}
		cfg.ErrorOutputPaths = []string{"stderr"}
		switch os.Getenv("LOG_LEVEL") {
		case "debug":
			cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
			break
		case "info":
			cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
			break
		case "warning":
			cfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
			break
		case "error":
			cfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
			break
		case "dpanic":
			cfg.Level = zap.NewAtomicLevelAt(zap.DPanicLevel)
			break
		case "panic":
			cfg.Level = zap.NewAtomicLevelAt(zap.PanicLevel)
			break
		}

		if logger, err := cfg.Build(); err != nil {
			return nil, err
		} else {
			return logger, nil
		}
	} else {
		level := zap.InfoLevel
		w := zapcore.AddSync(&lumberjack.Logger{
			Filename:   "logs/log.log",
			MaxSize:    100, // megabytes
			MaxBackups: 3,
			MaxAge:     30, // days
		})
		encoder := zapcore.EncoderConfig{
			TimeKey:        "time",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}
		switch os.Getenv("LOG_LEVEL") {
		case "debug":
			level = zap.DebugLevel
			break
		case "warning":
			level = zap.WarnLevel
			break
		case "error":
			level = zap.ErrorLevel
			break
		case "dpanic":
			level = zap.DPanicLevel
			break
		case "panic":
			level = zap.PanicLevel
			break
		}
		core := zapcore.NewCore(zapcore.NewConsoleEncoder(encoder), w, level)
		return zap.New(core, zap.Development()), nil
	}
}
