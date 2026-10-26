// Package stt распознаёт голос (этап 7: голосовой ввод) через faster-whisper.
//
// Принцип, почему локально: микрофонный текст — самая чувствительная из
// вещей, что есть у компаньона (в речь человек говорит то, что не написал бы
// в чат). Поэтому распознавание целиком офлайн: движок faster-whisper и
// модель лежат на машине, в сеть из этой цепочки не уходит НИЧЕГО (в отличие
// от TTS, где в облако Microsoft уходит только текст её собственной реплики).
//
// Реализация — python-скрипт с embedded-кодом: движок живёт в Python, Go
// запускает его как CLI (тот же приём, что edge-tts в internal/tts).
package stt

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed assets/whisper_recognize.py
var assets embed.FS

// Service распознаёт WAV (16 кГц, mono) в текст. Нулевой Service (python,
// faster-whisper или модель не найдены) честно возвращает «недоступен» —
// микрофона в интерфейсе не появляется, и это не ошибка запуска.
type Service struct {
	mu     sync.Mutex
	python string // путь к python ("" = недоступен)
	script string // файл скрипта в кэше
	model  string
	lang   string // язык распознавания (ru/en, этап 10)
	log    *slog.Logger
}

// New собирает службу. Проверяет дёшево: python в PATH, модель на диске.
// Сам faster-whisper пробуется при первом распознавании: отсутствие пакета
// видно как понятная ошибка запроса. lang — язык речи человека (язык
// персоны из конфига); пустой — русский.
func New(log *slog.Logger, enabled bool, modelDir, scriptDir, lang string) *Service {
	if log == nil {
		log = slog.Default()
	}
	s := &Service{model: strings.TrimSpace(modelDir), lang: strings.TrimSpace(lang), log: log}
	if s.lang != "en" {
		s.lang = "ru"
	}
	if !enabled {
		return s
	}
	bin, err := exec.LookPath("python")
	if err != nil {
		log.Info("голосового ввода не будет: python не найден в PATH")
		return s
	}
	// faster-whisper опознаётся по model.bin в папке модели.
	if _, err := os.Stat(filepath.Join(s.model, "model.bin")); err != nil {
		log.Warn("голосового ввода не будет: модель faster-whisper не найдена",
			"dir", s.model,
			"подсказка", "pip install faster-whisper и скачать systran/faster-whisper-base")
		return s
	}
	script := filepath.Join(scriptDir, "whisper_recognize.py")
	if data, err := assets.ReadFile("assets/whisper_recognize.py"); err == nil {
		if err := os.MkdirAll(scriptDir, 0o755); err == nil {
			// Перезаписываем каждый запуск: скрипт — часть бинарника,
			// на диске не должно лежать устаревшей версии.
			if os.WriteFile(script, data, 0o644) == nil {
				s.script = script
			}
		}
	}
	if s.script == "" {
		log.Warn("голосового ввода не будет: скрипт распознавания не распаковался", "dir", scriptDir)
		return s
	}
	s.python = bin
	log.Info("голосовой ввод: faster-whisper", "model", s.model)
	return s
}

// Available — можно ли распознавать.
func (s *Service) Available() bool { return s != nil && s.python != "" }

// recognized — строка stdout скрипта.
type recognized struct {
	Text  string `json:"text"`
	Error string `json:"error"`
}

// SetLanguage — язык распознавания за языком персоны (этап 13, живая смена
// языка): человек переключил ru/en в меню — шепталка должна слышать по-новому.
func (s *Service) SetLanguage(lang string) {
	if lang != "en" {
		lang = "ru"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lang = lang
}

// Recognize расшифровывает WAV по пути. Последовательно (модель грузится на
// каждый запрос и требует эксклюзивного доступа к памяти), с таймаутом:
// запись короче минуты, расшифровка — секунды.
func (s *Service) Recognize(ctx context.Context, wavPath string) (string, error) {
	if !s.Available() {
		return "", fmt.Errorf("голосовой ввод недоступен")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.python, s.script, s.model, wavPath, s.lang).Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("распознавание не успело за 60 с")
		}
		return "", fmt.Errorf("whisper: %w", err)
	}
	var res recognized
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("whisper: не разобрался ответ (%s)", strings.TrimSpace(string(out)))
	}
	if res.Error != "" {
		return "", fmt.Errorf("whisper: %s", res.Error)
	}
	return strings.TrimSpace(res.Text), nil
}
