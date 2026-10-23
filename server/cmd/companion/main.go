// Команда companion — сервер виртуального компаньона (этап 2: разговор).
//
// Что делает: читает конфиг и файл ключей .env, поднимает локальный
// HTTP/WebSocket-сервер, отдаёт фронтенд персонажа, хранит состояние (позицию
// окна) между запусками и отвечает на реплики человека от лица персонажа через
// NVIDIA NIM.
//
// Запуск:
//
//	go run ./cmd/companion
//	go run ./cmd/companion -addr 127.0.0.1:9000 -config D:\my.json
//	go run ./cmd/companion -dump-persona > my-persona.json
//
// Остановка: Ctrl+C — сервер закрывает соединения и выходит.
package main

import (
	"context"
	"errors"
	"flag"
	"sync/atomic"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"friend/internal/chat"
	"friend/internal/companion"
	"friend/internal/config"
	"friend/internal/dotenv"
	"friend/internal/focus"
	"friend/internal/llm"
	"friend/internal/logging"
	"friend/internal/memory"
	"friend/internal/mood"
	"friend/internal/persona"
	"friend/internal/proactive"
	"friend/internal/server"
	"friend/internal/state"
	"friend/internal/stt"
	"friend/internal/tts"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ОШИБКА:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("companion", flag.ContinueOnError)
	configPath := fs.String("config", "", "путь к config.json (по умолчанию — каталог пользователя)")
	addr := fs.String("addr", "", "адрес сервера, перекрывает конфиг (напр. 127.0.0.1:8787)")
	webDir := fs.String("web", "", "каталог фронтенда персонажа (по умолчанию — web рядом с проектом)")
	// -dump-persona: выложить встроенный характер в файл и выйти. Так характер
	// можно править под себя, не пересобирая сервер и не разбирая бинарник.
	// Значение — язык файла (ru по умолчанию, en — английская Mira).
	dumpPersona := fs.String("dump-persona", "", "напечатать встроенный характер (JSON) и выйти; значение — язык (ru/en)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if cfgPath := config.ConfigPathFromArgs(os.Args[1:]); cfgPath != "" && *configPath == "" {
		*configPath = cfgPath
	}
	if *dumpPersona != "" {
		_, err := os.Stdout.Write(persona.EmbeddedJSONFor(*dumpPersona))
		return err
	}

	// Файл ключей читается ДО конфига: оттуда могут прийти переменные FRIEND_*,
	// а среда поверх файла — часть контракта (см. пакет dotenv).
	envRes, err := loadEnvFile()
	if err != nil {
		return err
	}

	cfg, note, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.Server.Addr = *addr
	}

	log, err := logging.New(os.Stdout, cfg.Log.Level)
	if err != nil {
		// Плохой уровень логирования — не повод не запуститься: ругаемся и
		// работаем на info.
		log, _ = logging.New(os.Stdout, logging.LevelInfo)
		log.Warn("уровень логирования из конфига не принят", "err", err)
	}
	slog.SetDefault(log)
	if note != "" {
		log.Info(note)
	}
	if envRes.Found() {
		// Наружу — только имена: значения из файла ключей в лог не попадают.
		log.Info("файл ключей прочитан", "file", envRes.Path,
			"переменные", envRes.Applied, "уже были заданы", envRes.Skipped)
	} else {
		log.Info("файла ключей .env нет", "ожидался", dotenv.Candidates(cwd(), config.DataDir()))
	}

	// Язык персоны (этап 10) выбирает и файл характера (встроенный ru/en),
	// и язык всех промптов, и голос со STT: одно поле в конфиге — вся Мира
	// меняет язык целиком. Явный persona.file перекрывает только характер,
	// язык всё равно берётся из конфига.
	profile, personaNote, err := persona.LoadFor(cfg.Persona.File, cfg.Avatar.Language)
	if err != nil {
		return err
	}
	log.Info(personaNote, "язык", profile.Language)

	// Имя в окне берём из характера: переименовал персонажа в его файле —
	// подпись в окне меняется вместе с ним, а не остаётся старой из конфига.
	if profile.Name != "" && profile.Name != cfg.Avatar.Name {
		log.Info("имя персонажа из характера", "в окне", profile.Name, "в конфиге", cfg.Avatar.Name)
		cfg.Avatar.Name = profile.Name
	}

	model := llm.New(cfg.LLM)
	log.Info("модель готова", "состояние", model.Status())

	sess := chat.New(cfg.LLM.HistoryMessages)
	comp := companion.New(profile, model, sess)
	// Активное окно человека: focuswatch → оболочка → страница → сервер
	// (тип focus в протоколе). Строка обстановки попадает в системный промпт
	// на каждую реплику. Без focuswatch (Linux/macOS, страница в браузере)
	// трекер пуст — промпт честно собирается без строки «он сейчас в …».
	tracker := focus.New()
	comp.SetContext(func() string {
		s, ok := tracker.Snapshot()
		if !ok {
			return ""
		}
		// Язык строки обстановки — язык персоны (этап 10): промпт цельный.
		return focus.Line(s, time.Now(), profile.Language)
	})

	store, err := state.Open(cfg.Paths.StateFile)
	if err != nil {
		// Битый файл состояния: тоже не повод не запускаться, но сказать надо.
		log.Warn("состояние прочитано с замечанием", "err", err)
	}
	log.Info("состояние", "file", store.Path(), "position", store.Snapshot().Position)

	// Внутреннее состояние (этап 5): настроение, энергия, привязанность.
	// Zero-снимок из старого файла — «fresh»: свежая Мира.
	moodSt := store.Snapshot().Mood
	if moodSt.At.IsZero() {
		moodSt = mood.Default(time.Now())
	}
	log.Info("настроение", "mood", moodSt.Mood, "energy", moodSt.Energy, "affection", moodSt.Affection)
	moodEvents := make(chan mood.Event, 16)

	// Долгая память (этап 3): SQLite-файл в каталоге пользователя. Не
	// открылся (битый файл, права, диск) — компаньон работает БЕЗ долгой
	// памяти: разговор, инициатива и «запомни:» (честным текстом про
	// отсутствие памяти) при этом живут.
	mem, memErr := memory.Open(cfg.Paths.MemoryFile)
	if memErr != nil {
		log.Warn("память не открылась — работаю без долгой памяти",
			"file", cfg.Paths.MemoryFile, "err", memErr)
	} else {
		defer mem.Close()
		// Восстановление истории: последние реплики архива в живую сессию.
		// Их время «перезаписывается» на момент старта — сознательная мелочь:
		// «последний разговор был недавно» после перезапуска честнее, чем
		// «был три дня назад» с часами из архива, по которым планировщик
		// начал бы догонять опоздавшие напоминания.
		if list, err := mem.RecentConvo(cfg.LLM.HistoryMessages); err == nil && len(list) > 0 {
			for _, m := range list {
				sess.Add(m.Role, m.Text)
			}
			log.Info("история восстановлена из памяти", "реплик", len(list))
		}
		log.Info("память", "file", cfg.Paths.MemoryFile,
			"фактов", mem.FactCount(), "саммари", mem.SummaryCount(), "архив", mem.ConvoCount())
	}

	web, err := resolveWebDir(*webDir)
	if err != nil {
		return err
	}
	log.Info("фронтенд", "dir", web)

	hub := server.NewHub(log, store, cfg.Avatar, comp)
	hub.SetKeyHint(keyHint(model, envRes))
	hub.SetFocusTracker(tracker)
	if mem != nil {
		// Долгая память: «запомни:» в хабе, факты и архив реплик в компаньоне.
		hub.SetMemory(mem)
		comp.SetMemory(mem)
	}
	// Внутреннее состояние: хаб сообщает реплики человека, компаньон слышит
	// настроение в промпте (источник — ниже), цикл решает, что с ним случилось.
	hub.SetMoodEvents(moodEvents)

	// Планировщик инициативы (этап 4): раз в tick_seconds смотрит обстановку
	// (активное окно, бездействие мыши, когда последний раз говорили) и
	// решает, заговорить ли первой. Правила анти-спама и окна тишины — в
	// internal/proactive; здесь только сбор входных данных и отправка.
	sched := proactive.New(proactive.Config{
		Enabled: cfg.Proactive.Enabled,
		MinGap:        time.Duration(cfg.Proactive.MinGapMinutes) * time.Minute,
		FoodReminder:  time.Duration(cfg.Proactive.FoodReminderMin) * time.Minute,
		QuietFromHour: cfg.Proactive.QuietFromHour,
		QuietToHour:   cfg.Proactive.QuietToHour,
	})
	// Озвучка (этап 7): Edge TTS через CLI. Отсутствие edge-tts — не ошибка
	// запуска: озвучка опциональна, и об этом говорит лог. Голос по умолчанию
	// следует языку персоны (этап 10), конфиг voice.edge_voice перекрывает.
	voiceSvc := tts.New(log, cfg.Voice.Enabled, cfg.Voice.EdgeVoice, cfg.Voice.Speed,
		filepath.Join(filepath.Dir(cfg.Paths.StateFile), "tts"), profile.Language)
	if voiceSvc.Available() {
		hub.SetVoice(&server.VoiceInfo{Available: true, Engine: "edge-tts", Voice: voiceSvc.Voice()})
	}
	// Голосовой ввод (этап 7): faster-whisper через python. Отсутствие python
	// или модели — не ошибка запуска: микрофон просто не показывается.
	// Распознавание локальное: запись с микрофона расшифровывается на этой
	// машине и никуда не уходит (в отличие от TTS, где текст летит в Microsoft).
	sttSvc := stt.New(log, cfg.STT.Enabled, cfg.STT.ModelDir,
		filepath.Join(filepath.Dir(cfg.Paths.StateFile), "stt"), profile.Language)
	if sttSvc.Available() {
		hub.SetSTT(&server.STTInfo{Available: true, Engine: "faster-whisper"})
	}
	// Wake word (этап 7): слушать её имя фоном можно, только если функция
	// включена человеком И есть локальный распознаватель. По умолчанию
	// выключено — постоянно слушать микрофон человек разрешает сам.
	wakeOn := cfg.Wake.Enabled && sttSvc.Available()
	if wakeOn {
		hub.SetWake(&server.WakeInfo{Available: true, Engine: "faster-whisper"})
	}
	srv, err := server.New(server.Options{Addr: cfg.Server.Addr, WebDir: web, SpritesDir: cfg.Paths.SpritesDir, TTS: voiceSvc, STT: sttSvc, STTDir: filepath.Join(filepath.Dir(cfg.Paths.StateFile), "stt"), Wake: wakeOn, Log: log, Hub: hub})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// moodCur — живое состояние: читается компаньоном при каждой сборке
	// промпта, пишется только циклом ниже. atomic.Value — чтобы читатель
	// (горoutine модели) и писатель (цикл) не цеплялись гонкой.
	var moodCur atomic.Value
	moodCur.Store(moodSt)
	moodGet := func() mood.State { return moodCur.Load().(mood.State) }
	moodSet := func(s mood.State) { moodCur.Store(s) }
	comp.SetMoodSource(moodGet)

	go proactiveLoop(ctx, log, sched, comp, hub, sess, tracker, mem, store,
		moodEvents, moodGet, moodSet, time.Duration(cfg.Proactive.TickSeconds)*time.Second, profile.Language)

	errCh := make(chan error, 1)
	go func() {
		log.Info("компаньон готов", "url", "http://"+cfg.Server.Addr, "name", cfg.Avatar.Name, "разговор", comp.Ready())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("останавливаюсь")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// loadEnvFile читает файл ключей .env (см. пакет dotenv).
func loadEnvFile() (dotenv.Result, error) {
	return dotenv.Load(dotenv.Candidates(cwd(), config.DataDir())...)
}

// cwd — рабочий каталог (обычно корень проекта: оттуда запускают start.bat).
func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}

// keyHint — что сказать человеку, если ключа нет.
//
// Текст собирается здесь, потому что путь к файлу ключей знает только
// запускающий код; хаб только показывает его в окне.
func keyHint(m *llm.Client, env dotenv.Result) string {
	envName := strings.TrimSpace(m.KeyEnv())
	if env.Found() {
		return "Впиши ключ в " + env.Path + " строкой " + envName + "=... и запусти start.bat снова."
	}
	return "Создай файл .env рядом со start.bat со строкой " + envName + "=... (или задай переменную окружения) и запусти start.bat снова."
}

// proactiveLoop — сердце инициативы (этап 4).
//
// Раз в tickSeconds собирает обстановку (активное окно + бездействие мыши из
// трекера focus, когда последний раз говорили — из истории чата), спрашивает
// планировщика «можно говорить?» и, если можно, просит у модели реплику по
// поводу. Всё, что решает «когда», живёт в internal/proactive; здесь только
// сбор данных, запрос и отправка кадра reply{proactive:true} всем окнам.
//
// Правила, которые здесь важны:
//   - провал модели отмечает ПОПЫТКУ: следующий тик не должен снова бить в
//     ту же модель (анти-спам по попыткам в самом планировщике);
//   - отправка отмечает отправку, а не намерение: реплика в историю попадает в
//     companion.Speak, и следующий ответ модели видит, что она заговорила;
//   - нет ключа — планировщик всё равно отмечает попытку: следующий заход
//     прогреет дистанцию и не устроит «тихий стук» в закрытую дверь.
func proactiveLoop(ctx context.Context, log *slog.Logger, sched *proactive.Scheduler,
	comp *companion.Companion, hub *server.Hub, sess *chat.Session, tracker *focus.Tracker,
	mem *memory.Store, store *state.Store, moodEvents <-chan mood.Event,
	moodGet func() mood.State, moodSet func(mood.State), tick time.Duration, lang string) {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	prevPart := persona.PartOf(time.Now())
	// Когда она заговорила сама в последний раз (для проверки игнора).
	var lastProactive time.Time
	// Проверка игнора уже применена к этой проактивной реплике.
	var ignoreChecked bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		now := time.Now()
		moodSt := moodGet()

		// События состояния (реплики человека из хаба) — все, что накопились.
		// break внутри select прерывает только select, поэтому для выхода из
		// цикла — метка.
	drain:
		for {
			select {
			case ev := <-moodEvents:
				moodSt = mood.Apply(moodSt, ev, now)
			default:
				break drain
			}
		}

		// Проактивная реплика без ответа — игнор. Считается один раз на
		// реплику, через 30 минут после отправки.
		if !lastProactive.IsZero() && !ignoreChecked && mood.Ignored(moodSt, lastProactive, now) {
			moodSt = mood.Apply(moodSt, mood.EvIgnored, now)
			ignoreChecked = true
			log.Info("её реплику проигнорировали", "настроение", moodSt.Mood)
		}

		// Дрейф за время.
		moodSt = mood.Drift(moodSt, now)

		// Сохраняем и показываем только заметное изменение: файл не должен
		// переписываться каждые полминуты от шума округления.
		if mood.Changed(moodGet(), moodSt) {
			moodSet(moodSt)
			if _, err := store.SetMood(moodSt); err != nil {
				log.Warn("состояние не сохранилось", "err", err)
			}
			hub.Broadcast(server.StateMsg{
				Type: server.TypeState,
				Mood: &server.MoodInfo{
					Mood: moodSt.Mood, Energy: moodSt.Energy,
					Affection: moodSt.Affection, Band: mood.Band(moodSt),
				},
			})
			log.Debug("состояние сдвинулось", "mood", moodSt.Mood, "energy", moodSt.Energy,
				"affection", moodSt.Affection, "band", mood.Band(moodSt))
		}
		// Ночь началась (смена части суток) — сжать день в саммари. Один
		// раз на день: проверка по дню в самой базе (HasSummary), а не по
		// памяти процесса: перезапуск сервера посреди ночи не должен плодить
		// второе саммари.
		part := persona.PartOf(now)
		if part == persona.Night && prevPart != persona.Night {
			go summarizeDay(ctx, log, comp, mem, now)
		}
		prevPart = part

		in := proactive.Inputs{Now: now, Language: lang}
		if s, ok := tracker.Snapshot(); ok {
			in.Focus, in.HasFocus = s, true
			in.LastInput = s.IdleAt
		}
		for _, m := range sess.Messages() {
			if m.Role == chat.RoleUser {
				in.LastSay = m.At
			}
		}

		ev, ok := sched.Tick(in)
		if !ok {
			continue
		}
		if !comp.Ready() {
			sched.MarkAttempt(time.Now())
			log.Debug("повод есть, разговора нет", "kind", ev.Kind)
			continue
		}
		cctx, cancel := context.WithTimeout(context.Background(), comp.Timeout()+15*time.Second)
		text, err := comp.Speak(cctx, ev.Reason)
		cancel()
		if err != nil {
			log.Warn("инициатива не получилась", "kind", ev.Kind, "err", err)
			sched.MarkAttempt(time.Now())
			continue
		}
		sched.MarkSent(ev.Kind, time.Now())
		// Idle-повод — это «она задремала»: засыпает прямо во фразе, и фронтенд
		// по полю emotion закроет ей глаза и пустит «Zzz». Остальные поводы —
		// обычные бодрствующие реплики.
		var emotion string
		if ev.Kind == proactive.KindIdle {
			emotion = "sleep"
		}
		n := hub.Broadcast(server.ReplyMsg{
			Type:      server.TypeReply,
			Text:      text,
			At:        time.Now().UTC(),
			Proactive: true,
			Emotion:   emotion,
		})
		log.Info("заговорила сама", "kind", ev.Kind, "текста", len([]rune(text)), "окон", n)
		// Помечаем момент её реплики: через 30 минут цикл проверит, ответил
		// ли человек (игнор опускает настроение).
		lastProactive = time.Now()
		ignoreChecked = false
	}
}

// summarizeDay — ночное саммари (этап 3): сжимает разговор за последние сутки
// в одну заметку о человеке и кладёт в базу. Правила:
//   - саммари уже есть за сегодня — ничего не делаем (перезапуск посреди
//     ночи не плодит второе);
//   - реплик меньше шести (3 обмена) — сжимать нечего, молчим;
//   - нет ключа / модель сбойнула — Warn и тишина: саммари — не критично.
func summarizeDay(ctx context.Context, log *slog.Logger, comp *companion.Companion,
	mem *memory.Store, at time.Time) {
	if mem == nil || mem.HasSummary(at) {
		return
	}
	if !comp.Ready() {
		log.Debug("саммари отложено: разговора нет")
		return
	}
	list, err := mem.ConvoSince(at.Add(-24 * time.Hour))
	if err != nil {
		log.Warn("архив разговора не прочитался", "err", err)
		return
	}
	userCount := 0
	var sb strings.Builder
	for _, m := range list {
		if m.Role == chat.RoleUser {
			userCount++
		}
		sb.WriteString(m.Role + ": " + m.Text + "\n")
	}
	if userCount < 3 {
		log.Debug("саммари отложено: разговора за день мало", "реплик", len(list))
		return
	}
	cctx, cancel := context.WithTimeout(context.Background(), comp.Timeout()+30*time.Second)
	text, err := comp.Summarize(cctx, sb.String())
	cancel()
	if err != nil {
		log.Warn("саммари не вышел", "err", err)
		return
	}
	if text == "" {
		log.Warn("саммари пустое")
		return
	}
	if err := mem.SaveSummary(text, at); err != nil {
		log.Warn("саммари не записался", "err", err)
		return
	}
	log.Info("саммари за день записан", "знаков", len([]rune(text)), "реплик", len(list))
}

// resolveWebDir ищет каталог фронтенда: явный флаг, затем «web» рядом с
// бинарником (собранный релиз), затем «web» рядом с исходниками (go run из
// server/). Так один и тот же код работает и в разработке, и в сборке — без
// хардкода путей.
func resolveWebDir(flagValue string) (string, error) {
	candidates := []string{}
	if flagValue != "" {
		candidates = append(candidates, flagValue)
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "web"),
			filepath.Join(exeDir, "..", "web"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "web"),
			filepath.Join(wd, "..", "web"),
		)
	}
	var tried []string
	for _, c := range candidates {
		tried = append(tried, c)
		if st, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("не нашёл index.html персонажа (искал: %v) — передай -web", tried)
}
