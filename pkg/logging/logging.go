// Package logging provides structured logging for the Exio tunneling system.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Level represents a log severity level.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Logger is a structured logger that supports text and JSON output.
type Logger struct {
	output io.Writer
	json   bool
	prefix string
	mu     sync.Mutex
}

// New creates a new Logger. If jsonMode is true, output is JSON; otherwise it
// matches the existing "[prefix] timestamp message" format.
func New(output io.Writer, prefix string, jsonMode bool) *Logger {
	if output == nil {
		output = os.Stdout
	}
	return &Logger{
		output: output,
		json:   jsonMode,
		prefix: prefix,
	}
}

// Info logs an informational message with optional key-value pairs.
func (l *Logger) Info(msg string, kvs ...interface{}) {
	l.log(LevelInfo, msg, kvs...)
}

// Warn logs a warning message with optional key-value pairs.
func (l *Logger) Warn(msg string, kvs ...interface{}) {
	l.log(LevelWarn, msg, kvs...)
}

// Error logs an error message with optional key-value pairs.
func (l *Logger) Error(msg string, kvs ...interface{}) {
	l.log(LevelError, msg, kvs...)
}

func (l *Logger) log(level Level, msg string, kvs ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	if l.json {
		entry := map[string]interface{}{
			"time":  now.UTC().Format(time.RFC3339),
			"level": string(level),
			"msg":   msg,
		}
		for i := 0; i+1 < len(kvs); i += 2 {
			key, ok := kvs[i].(string)
			if !ok {
				key = fmt.Sprintf("%v", kvs[i])
			}
			entry[key] = kvs[i+1]
		}
		data, _ := json.Marshal(entry)
		fmt.Fprintf(l.output, "%s\n", data)
	} else {
		// Match existing format: [prefix] 2006/01/02 15:04:05 message key=value ...
		ts := now.Format("2006/01/02 15:04:05")
		extra := ""
		for i := 0; i+1 < len(kvs); i += 2 {
			extra += fmt.Sprintf(" %v=%v", kvs[i], kvs[i+1])
		}
		fmt.Fprintf(l.output, "%s %s%s\n", l.prefix+ts, msg, extra)
	}
}
