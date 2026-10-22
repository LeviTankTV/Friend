// Package logging — уровни логов поверх стандартного log/slog.
//
// Отдельный пакет нужен, чтобы «логирование с уровнями» было ОДНО на весь
// проект: сервер, планировщик и LLM-клиент берут логгер отсюда, а не заводят
// каждый свой формат.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Lевел-имена совпадают с конфигом (log.level).
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// ParseLevel переводит строку из конфига в slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", LevelInfo:
		return slog.LevelInfo, nil
	case LevelDebug:
		return slog.LevelDebug, nil
	case LevelWarn, "warning":
		return slog.LevelWarn, nil
	case LevelError:
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("неизвестный уровень логирования %q (ожидается debug|info|warn|error)", s)
	}
}

// New собирает логгер: текст с уровнями, время в формате ISO.
func New(w io.Writer, level string) (*slog.Logger, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})
	return slog.New(h), nil
}
