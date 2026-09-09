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

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
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

	levelColor := colorCyan
	switch level {
	case LevelError:
		levelColor = colorRed
	case LevelWarn:
		levelColor = colorYellow
	case LevelInfo:
		levelColor = colorGreen
	}

	timestamp := entry.Time.Format("15:04:05")
	prefix := fmt.Sprintf("%s[%s] %s%s", colorGray, timestamp, levelColor, strings.ToUpper(entry.Level))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s%s %s%s\n", prefix, colorReset, colorCyan, event))

	if len(entry.Fields) > 0 {
		sb.WriteString(fmt.Sprintf("  %s│%s ", colorGray, colorReset))
		first := true
		for _, v := range entry.Fields {
			if !first {
				sb.WriteString(fmt.Sprintf("  %s·%s ", colorGray, colorReset))
			}
			sb.WriteString(fmt.Sprintf("%s=%s%v", colorYellow, colorReset, v))
			first = false
		}
		sb.WriteString("\n")
	}

	l.logger.Print(sb.String())
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
