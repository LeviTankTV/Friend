package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Options — то, что нужно серверу для запуска.
type Options struct {
	Addr   string
	WebDir string // каталог с index.html персонажа
	// SpritesDir — папка внешнего арта (этап 6): лист кадров персонажа,
	// который раздаётся по /sprites/. Не совпадает с WebDir: арт — файлы
	// человека, они живут в его каталоге данных и переживают пересборку.
	SpritesDir string
	// TTS — озвучка реплик (этап 7): по /tts?text=... отдаёт mp3 голоса.
	// Nil — озвучки нет, и фронтенд её не просит.
	TTS TTSService
	// STT — голосовой ввод (этап 7): по /stt принимает wav и возвращает текст.
	// Nil — ввода нет, и микрофонная кнопка не показывается.
	STT STTService
	// STTDir — каталог для временных wav-файлов recognition. Пусто — системный
	// временный каталог.
	STTDir string
	// Wake — wake word (этап 7): слушает ли сервер её имя по /wake. Роут
	// регистрируется только если функция включена И есть чем слушать (STT).
	Wake bool
	Log  *slog.Logger
	Hub  *Hub
}

// TTSService — озвучивающая сторона (реализация — internal/tts.Service).
// rateDelta и pitchDelta — интонация настроения (этап 9б, проценты и герцы,
// 0 = базовый голос).
type TTSService interface {
	Available() bool
	Synth(ctx context.Context, text string, rateDelta, pitchDelta, volDelta int) (string, error)
}

// STTService — распознающая сторона (реализация — internal/stt.Service).
type STTService interface {
	Available() bool
	Recognize(ctx context.Context, wavPath string) (string, error)
}

// New собирает HTTP-сервер: отдаёт персонажа, отвечает /healthz и держит /ws.
func New(opts Options) (*http.Server, error) {
	if opts.Hub == nil {
		return nil, fmt.Errorf("server: не передан хаб WebSocket")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"ok":       true,
			"protocol": protocolVersion,
			"clients":  opts.Hub.Count(),
			// Язык персонажа — наружу (этап 13): им оперирует самопроверка,
			// человек смотрит живьём после переключения из меню.
			"language": opts.Hub.Language(),
			// Сохранённая позиция видна снаружи: по ней самопроверка оболочки
			// проверяет, что окно не «запомнило» точку за пределами экрана.
			"position": opts.Hub.Position(),
			"time":     time.Now().UTC(),
		}
		// Активное окно человека — видно снаружи, пока трекер получает кадры
		// focus: по этой строке самопроверка судит, прошла ли цепочка
		// focuswatch → страница → сервер целиком.
		if s, ok := opts.Hub.GetFocus(); ok {
			resp["focus"] = map[string]any{
				"process": s.Process,
				"kind":    s.Kind,
				"label":   s.Label,
				"title":   s.Title,
				"since":   s.Since,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/ws", opts.Hub.HandleWS)

	// Память напоказ: человек видит, что она запомнила по его же словам, и
	// может удалить любой факт. Право «забыть» — у того, кому запомнили.
	// Сервер локальный (127.0.0.1): эти данные наружу не идут вовсе.
	memory := func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			facts, total, err := opts.Hub.ListFacts(offset, limit)
			if err != nil {
				http.Error(w, "память не читается: "+err.Error(), http.StatusInternalServerError)
				return
			}
			type factJSON struct {
				ID      int64  `json:"id"`
				Text    string `json:"text"`
				Created string `json:"created"`
			}
			out := make([]factJSON, 0, len(facts))
			for _, f := range facts {
				out = append(out, factJSON{f.ID, f.Text, f.Created.Format(time.RFC3339)})
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"facts": out, "total": total})
		case http.MethodDelete:
			idStr := strings.TrimPrefix(r.URL.Path, "/memory/")
			id, err := strconv.ParseInt(idStr, 10, 64)
			if idStr == "" || idStr == r.URL.Path || err != nil {
				http.Error(w, "удаление факта: DELETE /memory/{id}", http.StatusBadRequest)
				return
			}
			gone, err := opts.Hub.ForgetFact(id)
			if err != nil {
				http.Error(w, "факт не удалился: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if !gone {
				http.Error(w, "такого факта уже нет", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
		default:
			http.Error(w, "память показывают GET-ом, удаляют DELETE-ом", http.StatusMethodNotAllowed)
		}
	}
	mux.HandleFunc("/memory", memory)
	mux.HandleFunc("/memory/", memory)

	// Галерея характеров (этап 16): GET — витрина (файлы в каталоге персон
	// рядом с текущим), POST — применение выбранного. Применение валидирует
	// LoadFor и обновляет имя/матчер живьём; выбор пишет main.go в конфиг.
	mux.HandleFunc("/personas", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, cur, err := opts.Hub.Personas()
			if err != nil {
				http.Error(w, "галерея не читается: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"current": cur, "list": list})
		case http.MethodPost:
			var in struct {
				File string `json:"file"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil || in.File == "" {
				http.Error(w, "нужен {\"file\": \"...\"}", http.StatusBadRequest)
				return
			}
			if err := opts.Hub.ApplyPersona(in.File); err != nil {
				http.Error(w, "характер не применился: "+err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.Error(w, "галерею показывают GET-ом, применяют POST-ом", http.StatusMethodNotAllowed)
		}
	})

	// Внешний арт (этап 6): страница сама решает, есть ли лист — она просит
	// /sprites/sheet.json и получает 404, когда человек арт не подкладывал.
	// Поэтому здесь просто каталог, как и весь фронтенд — без кэша: правки
	// арта должны быть видны после перезагрузки окна.
	// http.Dir обрезает «..»: за пределы папки арта запрос не уйдёт.
	if opts.SpritesDir != "" {
		mux.Handle("/sprites/", noStore(http.StripPrefix("/sprites/",
			http.FileServer(http.Dir(opts.SpritesDir)))))
	}

	// Озвучка (этап 7): /tts?text=... → mp3. Сервер локальный (127.0.0.1),
	// запрос приходит от страницы персонажа. Ответ кэшируется генерацией:
	// один и тот же текст отдаётся из кэша мгновенно.
	if opts.TTS != nil && opts.TTS.Available() {
		mux.HandleFunc("/tts", func(w http.ResponseWriter, r *http.Request) {
			text := strings.TrimSpace(r.URL.Query().Get("text"))
			if text == "" {
				http.Error(w, "нужен текст (?text=...)", http.StatusBadRequest)
				return
			}
			// Интонация (этап 9б): страница сообщает настроение числом —
			// сдвиги темпа (проценты) и тона (герцы). Свои — ограничены ±50,
			// всё, что не число, = 0 (базовый голос).
			rateDelta, _ := strconv.Atoi(r.URL.Query().Get("rate"))
			pitchDelta, _ := strconv.Atoi(r.URL.Query().Get("pitch"))
			if rateDelta < -50 {
				rateDelta = -50
			}
			if rateDelta > 50 {
				rateDelta = 50
			}
			if pitchDelta < -50 {
				pitchDelta = -50
			}
			if pitchDelta > 50 {
				pitchDelta = 50
			}
			// Тише ночью (этап 11в): окно 23:00–7:00 — время, когда громкий
			// голос из колонок бьёт по тишине всей квартиры. −30% — та же
			// реплика, но вполголоса; в кэше она живёт отдельным файлом.
			volDelta := 0
			if h := time.Now().Hour(); h >= 23 || h < 7 {
				volDelta = -30
			}
			// Таймаут не берём из контекста запроса: страница обрывает
			// соединение, когда дослушано, а генерация должна дожить до
			// конца. Отдельный контекст с запасом на саму генерацию.
			path, err := opts.TTS.Synth(context.Background(), text, rateDelta, pitchDelta, volDelta)
			if err != nil {
				http.Error(w, "озвучка не получилась: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, path)
		})
	}

	// Голосовой ввод и wake word (этап 7): POST /stt и /wake с телом-wav.
	// Граница приватности: запись с микрофона расшифровывается локально
	// (faster-whisper на этой машине) и никуда, кроме текста, не превращается —
	// сам wav удаляется сразу после распознавания.
	if opts.STT != nil && opts.STT.Available() {
		sttDir := opts.STTDir
		if sttDir == "" {
			sttDir = os.TempDir()
		}
		mux.HandleFunc("/stt", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "узнавать голос можно только POST-ом", http.StatusMethodNotAllowed)
				return
			}
			tmpName, status, err := saveWavBody(r, sttDir)
			if err != nil {
				http.Error(w, http.StatusText(status)+" — "+err.Error(), status)
				return
			}
			defer os.Remove(tmpName)
			// Не request.Context(): страница ждёт ответа, а распознавание
			// живёт на своей стороне с собственным таймаутом (см. stt.Service).
			text, err := opts.STT.Recognize(context.Background(), tmpName)
			if err != nil {
				http.Error(w, "распознавание не удалось: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
		})

		// Wake word (этап 7): POST /wake с речевым сегментом → {"wake":...}.
		// Фронтенд будит её короткой фразой; сказанные ей слова уходят в
		// разговор как напечатанные (см. Hub.HandleWake).
		if opts.Wake {
			mux.HandleFunc("/wake", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.Error(w, "слышать имя можно только POST-ом", http.StatusMethodNotAllowed)
					return
				}
				tmpName, status, err := saveWavBody(r, sttDir)
				if err != nil {
					http.Error(w, http.StatusText(status)+" — "+err.Error(), status)
					return
				}
				defer os.Remove(tmpName)
				text, err := opts.STT.Recognize(context.Background(), tmpName)
				if err != nil {
					http.Error(w, "распознавание не удалось: "+err.Error(), http.StatusServiceUnavailable)
					return
				}
				mentioned := opts.Hub.WakeMatch(text)
				if mentioned {
					// В лог — только факт, не слова человека.
					opts.Log.Info("услышала своё имя")
					opts.Hub.HandleWake(text)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				_ = json.NewEncoder(w).Encode(map[string]any{"wake": mentioned, "text": text})
			})
		}
	}

	if opts.WebDir != "" {
		if _, err := os.Stat(filepath.Join(opts.WebDir, "index.html")); err != nil {
			return nil, fmt.Errorf("server: в каталоге %s нет index.html: %w", opts.WebDir, err)
		}
		fileServer := http.FileServer(http.Dir(opts.WebDir))
		mux.Handle("/", noStore(fileServer))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "каталог фронтенда не задан (-web)", http.StatusNotFound)
		})
	}

	return &http.Server{
		Addr:              opts.Addr,
		Handler:           cors(logRequests(opts.Log, mux)),
		ReadHeaderTimeout: 5 * time.Second,
	}, nil
}

// cors — ответы с разрешением чужого origin. Нужен оболочке Tauri: она отдаёт
// страницу из tauri://localhost, и браузер требует CORS на /stt /memory /wake.
// "*" здесь безопасен: сервер слушает только 127.0.0.1, наружу не светится,
// и данных «чужих» у него нет — всё предназначено этой же машине.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// saveWavBody — тело запроса-записи во временный wav. Отдатывает фильтры
// размеров (POST-эндпойнты голоса: /stt и /wake), пишет во временный файл в
// dir. Возвращает путь и HTTP-статус отказа; временный файл обязан быть
// удалён вызывающим (defer) — записи с микрофона на диске не остаются.
func saveWavBody(r *http.Request, dir string) (string, int, error) {
	// 8 МБ с запасом: минута разговора в 16 кГц mono — ~2 МБ,
	// больше сюда физически не помещается осмысленной записи.
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20+1))
	if err != nil {
		return "", http.StatusBadRequest, errors.New("тело запроса не прочиталось")
	}
	if len(body) > 8<<20 {
		return "", http.StatusRequestEntityTooLarge, errors.New("запись слишком большая")
	}
	// 44 байта — пустой WAV-заголовок; всё, что короче, — не запись.
	if len(body) < 100 {
		return "", http.StatusBadRequest, errors.New("запись слишком короткая")
	}
	tmp, err := os.CreateTemp(dir, "stt-*.wav")
	if err != nil {
		return "", http.StatusInternalServerError, errors.New("временный файл не создался")
	}
	name := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		os.Remove(name)
		return "", http.StatusInternalServerError, errors.New("запись не сохранилась")
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", http.StatusInternalServerError, errors.New("запись не закрылась")
	}
	return name, 0, nil
}

// noStore — фронтенд правится часто, а окно компаньона перезапускается мгновенно:
// кэш браузера здесь только мешает отладке (увидел старое поведение и ищешь баг
// в коде). Статика при этом кэшируется условно: Cache-Control без no-store для
// js/png был бы преждевременной оптимизацией при локальной загрузке.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		next.ServeHTTP(w, r)
	})
}

// logRequests — журнал запросов на уровне debug: на уровне info в консоли
// остаются только события компаньона (подключение, позиция, ошибки).
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Debug("http", "method", r.Method, "path", r.URL.Path, "ms", time.Since(start).Milliseconds())
	})
}
