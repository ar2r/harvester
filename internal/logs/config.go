package logs

import (
	"log/slog"
	"os"
	"time"
)

type Encoding string

const (
	Text Encoding = "text"
	JSON Encoding = "json"
)

func (Encoding) SchemaEnum() any {
	return []Encoding{
		Text,
		JSON,
	}
}

type Config struct {
	Level     slog.Level `yaml:"level,omitempty" default:"INFO" doc:"Уровень логирования." enum:"DEBUG,INFO,WARN,ERROR"`
	Encoding  Encoding   `yaml:"encoding,omitempty" default:"text" doc:"Формат логирования."`
	AddSource bool       `yaml:"addSource,omitempty" doc:"Добавлять ли номера строк в логи."`
}

func Get(cfg Config) *slog.Logger {
	opts := &slog.HandlerOptions{
		AddSource: cfg.AddSource,
		Level:     cfg.Level,
	}

	var handler slog.Handler
	switch cfg.Encoding {
	case JSON:
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		// Текстовый режим — для человека: короткое время вместо полного
		// таймстемпа с датой и часовым поясом.
		opts.ReplaceAttr = shortenTime
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	return slog.New(handler)
}

func shortenTime(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey && len(groups) == 0 {
		if value, ok := a.Value.Any().(time.Time); ok {
			a.Value = slog.StringValue(value.Format("15:04:05"))
		}
	}

	return a
}
