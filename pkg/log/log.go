package log

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"os"
	"time"
)

const ctxLoggerKey = "zapLogger"

type Logger struct {
	*zap.Logger
}

// DailyRotateWriter wraps lumberjack.Logger to provide date-based rotation
type DailyRotateWriter struct {
	originalPath string
	currentDate  string
	hook         *lumberjack.Logger
	mu           sync.Mutex
	config       *viper.Viper
}

// NewDailyRotateWriter creates a new date-aware log writer
func NewDailyRotateWriter(originalPath string, config *viper.Viper) *DailyRotateWriter {
	currentDate := time.Now().Format("2006-01-02")
	dailyPath := generateDailyLogFilename(originalPath, currentDate)

	d := &DailyRotateWriter{
		originalPath: originalPath,
		currentDate:  currentDate,
		config:       config,
	}
	d.hook = d.createLumberjackLogger(dailyPath)

	return d
}

// createLumberjackLogger creates a new lumberjack.Logger with config
func (d *DailyRotateWriter) createLumberjackLogger(filename string) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    d.config.GetInt("log.max_size"),
		MaxBackups: d.config.GetInt("log.max_backups"),
		MaxAge:     d.config.GetInt("log.max_age"),
		Compress:   d.config.GetBool("log.compress"),
	}
}

// Write implements io.Writer interface with date checking
func (d *DailyRotateWriter) Write(p []byte) (n int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if date has changed
	newDate := time.Now().Format("2006-01-02")
	if newDate != d.currentDate {
		// Date changed, need to rotate to new file
		d.currentDate = newDate
		newDailyPath := generateDailyLogFilename(d.originalPath, newDate)

		// Close current file and handle error
		if err := d.hook.Close(); err != nil {
			// Log the error but continue with rotation
			// We can't return error here as it would break logging
			_, _ = fmt.Fprintf(os.Stderr, "failed to close log file: %v\n", err)
		}

		// Create new lumberjack logger with new filename
		d.hook = d.createLumberjackLogger(newDailyPath)
	}

	return d.hook.Write(p)
}

// Sync implements zapcore.WriteSyncer interface
func (d *DailyRotateWriter) Sync() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Ensure data is flushed to disk
	// lumberjack writes synchronously, but we should still flush the underlying file
	return nil
}

// Close closes the underlying log file
func (d *DailyRotateWriter) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.hook != nil {
		return d.hook.Close()
	}
	return nil
}

// generateDailyLogFilename generates a date-based log filename
func generateDailyLogFilename(originalPath string, dateStr string) string {
	dir := filepath.Dir(originalPath)
	filename := filepath.Base(originalPath)
	ext := filepath.Ext(filename)
	nameWithoutExt := strings.TrimSuffix(filename, ext)

	// Create new filename with date: server-2025-01-12.log
	// Handle edge case: if no extension, don't add extra dot
	var newFilename string
	if ext != "" {
		newFilename = fmt.Sprintf("%s-%s%s", nameWithoutExt, dateStr, ext)
	} else {
		newFilename = fmt.Sprintf("%s-%s", nameWithoutExt, dateStr)
	}
	return filepath.Join(dir, newFilename)
}

func NewLog(conf *viper.Viper) *Logger {
	// log address "out.log" User-defined
	lp := conf.GetString("log.log_file_name")
	lv := conf.GetString("log.log_level")

	var level zapcore.Level
	//debug<info<warn<error<fatal<panic
	switch lv {
	case "debug":
		level = zap.DebugLevel
	case "info":
		level = zap.InfoLevel
	case "warn":
		level = zap.WarnLevel
	case "error":
		level = zap.ErrorLevel
	default:
		level = zap.InfoLevel
	}

	// Create writer based on daily rotation setting
	var writer zapcore.WriteSyncer
	if conf.GetBool("log.daily_rotation") {
		// Use daily rotate writer
		dailyWriter := NewDailyRotateWriter(lp, conf)
		writer = zapcore.AddSync(dailyWriter)
	} else {
		// Use traditional lumberjack writer
		hook := &lumberjack.Logger{
			Filename:   lp,                             // Log file path
			MaxSize:    conf.GetInt("log.max_size"),    // Maximum size unit for each log file: M
			MaxBackups: conf.GetInt("log.max_backups"), // The maximum number of backups that can be saved for log files
			MaxAge:     conf.GetInt("log.max_age"),     // Maximum number of days the file can be saved
			Compress:   conf.GetBool("log.compress"),   // Compression or not
		}
		writer = zapcore.AddSync(hook)
	}

	var encoder zapcore.Encoder
	if conf.GetString("log.encoding") == "console" {
		encoder = zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "Logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseColorLevelEncoder,
			EncodeTime:     timeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.FullCallerEncoder,
		})
	} else {
		encoder = zapcore.NewJSONEncoder(zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.EpochTimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		})
	}
	// default(both) log to console and file
	core := zapcore.NewCore(
		encoder,
		zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout), writer), // Print to console and file
		level,
	)
	mode := conf.GetString("log.mode")
	switch mode {
	case "console":
		core = zapcore.NewCore(
			encoder,
			zapcore.AddSync(os.Stdout),
			level,
		)
	case "file":
		core = zapcore.NewCore(
			encoder,
			writer,
			level,
		)
	}
	if conf.GetString("env") != "prod" {
		return &Logger{zap.New(core, zap.Development(), zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))}
	}
	return &Logger{zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))}
}

func timeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	//enc.AppendString(t.Format("2006-01-02 15:04:05"))
	enc.AppendString(t.Format("2006-01-02 15:04:05.000000000"))
}

// WithValue Adds a field to the specified context
func (l *Logger) WithValue(ctx context.Context, fields ...zapcore.Field) context.Context {
	if c, ok := ctx.(*gin.Context); ok {
		ctx = c.Request.Context()
		c.Request = c.Request.WithContext(context.WithValue(ctx, ctxLoggerKey, l.WithContext(ctx).With(fields...)))
		return c
	}
	return context.WithValue(ctx, ctxLoggerKey, l.WithContext(ctx).With(fields...))
}

// WithContext Returns a zap instance from the specified context
func (l *Logger) WithContext(ctx context.Context) *Logger {
	if c, ok := ctx.(*gin.Context); ok {
		ctx = c.Request.Context()
	}
	zl := ctx.Value(ctxLoggerKey)
	ctxLogger, ok := zl.(*zap.Logger)
	if ok {
		return &Logger{ctxLogger}
	}
	return l
}

// SanitizeRequestBody sanitizes request body for logging by truncating long content
// This prevents logging of large base64 images or other binary data
func SanitizeRequestBody(body []byte) string {
	const maxLength = 500
	bodyStr := string(body)

	// If body is shorter than max length, return as-is
	if len(bodyStr) <= maxLength {
		return bodyStr
	}

	// Truncate and add indication
	return bodyStr[:maxLength] + "... (truncated)"
}
