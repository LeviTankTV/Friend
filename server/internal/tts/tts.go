// Package tts озвучивает её реплики через Edge TTS (этап 7).
//
// Почему CLI, а не библиотека: Edge TTS — это бесплатный сервис Microsoft,
// единственный готовый клиент к нему — python-пакет `edge-tts`, и у него есть
// командная строка. Писать свой websocket-протокол Microsoft — значит руками
// воспроизводить протокол, который меняется без предупреждения; CLI пакета
// обновляется вместе с пакетом.
//
// Граница приватности (важно): в облако Microsoft уходит ТЕКСТ РЕПЛИКИ — то,
// что она произносит вслух. Ни экран, ни память, ни история не уходят:
// озвучиваются только готовые фразы. Функция выключается одной настройкой.
//
// Генерация идёт ПОСЛЕДОВАТЕЛЬНО (одна мьютекс-очередь): у бесплатного
// тира Edge TTS короткие сессии, и параллельные запросы ловят 403.
// Готовые фразы кэшируются по хэшу «голос+темп+текст»: одна и та же реплика
// (например, приветствие) озвучивается мгновенно.
package tts

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Service озвучивает текст в mp3. Нулевой Service (edge-tts не найден)
// честно говорит «недоступен» и не пытается генерировать.
type Service struct {
	mu    sync.Mutex
	edge  string // путь к CLI edge-tts; пусто = не установлен
	voice string
	speed string
	cache string
	log   *slog.Logger
}

// New собирает службу. enabled=false или отсутствие edge-tts в PATH —
// Service остаётся нерабочим, и это не ошибка запуска: озвучка опциональна.
// Пустой голос выбирается по языку персоны (этап 10): русский — Svetlana,
// английский — JennyNeural (тёплый разговорный женский; поменять можно
// конфигом voice.edge_voice — живое прослушивание за человеком).
func New(log *slog.Logger, enabled bool, voice, speed, cacheDir, lang string) *Service {
	if log == nil {
		log = slog.Default()
	}
	s := &Service{voice: strings.TrimSpace(voice), speed: strings.TrimSpace(speed), cache: cacheDir, log: log}
	if s.voice == "" {
		if strings.EqualFold(strings.TrimSpace(lang), "en") {
			s.voice = "en-US-JennyNeural"
		} else {
			s.voice = "ru-RU-SvetlanaNeural"
		}
	}
	if s.speed == "" {
		s.speed = "+0%"
	}
	if !enabled {
		return s
	}
	bin, err := exec.LookPath("edge-tts")
	if err != nil {
		log.Warn("озвучки не будет: edge-tts не найден в PATH (pip install edge-tts)")
		return s
	}
	s.edge = bin
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			log.Warn("кэш озвучки недоступен, озвучка будет без кэша", "dir", cacheDir, "err", err)
			s.cache = ""
		}
	}
	log.Info("озвучка: edge-tts", "voice", s.voice, "speed", s.speed)
	return s
}

// Available — можно ли озвучивать (edge-tts установлен и функция включена).
func (s *Service) Available() bool { return s != nil && s.edge != "" }

// Voice — имя голоса для человека (показывается в welcome).
func (s *Service) Voice() string {
	if !s.Available() {
		return ""
	}
	return s.voice
}

// cachePath — файл кэша по хэшу голоса, темпа, тона и текста.
func (s *Service) cachePath(text, rate, pitch string) string {
	if s.cache == "" {
		return ""
	}
	sum := sha1.Sum([]byte(s.voice + "|" + rate + "|" + pitch + "|" + text))
	return filepath.Join(s.cache, hex.EncodeToString(sum[:])+".mp3")
}

// MoodParams — интонация по настроению (этап 9б): радостная говорит чуть
// быстрее и выше, грустная — медленнее и ниже, сонная — заметно медленнее
// и совсем низко. Это deltas к базовому темпу; edge-tts принимает --rate
// (+/- %) и --pitch (+/- Hz). Ключ кэша включает итоговые значения — одна
// и та же фраза, сказанная по-разному, звучит по-разному и не «съедает»
// чужой кэш.
func MoodParams(band string, asleep bool) (rate, pitch int) {
	if asleep {
		return -25, -10
	}
	switch band {
	case "low":
		return -12, -6
	case "high":
		return 12, 8
	default:
		return 0, 0
	}
}

// parseRate — «+0%», «-15%» → целое число процентов (база из конфига).
func parseRate(v string) int {
	v = strings.TrimSpace(strings.TrimSuffix(v, "%"))
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return 0
	}
	return n
}

// Synth готовит mp3 и возвращает путь к файлу. rateDelta и pitchDelta —
// интонация запроса (проценты и герцы, ограничены ±50): радость/грусть/
// сон (этап 9б). 0,0 = базовый голос. Генерация сериализована и
// ограничена по времени; кэш проверяется и под блокировкой (другой запрос
// мог сгенерировать ту же фразу, пока мы ждали очередь).
func (s *Service) Synth(ctx context.Context, text string, rateDelta, pitchDelta int) (string, error) {
	if !s.Available() {
		return "", fmt.Errorf("озвучка недоступна")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("пустой текст")
	}
	if len([]rune(text)) > 400 {
		text = string([]rune(text)[:400])
	}
	rate := parseRate(s.speed) + rateDelta
	if rate < -50 {
		rate = -50
	}
	if rate > 50 {
		rate = 50
	}
	pitch := pitchDelta
	if pitch < -50 {
		pitch = -50
	}
	if pitch > 50 {
		pitch = 50
	}
	rateStr := fmt.Sprintf("%+d%%", rate)
	pitchStr := fmt.Sprintf("%+dHz", pitch)
	if path := s.cachePath(text, rateStr, pitchStr); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if path := s.cachePath(text, rateStr, pitchStr); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	out := s.cachePath(text, rateStr, pitchStr)
	tmp := out
	if tmp == "" {
		f, err := os.CreateTemp("", "friend-tts-*.mp3")
		if err != nil {
			return "", err
		}
		tmp = f.Name()
		f.Close()
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Внимательно с именами флагов: в edge-tts 7 темп называется --rate
	// (--speed не принят, CLI падает с exit 2 на распарсенном argv), тон —
	// --pitch в герцах.
	cmd := exec.CommandContext(ctx, s.edge,
		"--voice", s.voice, "--rate", rateStr, "--pitch", pitchStr,
		"--text", text, "--write-media", tmp)
	// В приватный временный каталог служебный файл edge-tts не пишем:
	// бинарник на Windows создаёт каталог рядом с cwd. Работаем из кэша.
	if s.cache != "" {
		cmd.Dir = s.cache
	}
	if outB, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("edge-tts: %w: %s", err, strings.TrimSpace(string(outB)))
	}
	if fi, err := os.Stat(tmp); err != nil || fi.Size() < 200 {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("edge-tts вернул пустой файл")
	}
	if out == "" {
		return tmp, nil
	}
	if err := os.Rename(tmp, out); err != nil {
		return tmp, nil
	}
	return out, nil
}
