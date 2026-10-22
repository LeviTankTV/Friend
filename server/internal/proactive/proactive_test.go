package proactive

import (
	"strings"
	"testing"
	"time"

	"friend/internal/focus"
)

func cfg() Config {
	return Config{
		MinGap:        25 * time.Minute,
		FoodReminder:  3 * time.Hour,
		QuietFromHour: 23,
		QuietToHour:   7,
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

func TestQuietSuppressesEverythingExceptSleepAndIdle(t *testing.T) {
	s := New(cfg())
	seed(t, s, day(20, 0)) // вечер прогрет, про смену вечером молчим

	// 23:30: молчание, вода и вечер — всё подавлено окном тишины.
	if ev, ok := s.Tick(Inputs{
		Now: day(23, 30), LastInput: day(23, 25), LastSay: day(18, 0),
		HasFocus: true, Focus: focus.Snapshot{Process: "chrome.exe", Kind: focus.KindBrowser},
	}); ok {
		t.Fatalf("в окно тишины нечего говорить, получил %+v", ev)
	}
	// 4:30 (ночь): «засыпаю» — можно (данные о мыши есть).
	if ev, ok := s.Tick(Inputs{
		Now: day(4, 30), LastInput: day(4, 0), // пропал на 30 мин
	}); !ok || ev.Kind != KindIdle {
		t.Fatalf("«я засыпаю» ночью обязано проходить: ok=%v ev=%+v", ok, ev)
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
