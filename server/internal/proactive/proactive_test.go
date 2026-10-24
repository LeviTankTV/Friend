package proactive

import (
	"strings"
	"testing"
	"time"

	"friend/internal/focus"
)

func cfg() Config {
	return Config{
		Enabled:       true,
		MinGap:        25 * time.Minute,
		FoodReminder:  3 * time.Hour,
		QuietFromHour: 23,
		QuietToHour:   7,
	}
}

// Ночная бережность (этап 9д): глубокая ночь + редактор час и больше —
// одна мягкая реплика про него. Требует код-окно; браузер не трогает;
// одна за ночь; днём не срабатывает.
func TestNightCareForEditor(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(20, 0))
	// Ночной ритуал уже сказан (ночь началась в 23:00, ритуал — в 23:05) —
	// дальше по приоритету идёт бережность. Отстоять надо не меньше
	// удвоенного анти-спама: в редакторе он «занят».
	s.MarkSent(KindNight, day(23, 5))
	s.MarkSent(KindSilence, day(22, 40)) // только обнуляет анти-спам

	in := Inputs{
		Now: day(23, 40), LastInput: day(23, 35),
		HasFocus: true, Focus: focus.Snapshot{
			Process: "goland64.exe", Kind: focus.KindCode, Label: "GoLand", Since: day(22, 30),
		},
	}
	ev, ok := s.Tick(in)
	if !ok || ev.Kind != KindCare {
		t.Fatalf("час в редакторе в глубокой ночи — бережность: ok=%v ev=%+v", ok, ev)
	}
	if !strings.Contains(ev.Reason, "GoLand") || !strings.Contains(ev.Reason, "22:30") {
		t.Fatalf("в поводе должны быть метка окна и время начала: %q", ev.Reason)
	}
	s.MarkSent(ev.Kind, day(23, 40))
	if ev, ok := s.Tick(Inputs{
		Now: day(23, 50), LastInput: day(23, 45),
		HasFocus: true, Focus: focus.Snapshot{Process: "goland64.exe", Kind: focus.KindCode, Since: day(22, 30)},
	}); ok {
		t.Fatalf("за одну ночь бережность произносится один раз: %+v", ev)
	}
	// Тот же редакторский марафон, но НЕ ночь (12:40) — бережность молчит,
	// живёт обычный «чай в редакторе».
	s2 := New(cfg())
	seed(t, s2, day(9, 0))
	if ev, ok := s2.Tick(Inputs{
		Now: day(12, 40), LastInput: day(12, 35), LastSay: day(9, 0),
		HasFocus: true, Focus: focus.Snapshot{Process: "code.exe", Kind: focus.KindCode, Label: "VS Code", Since: day(10, 0)},
	}); ok && ev.Kind == KindCare {
		t.Fatalf("днём ночная бережность не звучит: %+v", ev)
	}
}

// Поддразнивание игрой (этап 9д): полтора часа в игре — одна тёплая реплика
// на заход; смена игры обнуляет счёт; ночью молчит.
func TestGameTease(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(12, 0))

	in := Inputs{
		Now: day(14, 0), LastInput: day(13, 55), LastSay: day(12, 0),
		HasFocus: true, Focus: focus.Snapshot{
			Process: "steam.exe", Kind: focus.KindGame, Label: "Cyberpunk", Since: day(12, 20),
		},
	}
	ev, ok := s.Tick(in)
	if !ok || ev.Kind != KindGame {
		t.Fatalf("полтора часа в игре — поддразнить: ok=%v ev=%+v", ok, ev)
	}
	s.MarkSent(ev.Kind, day(14, 0))
	if ev, ok := s.Tick(Inputs{
		Now: day(15, 0), LastInput: day(14, 55), LastSay: day(12, 0),
		HasFocus: true, Focus: focus.Snapshot{Process: "steam.exe", Kind: focus.KindGame, Since: day(12, 20)},
	}); ok {
		t.Fatalf("про этот заход в игру она уже поддела: %+v", ev)
	}
	// Ночь (окно тишины) — игра молчит.
	if ev, ok := s.Tick(Inputs{
		Now: day(23, 30).AddDate(0, 0, 1), LastInput: day(23, 25).AddDate(0, 0, 1), LastSay: day(12, 0),
		HasFocus: true, Focus: focus.Snapshot{Process: "steam.exe", Kind: focus.KindGame, Label: "Cyberpunk", Since: day(22, 0).AddDate(0, 0, 1)},
	}); ok && ev.Kind == KindGame {
		t.Fatalf("в окно тишины игра не поёт: %+v", ev)
	}
}

// Планировщик выключен — молчит любой тик, любой час, любая обстановка
// (так самопроверка глушит инициативы, чтобы они не шумели в проверки).
func TestDisabledIsSilent(t *testing.T) {
	c := cfg()
	c.Enabled = false
	s := New(c)
	s.SetStarted(day(18, 0))
	for _, at := range []time.Time{day(19, 0), day(23, 30), day(3, 30)} {
		if ev, ok := s.Tick(Inputs{Now: at, LastInput: at.Add(-time.Hour), LastSay: day(12, 0)}); ok {
			t.Fatalf("выключенный планировщик заговорил в %v: %+v", at, ev)
		}
	}
}

// day строит «сегодня в HH:MM» от фиксированной даты — часы в тестах должны
// быть предсказуемыми, это не «now плюс-минут».
func day(h, m int) time.Time {
	return time.Date(2026, 10, 22, h, m, 0, 0, time.UTC)
}

// seed прогревает планировщик первым тиком (после него части суток считаются
// сменами, а не фактом).
func seed(t *testing.T, s *Scheduler, at time.Time) {
	t.Helper()
	s.SetStarted(at.Add(-4 * time.Hour))
	if ev, ok := s.Tick(Inputs{Now: at}); ok {
		t.Fatalf("первый тик не должен ничего говорить, получил %+v", ev)
	}
}

func TestFirstTickIsSilent(t *testing.T) {
	s := New(cfg())
	if ev, ok := s.Tick(Inputs{Now: day(15, 0), LastInput: day(15, 0)}); ok {
		t.Fatalf("первый тик обязан прогревать, а не приветствовать: %+v", ev)
	}
}

// «Иди спать» — единственный триггер, которому окно тишины не преграда:
// смысл реплики в том, чтобы человек услышал её ночью.
func TestSleepAllowedInQuiet(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(2, 0))

	ev, ok := s.Tick(Inputs{
		Now: day(3, 20), LastInput: day(3, 15),
	})
	if !ok || ev.Kind != KindSleep {
		t.Fatalf("в 3:20 «иди спать» обязано сработать, ok=%v ev=%+v", ok, ev)
	}
	if !strings.Contains(ev.Reason, "3:20") {
		t.Fatalf("в поводе должно быть время: %q", ev.Reason)
	}

	// Одна за ночь: сразу после отправки анти-спам, и на следующий тик тоже.
	s.MarkSent(ev.Kind, day(3, 20))
	if ev, ok := s.Tick(Inputs{Now: day(3, 25), LastInput: day(3, 20)}); ok {
		t.Fatalf("после отправленной ночной реплики говорить рано: %+v", ev)
	}
	s.MarkSent(KindSilence, day(6, 50)) // другая инициатива «обнулила» анти-спам
	if ev, ok := s.Tick(Inputs{Now: day(6, 55), LastInput: day(6, 50)}); ok {
		t.Fatalf("«иди спать» во вторую ночь окна быть не должно: %+v", ev)
	}
}

func TestQuietSuppressesEverythingExceptNightSleepAndIdle(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(20, 0)) // вечер прогрет, про смену вечером молчим

	// 23:30: в окно тишины проходит только ночной ритуал («пора спать, я
	// рядом») — он ради ночи и придуман; молчание, вода и вечер подавлены.
	ev, ok := s.Tick(Inputs{
		Now: day(23, 30), LastInput: day(23, 25), LastSay: day(18, 0),
		HasFocus: true, Focus: focus.Snapshot{Process: "chrome.exe", Kind: focus.KindBrowser},
	})
	if !ok || ev.Kind != KindNight {
		t.Fatalf("в 23:30 обязан пройти только ночной ритуал: ok=%v ev=%+v", ok, ev)
	}
	s.MarkSent(ev.Kind, day(23, 30))
	// 23:40: ритуал этой ночи сказан, остальное — окно тишины.
	if ev, ok := s.Tick(Inputs{
		Now: day(23, 40), LastInput: day(23, 35), LastSay: day(18, 0),
		HasFocus: true, Focus: focus.Snapshot{Process: "chrome.exe", Kind: focus.KindBrowser},
	}); ok {
		t.Fatalf("после ритуала в окно тишины говорить нечем: %+v", ev)
	}
	// 4:30 УТРА (следующего дня: ночь 23:30 уже позади): «засыпаю» — можно
	// (данные о мыши есть).
	if ev, ok := s.Tick(Inputs{
		Now: day(4, 30).AddDate(0, 0, 1), LastInput: day(4, 0).AddDate(0, 0, 1), // пропал на 30 мин
	}); !ok || ev.Kind != KindIdle {
		t.Fatalf("«я засыпаю» ночью обязано проходить: ok=%v ev=%+v", ok, ev)
	}
}

// Ночной ритуал (этап 9г): «пора спать, я рядом» — с 23:00, одна за ночь
// (ночь = от 23:00, послеполуночные часы ночуют «вчера»), только рядом.
func TestNightRitualOncePerNight(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(20, 0))

	// 22:30 — глубокая ночь ещё не началась.
	if ev, ok := s.Tick(Inputs{Now: day(22, 30), LastInput: day(22, 25)}); ok && ev.Kind == KindNight {
		t.Fatalf("до 23:00 ночного ритуала быть не должно: %+v", ev)
	}
	// 23:10 — ритуал с временем в поводе.
	ev, ok := s.Tick(Inputs{Now: day(23, 10), LastInput: day(23, 5)})
	if !ok || ev.Kind != KindNight {
		t.Fatalf("в 23:10 ритуал обязан сработать: ok=%v ev=%+v", ok, ev)
	}
	if !strings.Contains(ev.Reason, "23:10") {
		t.Fatalf("в поводе должно быть время: %q", ev.Reason)
	}
	s.MarkSent(ev.Kind, day(23, 10))
	// Та же ночь, 1:30 — ритуал уже сказан (ночь началась вчера в 23:00).
	if ev, ok := s.Tick(Inputs{Now: day(1, 30), LastInput: day(1, 25)}); ok && ev.Kind == KindNight {
		t.Fatalf("за одну ночь ритуал произносится один раз: %+v", ev)
	}
	// Следующая ночь 23:05 — снова можно.
	if ev, ok := s.Tick(Inputs{
		Now: day(23, 5).AddDate(0, 0, 1), LastInput: day(23, 0).AddDate(0, 0, 1),
	}); !ok || ev.Kind != KindNight {
		t.Fatalf("на следующую ночь ритуал обязан обновиться: ok=%v ev=%+v", ok, ev)
	}
}

// Ритуалы утра и вечера (этап 9г) несут время суток и факт про редактор.
func TestRitualsCarryTimeAndEditorFact(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(3, 0)) // ночь: утро станет сменой, а не фактом

	ev, ok := s.Tick(Inputs{
		Now: day(7, 5), LastInput: day(7, 0), LastSay: day(6, 50),
		HasFocus: true, Focus: focus.Snapshot{
			Process: "goland64.exe", Kind: focus.KindCode, Label: "GoLand", Since: day(6, 30),
		},
	})
	if !ok || ev.Kind != KindMorning {
		t.Fatalf("утренний ритуал обязан сработать: ok=%v ev=%+v", ok, ev)
	}
	if !strings.Contains(ev.Reason, "7:05") || !strings.Contains(ev.Reason, "редактор") {
		t.Fatalf("в утреннем поводе должны быть время и факт про редактор: %q", ev.Reason)
	}
	s.MarkSent(ev.Kind, day(7, 5))
	// Обнуляет анти-спам перед вечером; в редакторе дистанция двойная (50 мин),
	// потому отстояние — час.
	s.MarkSent(KindSilence, day(17, 0))

	// Вечер: смена части суток — тот же набор (время + редактор).
	ev, ok = s.Tick(Inputs{
		Now: day(18, 5), LastInput: day(18, 0), LastSay: day(17, 0),
		HasFocus: true, Focus: focus.Snapshot{
			Process: "code.exe", Kind: focus.KindCode, Label: "VS Code", Since: day(17, 0),
		},
	})
	if !ok || ev.Kind != KindEvening {
		t.Fatalf("вечерний ритуал обязан сработать: ok=%v ev=%+v", ok, ev)
	}
	if !strings.Contains(ev.Reason, "18:05") || !strings.Contains(ev.Reason, "редактор") {
		t.Fatalf("в вечернем поводе должны быть время и факт про редактор: %q", ev.Reason)
	}
}

func TestIdleRequiresMouseData(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(12, 0))

	// 15:00, данных о мыши нет (LastInput нулевой) — «пропал» не срабатывает
	// вслепую, на Linux idle не бывает вовсе.
	if ev, ok := s.Tick(Inputs{Now: day(15, 0)}); ok && ev.Kind == KindIdle {
		t.Fatalf("без данных о мыши «пропал» сработало вслепую: %+v", ev)
	}
	if ev, ok := s.Tick(Inputs{
		Now: day(15, 0), LastInput: day(14, 0), // пропал на час
	}); !ok || ev.Kind != KindIdle {
		t.Fatalf("час без мыши — она обязана засыпать: ok=%v ev=%+v", ok, ev)
	}
}

func TestSilenceNudgeForBusyIsSuppressed(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(12, 0))

	// Два часа молчания, он рядом, в браузере (не занят) — мягкий намёк.
	ev, ok := s.Tick(Inputs{
		Now: day(15, 0), LastInput: day(14, 55), LastSay: day(12, 10),
		HasFocus: true, Focus: focus.Snapshot{Process: "chrome.exe", Kind: focus.KindBrowser},
	})
	if !ok || ev.Kind != KindSilence {
		t.Fatalf("два часа молчания рядом — повод появиться: ok=%v ev=%+v", ok, ev)
	}
	s.MarkSent(ev.Kind, day(15, 0))

	// Тот же молчащий, но в редакторе (занят) — молчим. Заход в редактор
	// свежий (10 минут) — «два часа в коде» не срабатывает, молчание занятого
	// не тревожат ничем.
	if ev, ok := s.Tick(Inputs{
		Now: day(17, 0), LastInput: day(16, 55), LastSay: day(16, 40),
		HasFocus: true, Focus: focus.Snapshot{
			Process: "goland64.exe", Kind: focus.KindCode, Since: day(16, 50),
		},
	}); ok {
		t.Fatalf("занятого молчанием не тревожат: %+v", ev)
	}
}

func TestAppMarathonOncePerVisit(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(9, 0))

	in := Inputs{
		Now: day(16, 0), LastInput: day(15, 58), LastSay: day(15, 40),
		HasFocus: true, Focus: focus.Snapshot{
			Process: "goland64.exe", Kind: focus.KindCode, Label: "GoLand (IDE для Go)",
			Since: day(13, 0), // в GoLand с 13:00 — уже 3 часа
		},
	}
	ev, ok := s.Tick(in)
	if !ok || ev.Kind != KindApp {
		t.Fatalf("три часа в редакторе — она обязана предложить чай: ok=%v ev=%+v", ok, ev)
	}
	s.MarkSent(ev.Kind, day(16, 0))
	if ev, ok := s.Tick(Inputs{
		Now: day(17, 0), LastInput: day(16, 58), LastSay: day(15, 40),
		HasFocus: true, Focus: focus.Snapshot{Process: "goland64.exe", Kind: focus.KindCode, Since: day(13, 0)},
	}); ok {
		t.Fatalf("про этот заход в приложение она уже сказала: %+v", ev)
	}
	// Сменил приложение: ушёл в 16:00 (про тот заход чай уже сказан),
	// вернулся в 16:10 и сидит снова два часа — счёт с последней смены,
	// напоминание может прозвучать заново.
	if ev, ok := s.Tick(Inputs{
		Now: day(18, 10), LastInput: day(18, 8), LastSay: day(15, 40),
		HasFocus: true, Focus: focus.Snapshot{
			Process: "code.exe", Kind: focus.KindCode, Label: "VS Code (редактор кода)",
			Since: day(16, 10),
		},
	}); !ok || ev.Kind != KindApp {
		t.Fatalf("смена приложения обязана обнулять счёт: ok=%v ev=%+v", ok, ev)
	}
}

func TestMorningGreetingOnDayPartChangeOnly(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(3, 0)) // ночь: приветствие утром будет «сменой», не фактом

	// Утро после окна тишины (7:05): части суток сменились — приветствие.
	// С ним только что говорили — «вода» и «молчание» не перебивают приветствие.
	ev, ok := s.Tick(Inputs{
		Now: day(7, 5), LastInput: day(7, 0), LastSay: day(6, 50),
	})
	if !ok || ev.Kind != KindMorning {
		t.Fatalf("смена на утро обязана дать приветствие: ok=%v ev=%+v", ok, ev)
	}
	s.MarkSent(ev.Kind, day(7, 5))
	// Тот же день, 7:30: утро уже отмечено — молчим (незадолго до этого
	// с ним говорили, молчание не созрело).
	if ev, ok := s.Tick(Inputs{
		Now: day(7, 30), LastInput: day(7, 28), LastSay: day(7, 20),
	}); ok {
		t.Fatalf("приветствие обязано быть однократным на смену: %+v", ev)
	}
}

func TestAntiSpamGapDoublesWhenBusy(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(9, 0))

	// Отправила намёк в 12:00; в 12:20 (MinGap=25) он занят — дистанция 50 мин.
	s.MarkSent(KindSilence, day(12, 0))
	if ev, ok := s.Tick(Inputs{
		Now: day(12, 20), LastInput: day(12, 18), LastSay: day(9, 0),
		HasFocus: true, Focus: focus.Snapshot{Process: "code.exe", Kind: focus.KindCode},
	}); ok {
		t.Fatalf("занятому — двойная дистанция анти-спама: %+v", ev)
	}
	// Занятость ушла, дистанция 25 мин уже прошла — говорить можно.
	if ev, ok := s.Tick(Inputs{
		Now: day(12, 30), LastInput: day(12, 28), LastSay: day(9, 0),
		HasFocus: true, Focus: focus.Snapshot{Process: "chrome.exe", Kind: focus.KindBrowser},
	}); !ok {
		t.Fatalf("после MinGap вне занятости повод обязан пройти: ok=%v ev=%+v", ok, ev)
	}
}

func TestWaterRespectsFoodReminder(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(8, 0))

	// 10:00 — прошло 2 ч с запуска/последней реплики, порог 3 ч не достигнут.
	if ev, ok := s.Tick(Inputs{
		Now: day(10, 0), LastInput: day(9, 58), LastSay: day(8, 30),
	}); ok && ev.Kind == KindWater {
		t.Fatalf("раньше порога про воду молчим: %+v", ev)
	}
	if ev, ok := s.Tick(Inputs{
		Now: day(12, 0), LastInput: day(11, 58), LastSay: day(8, 30),
	}); !ok || ev.Kind != KindWater {
		t.Fatalf("три часа без воды рядом с ней — повод: ok=%v ev=%+v", ok, ev)
	}
}

func TestAttemptCooldown(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(9, 0))
	// Модель упала сразу после попытки — следующие 5 минут тишина.
	s.MarkAttempt(day(12, 0))
	if ev, ok := s.Tick(Inputs{
		Now: day(12, 3), LastInput: day(12, 0), LastSay: day(9, 0),
		HasFocus: true, Focus: focus.Snapshot{Process: "chrome.exe", Kind: focus.KindBrowser},
	}); ok {
		t.Fatalf("после провала пулемёт по модели запрещён: %+v", ev)
	}
}

// Этап 11а: накопленный недосып — дневная бережная реплика раз в день.
func TestPatternFiresOncePerDay(t *testing.T) {
	s := New(Config{Enabled: true, MinGap: 0, FoodReminder: time.Hour, QuietFromHour: 23, QuietToHour: 7})
	now := time.Date(2026, 10, 25, 14, 0, 0, 0, time.Local)
	s.SetStarted(now.Add(-time.Minute)) // иначе про воду сработает раньше паттерна
	in := Inputs{Now: now, LateNights: 4}
	in.LastInput = now // рядом
	_, _ = s.Tick(Inputs{Now: now.Add(-30 * time.Second)}) // посев части суток

	ev, ok := s.Tick(in)
	if !ok || ev.Kind != KindPattern {
		t.Fatalf("три ночи и больше — бережная реплика: %+v ok=%v", ev, ok)
	}
	if !strings.Contains(ev.Reason, "4 ночей") && !strings.Contains(ev.Reason, "4") {
		t.Fatalf("в поводе должно быть число ночей: %q", ev.Reason)
	}
	s.MarkSent(KindPattern, now)
	// Второй тик того же дня — молчит.
	if _, ok := s.Tick(in); ok {
		t.Fatal("про недосып — раз в день")
	}
	// Меньше трёх ночей — повода нет вовсе.
	s2 := New(Config{Enabled: true, MinGap: 0, FoodReminder: time.Hour, QuietFromHour: 23, QuietToHour: 7})
	s2.SetStarted(now.Add(-time.Minute))
	_, _ = s2.Tick(Inputs{Now: now.Add(-30 * time.Second)})
	if _, ok := s2.Tick(Inputs{Now: now, LateNights: 2, LastInput: now}); ok {
		t.Fatal("две ночи — не паттерн")
	}
	// Окно тишины глушит (ночь — не время напоминать про недосып).
	night := time.Date(2026, 10, 26, 1, 0, 0, 0, time.Local)
	s3 := New(Config{Enabled: true, MinGap: 0, FoodReminder: time.Hour, QuietFromHour: 23, QuietToHour: 7})
	s3.SetStarted(night.Add(-time.Minute))
	_, _ = s3.Tick(Inputs{Now: night.Add(-30 * time.Second)})
	// Ночные поводы честны этой ночью — молчать должен именно паттерн.
	s3.MarkSent(KindNight, night)
	s3.MarkSent(KindSleep, night)
	ev2, ok2 := s3.Tick(Inputs{Now: night, LateNights: 5, LastInput: night})
	if ok2 && ev2.Kind == KindPattern {
		t.Fatal("глубокой ночью паттерн молчит")
	}
}

// Этап 11б: встреча после долгого отсутствия — переход «не было → есть»,
// короче трёх часов не считается, повторно на том же возвращении не звучит.
func TestReturnAfterAbsence(t *testing.T) {
	s := New(cfg())
	s.SetStarted(day(9, 0))
	// Утром он за машиной.
	if _, ok := s.Tick(Inputs{Now: day(9, 0), LastInput: day(8, 58)}); ok {
		t.Fatal("посев присутствия не повод говорить")
	}
	// Обед: его нет полтора часа (короче AwayMin), вернулся — встречи нет
	// (вода в это время сработать может: 4,5 ч от старта — её право).
	if ev, ok := s.Tick(Inputs{Now: day(13, 30), LastInput: day(13, 28)}); ok && ev.Kind == KindReturn {
		t.Fatal("полтора часа отсутствия — не встреча")
	}
	// Ушёл в 13:58: тик в 18:00 видит «его нет» (она может засыпать — её
	// право; встречей это не считается).
	if ev, ok := s.Tick(Inputs{Now: day(18, 0), LastInput: day(13, 58)}); ok && ev.Kind == KindReturn {
		t.Fatal("отсутствующему встречу не говорят")
	}
	// Вернулся в 19:00: переход «не было → есть», пять часов отсутствия.
	ev, ok := s.Tick(Inputs{Now: day(19, 0), LastInput: day(19, 0)})
	if !ok || ev.Kind != KindReturn {
		t.Fatalf("пять часов отсутствия — встреча: ok=%v ev=%+v", ok, ev)
	}
	if !strings.Contains(ev.Reason, "5 часов") {
		t.Fatalf("в поводе — сколько его не было: %q", ev.Reason)
	}
	s.MarkSent(ev.Kind, day(19, 0))
	// Он на месте; обычные тики встречи не повторяют.
	if ev, ok := s.Tick(Inputs{Now: day(19, 30), LastInput: day(19, 28)}); ok && ev.Kind == KindReturn {
		t.Fatal("встреча звучит один раз на возвращение")
	}
	// Сутки отсутствия считаются днями.
	s2 := New(cfg())
	s2.SetStarted(day(9, 0))
	_, _ = s2.Tick(Inputs{Now: day(9, 0), LastInput: day(8, 58)})
	// Тик «его нет» между присутствиями — иначе перехода не видно.
	_, _ = s2.Tick(Inputs{Now: day(9, 0).Add(26 * time.Hour), LastInput: day(8, 58)})
	ev2, ok := s2.Tick(Inputs{Now: day(9, 0).Add(26*time.Hour + 2*time.Hour), LastInput: day(9, 0).Add(26*time.Hour + 2*time.Hour)})
	if !ok || ev2.Kind != KindReturn || !strings.Contains(ev2.Reason, "1 день") {
		t.Fatalf("сутки отсутствия: ok=%v ev=%+v", ok, ev2)
	}
}

// Этап 11б: возврат к старому факту — раз в день, занятому не мешает.
func TestFollowupOncePerDay(t *testing.T) {
	s := New(cfg())
	s.SetStarted(day(9, 0))
	_, _ = s.Tick(Inputs{Now: day(9, 0), LastInput: day(8, 58)})
	base := Inputs{
		Now: day(12, 0), LastInput: day(11, 58), LastSay: day(11, 50),
		FollowupFact: "кот ездит на дачу", FollowupDays: 40,
	}
	ev, ok := s.Tick(base)
	if !ok || ev.Kind != KindFollowup {
		t.Fatalf("старый факт — вернуться к нему: ok=%v ev=%+v", ok, ev)
	}
	if !strings.Contains(ev.Reason, "40 дней назад") || !strings.Contains(ev.Reason, "кот ездит") {
		t.Fatalf("в поводе — возраст и сам факт: %q", ev.Reason)
	}
	s.MarkSent(KindFollowup, day(12, 0))
	if _, ok := s.Tick(base); ok {
		t.Fatal("к фактам возвращаются раз в день")
	}
	// Занятому в коде — не сейчас (факт подождёт свободного момента).
	s2 := New(cfg())
	s2.SetStarted(day(9, 0))
	_, _ = s2.Tick(Inputs{Now: day(9, 0), LastInput: day(8, 58)})
	busy := base
	busy.HasFocus = true
	busy.Focus = focus.Snapshot{Process: "goland64.exe", Kind: focus.KindCode, Label: "GoLand", Since: day(11, 0)}
	if _, ok := s2.Tick(busy); ok {
		t.Fatal("занятому в редакторе факты не подсовывают")
	}
}
