// Package config читает настройки компаньона из JSON-файла и переменных
// окружения.
//
// Правила, которые здесь закодированы:
//   - конфиг не обязателен: нет файла — работаем на значениях по умолчанию;
//   - секретов в файле нет. Для ключа API хранится ИМЯ переменной окружения
//     (llm.api_key_env), а само значение читается в момент запроса (этап 2) —
//     так ключ не попадает ни в git, ни в логи, ни в дамп конфига;
//   - переменные окружения ПЕРЕКРЫВАЮТ файл (удобно для быстрой проверки без
//     правки конфига);
//   - пути не хардкодятся: файл состояния по умолчанию лежит в каталоге
//     пользователя (os.UserConfigDir).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config — весь конфиг компаньона.
type Config struct {
	Server    Server    `json:"server"`
	Avatar    Avatar    `json:"avatar"`
	Persona   Persona   `json:"persona"`
	LLM       LLM       `json:"llm"`
	Proactive Proactive `json:"proactive"`
	Voice     Voice     `json:"voice"`
	STT       STT       `json:"stt"`
	Wake      Wake      `json:"wake"`
	Log       Log       `json:"log"`
	Paths     Paths     `json:"paths"`
}

// Persona — характер компаньона.
//
// Файл необязателен: без него берётся встроенный характер (см. пакет persona).
// Пустой путь — это норма, а не «настройка забыта»: характер есть всегда.
type Persona struct {
	File string `json:"file"`
}

// Server — локальный HTTP/WebSocket-сервер.
type Server struct {
	Addr string `json:"addr"`
}

// Avatar — что показывать в окне.
type Avatar struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Sprite — путь к PNG-спрайту. Пусто — рисуем персонажа процедурно
	// (готового арта нет, а пустое окно проверять бессмысленно).
	Sprite string `json:"sprite"`
	// Language — язык персонажа (в промпт-личность, этап 2).
	Language string `json:"language"`
}

// LLM — параметры модели (OpenAI-совместимый API: NVIDIA NIM по умолчанию,
// локальная Ollama или другой провайдер — по base_url).
type LLM struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	// APIKeyEnv — ИМЯ переменной окружения с ключом. Пустая строка — у этого
	// адреса (локальная модель) ключа нет, и требовать его нельзя: иначе
	// человек получает «задай переменную окружения» при рабочем конфиге.
	APIKeyEnv string `json:"api_key_env"`
	// HistoryMessages — сколько последних реплик разговора уходит в запрос.
	// Ноль или меньше — значение по умолчанию (chat.DefaultMax): пустая строка
	// в конфиге не должна означать «модель без контекста».
	HistoryMessages int     `json:"history_messages"`
	MaxTokens       int     `json:"max_tokens"`
	Temperature     float64 `json:"temperature"`
	TimeoutSec      int     `json:"timeout_sec"`
}

// Proactive — планировщик (этап 4). Enabled=false — она никогда не
// заговаривает первой (планировщик молчит); внутреннее состояние, реакция на
// реплики и ответы на вопрос продолжают работать.
type Proactive struct {
	Enabled       bool `json:"enabled"`
	TickSeconds   int  `json:"tick_seconds"`
	MinGapMinutes int  `json:"min_gap_minutes"`
	QuietFromHour int  `json:"quiet_from_hour"`
	QuietToHour   int  `json:"quiet_to_hour"`
	FoodReminderMin int  `json:"food_reminder_minutes"`
}

// Voice — озвучка реплик через Edge TTS (этап 7). Граница приватности:
// в облако Microsoft уходит только ТЕКСТ реплики, который она произносит
// вслух; экран, память и история туда не попадают. enabled=false — функция
// полностью выключена.
type Voice struct {
	Enabled   bool   `json:"enabled"`
	EdgeVoice string `json:"edge_voice"` // голос edge-tts, ru-RU-SvetlanaNeural по умолчанию
	Speed     string `json:"speed"`      // темп вида "+10%", по умолчанию "+0%"
}

// STT — голосовой ввод через faster-whisper (этап 7). Полностью локально:
// движок и модель на машине, микрофонный текст никуда не отправляется.
// ModelDir — папка модели systran/faster-whisper-base.
type STT struct {
	Enabled  bool   `json:"enabled"`
	ModelDir string `json:"model_dir"`
}

// Wake — wake word (этап 7): фоновое слушание её имени. По умолчанию
// ВЫКЛЮЧЕНО: постоянно слушать микрофон человек должен только по своему
// решению. Работает на том же локальном распознавателе, что и STT (сегменты
// речи расшифровываются на машине и никуда не отправляются), и требует
// включённого stt — без чем слушать wake-слушателю нечего.
type Wake struct {
	Enabled bool `json:"enabled"`
}

// Log — уровень логирования.
type Log struct {
	Level string `json:"level"` // debug | info | warn | error
}

// Paths — файлы состояния.
type Paths struct {
	StateFile  string `json:"state_file"`
	MemoryFile string `json:"memory_file"`
	// SpritesDir — папка внешнего арта персонажа (этап 6): лист PNG-кадров с
	// лист-файлом sheet.json. Сервер раздаёт её по /sprites/. Пустой —
	// каталог `sprites` рядом с остальными файлами.
	SpritesDir string `json:"sprites_dir"`
}

// Default возвращает конфиг по умолчанию: он же документация к каждому полю.
func Default() Config {
	return Config{
		Server: Server{Addr: "127.0.0.1:8787"},
		Avatar: Avatar{
			Name:     "Мира",
			Width:    320,
			Height:   420,
			Language: "ru",
		},
		LLM: LLM{
			BaseURL:   "https://integrate.api.nvidia.com/v1",
			Model:     "openai/gpt-oss-20b",
			APIKeyEnv: "NVIDIA_API_KEY",
			// Лимит с запасом: модели на NIM перед ответом «думают», и на
			// маленьком лимите вызов заканчивается по длине с пустым ответом
			// (замерено: при 300 токенах так происходило через раз).
			HistoryMessages: 20,
			MaxTokens:       1024,
			Temperature:     0.8,
			TimeoutSec:      45,
		},
		Proactive: Proactive{
			Enabled:         true,
			TickSeconds:     30,
			MinGapMinutes:   25,
			QuietFromHour:   23,
			QuietToHour:     7,
			FoodReminderMin: 180,
		},
		Voice: Voice{
			Enabled:   true,
			EdgeVoice: "ru-RU-SvetlanaNeural",
			Speed:     "+0%",
		},
		STT: STT{Enabled: true},
		// Wake word по умолчанию выключен: фоновый микрофон — только по
		// явному решению человека (секция wake + переключатель в меню).
		Wake: Wake{Enabled: false},
		Log:  Log{Level: "info"},
	}
}

// Load читает конфиг из path и накладывает поверх переменные окружения.
// Отсутствие файла — не ошибка: возвращаются значения по умолчанию, и об этом
// сообщает второй результат.
func Load(path string) (Config, string, error) {
	cfg := Default()
	note := ""
	if path == "" {
		path = defaultConfigPath()
	}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, "", fmt.Errorf("конфиг %s: %w", path, err)
		}
		note = "конфиг загружен: " + path
	case os.IsNotExist(err):
		note = "конфига нет (" + path + ") — работаю на значениях по умолчанию"
	default:
		return cfg, "", fmt.Errorf("конфиг %s: %w", path, err)
	}

	applyEnv(&cfg)
	// Пустой путь к состоянию — кладём в каталог пользователя, а не рядом с
	// бинарником: иначе после каждой пересборки состояние «переезжает».
	if strings.TrimSpace(cfg.Paths.StateFile) == "" {
		cfg.Paths.StateFile = filepath.Join(defaultStateDir(), "state.json")
	}
	// Память (SQLite) — там же: она переживает пересборки и перезапуски.
	if strings.TrimSpace(cfg.Paths.MemoryFile) == "" {
		cfg.Paths.MemoryFile = filepath.Join(defaultStateDir(), "memory.db")
	}
	// Внешний арт — там же: человек подкладывает свои кадры в понятное место,
	// и они переживают пересборку бинарника.
	if strings.TrimSpace(cfg.Paths.SpritesDir) == "" {
		cfg.Paths.SpritesDir = filepath.Join(defaultStateDir(), "sprites")
	}
	// Модель распознавания речи — в каталоге данных: 75 МБ рядом с бинарником
	// жить не должны.
	if strings.TrimSpace(cfg.STT.ModelDir) == "" {
		cfg.STT.ModelDir = filepath.Join(defaultStateDir(), "stt-model", "faster-whisper-base")
	}
	if err := cfg.Validate(); err != nil {
		return cfg, note, err
	}
	return cfg, note, nil
}

// applyEnv — переменные окружения поверх файла. Только то, что реально нужно
// для запуска на другой машине/порту.
func applyEnv(c *Config) {
	setStr(&c.Server.Addr, "FRIEND_ADDR")
	setStr(&c.Paths.StateFile, "FRIEND_STATE_FILE")
	setStr(&c.Paths.MemoryFile, "FRIEND_MEMORY_FILE")
	setStr(&c.Paths.SpritesDir, "FRIEND_SPRITES_DIR")
	setStr(&c.STT.ModelDir, "FRIEND_STT_MODEL")
	setStr(&c.Log.Level, "FRIEND_LOG_LEVEL")
	setStr(&c.LLM.APIKeyEnv, "FRIEND_API_KEY_ENV")
	setStr(&c.Avatar.Name, "FRIEND_NAME")
	setStr(&c.Persona.File, "FRIEND_PERSONA_FILE")
}

func setStr(dst *string, env string) {
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		*dst = v
	}
}

// Validate ловит то, что ломает запуск, а не «некрасивый» конфиг.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Server.Addr) == "" {
		return fmt.Errorf("server.addr пуст: сервер некуда слушать")
	}
	if c.Avatar.Width <= 0 || c.Avatar.Height <= 0 {
		return fmt.Errorf("avatar.width/height должны быть > 0 (сейчас %dx%d)", c.Avatar.Width, c.Avatar.Height)
	}
	if err := ValidLogLevel(c.Log.Level); err != nil {
		return err
	}
	if c.Proactive.TickSeconds <= 0 {
		return fmt.Errorf("proactive.tick_seconds должен быть > 0")
	}
	if c.LLM.BaseURL == "" && c.LLM.Model == "" {
		return fmt.Errorf("llm.base_url и llm.model пусты")
	}
	return nil
}

// ValidLogLevel проверяет уровень логирования, не затягивая в конфиг пакет
// логирования: список из четырёх имён — это договорённость, а не логика.
func ValidLogLevel(s string) error {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "debug", "info", "warn", "warning", "error":
		return nil
	default:
		return fmt.Errorf("log.level=%q: ожидается debug|info|warn|error", s)
	}
}

// ConfigPathFromArgs — путь к конфигу из аргументов командной строки
// (-config=... или --config ...). Пусто — берётся путь по умолчанию.
func ConfigPathFromArgs(args []string) string {
	for i, a := range args {
		switch {
		case a == "-config" || a == "--config":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-config="):
			return strings.TrimPrefix(a, "-config=")
		case strings.HasPrefix(a, "--config="):
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return ""
}

func defaultConfigPath() string {
	return filepath.Join(defaultStateDir(), "config.json")
}

// DataDir — каталог данных компаньона (конфиг и состояние по умолчанию).
// Нужен снаружи: рядом с ним ищется файл ключей .env (см. пакет dotenv).
func DataDir() string { return defaultStateDir() }

// defaultStateDir — каталог данных компаньона у пользователя
// (%AppData%\Friend на Windows, ~/.config/Friend на Linux).
func defaultStateDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "."
	}
	return filepath.Join(dir, "Friend")
}
