package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

type Logger struct {
	mu       sync.Mutex
	file     *os.File
	level    LogLevel
	maxSize  int64
	filePath string
}

// InitLogger initializes a new logger instance writing to path with the given level and max size in bytes.
// It returns the new Logger instance and sets it as the default logger.
func InitLogger(path string, level LogLevel, maxBytes int64) (*Logger, error) {
	l := &Logger{
		level:   level,
		maxSize: 1024 * 1024, // Default 1 MB
	}
	if maxBytes > 0 {
		l.maxSize = maxBytes
	}

	confDir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	l.filePath = filepath.Join(confDir, path)

	if err := os.MkdirAll(filepath.Dir(l.filePath), 0755); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	l.file = f

	return l, nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *Logger) rotateIfNeeded() error {
	if l.file == nil {
		return nil
	}

	fi, err := l.file.Stat()
	if err != nil {
		return err
	}

	// If under max size, nothing to do
	if fi.Size() < l.maxSize {
		return nil
	}

	// Close current log file
	_ = l.file.Close()

	// Always overwrite the single backup
	bak := l.filePath + ".bak"
	_ = os.Remove(bak) // remove old backup if it exists
	if err := os.Rename(l.filePath, bak); err != nil {
		return err
	}

	// Create a fresh log file
	f, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	l.file = f

	return nil
}

func logPrefix(level LogLevel) string {
	switch level {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func (l *Logger) Logf(level LogLevel, format string, args ...interface{}) {
	if level < l.level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.rotateIfNeeded()
	ts := time.Now().Format(time.RFC3339)
	line := fmt.Sprintf("%s [%s] %s\n", ts, logPrefix(level), fmt.Sprintf(format, args...))
	if l.file != nil {
		_, _ = l.file.WriteString(line)
	} else {
		fmt.Print(line)
	}
}

func (l *Logger) Debugf(format string, args ...interface{}) { l.Logf(LevelDebug, format, args...) }
func (l *Logger) Infof(format string, args ...interface{})  { l.Logf(LevelInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...interface{})  { l.Logf(LevelWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...interface{}) { l.Logf(LevelError, format, args...) }
