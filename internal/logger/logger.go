package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

type Level string

const (
	LevelInfo  Level = "info"
	LevelError Level = "error"
	LevelWarn  Level = "warn"
	LevelDebug Level = "debug"
)

type Entry struct {
	Level  string                 `json:"level"`
	Event  string                 `json:"event"`
	Time   time.Time              `json:"time"`
	Fields map[string]interface{} `json:"fields,omitempty"`
}

type Logger struct {
	logger *log.Logger
	env    string
	hooks  []func(Entry)
}

func New(env string, w io.Writer) *Logger {
	if w == nil {
		w = os.Stdout
	}
	return &Logger{
		logger: log.New(w, "", 0),
		env:    env,
	}
}

func (l *Logger) AddHook(hook func(Entry)) {
	l.hooks = append(l.hooks, hook)
}

func (l *Logger) log(level Level, event string, fields map[string]interface{}) {
	entry := Entry{
		Level:  string(level),
		Event:  event,
		Time:   time.Now().UTC(),
		Fields: fields,
	}

	for _, hook := range l.hooks {
		hook(entry)
	}

	if l.env == "production" {
		b, err := json.Marshal(entry)
		if err == nil {
			l.logger.Println(string(b))
			return
		}
	}

	prefix := fmt.Sprintf("[%s] %s: ", entry.Time.Format(time.RFC3339), strings.ToUpper(entry.Level))
	l.logger.Printf("%s%s %v", prefix, entry.Event, entry.Fields)
}

func (l *Logger) Info(event string, fields map[string]interface{}) {
	l.log(LevelInfo, event, fields)
}

func (l *Logger) Error(event string, fields map[string]interface{}) {
	l.log(LevelError, event, fields)
}

func (l *Logger) Warn(event string, fields map[string]interface{}) {
	l.log(LevelWarn, event, fields)
}

func (l *Logger) Debug(event string, fields map[string]interface{}) {
	l.log(LevelDebug, event, fields)
}
