package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBlue   = "\033[34m"
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
	levelIcon := "·"
	switch level {
	case LevelError:
		levelColor = colorRed
		levelIcon = "✕"
	case LevelWarn:
		levelColor = colorYellow
		levelIcon = "!"
	case LevelInfo:
		levelColor = colorGreen
		levelIcon = "✓"
	case LevelDebug:
		levelColor = colorBlue
		levelIcon = "·"
	}

	timestamp := entry.Time.Format("15:04:05")
	levelText := strings.ToUpper(entry.Level)
	prefix := fmt.Sprintf("%s[%s]%s %-5s%s", colorGray, timestamp, levelColor, levelText, colorReset)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s %s%s%s %s\n", prefix, levelColor, levelIcon, colorReset, humanizeEvent(event)))

	if len(entry.Fields) > 0 {
		keys := make([]string, 0, len(entry.Fields))
		for key := range entry.Fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			sb.WriteString(fmt.Sprintf("  %s│%s %s%s%s %v\n", colorGray, colorReset, colorYellow, key, colorReset, entry.Fields[key]))
		}
	}

	l.logger.Print(sb.String())
}

func humanizeEvent(event string) string {
	words := strings.Fields(strings.ReplaceAll(event, "_", " "))
	if len(words) == 0 {
		return "event"
	}
	for i := range words {
		words[i] = strings.ToLower(words[i])
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
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
