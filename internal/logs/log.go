package logs

import "log/slog"

func Error(err error) slog.Attr {
	return slog.String("error", err.Error())
}

func Database(name string) slog.Attr {
	return slog.String("database", name)
}
