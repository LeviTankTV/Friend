package patterns

import (
	"testing"
	"time"
)

// at — время в локали теста: ночь строится по ЧАСУ, часовых поясов в
// разборе нет (сервер живёт в локальном времени человека).
func at(day int, hour, min int) time.Time {
	return time.Date(2026, 10, day, hour, min, 0, 0, time.Local)
}

func TestLateNights(t *testing.T) {
	now := at(25, 10, 0) // воскресенье 25-го, утро
	// Три ночи за неделю: 20-е (00:40), 22-е (01:10), 24-е (02:05).
	got := LateNights([]time.Time{
		at(20, 0, 40), at(22, 1, 10), at(24, 2, 5),
		at(21, 14, 0), // днём — не ночь
		at(23, 23, 50), // 23:50 — «ещё не поздно», не считаем
	}, now)
	if got != 3 {
		t.Fatalf("три поздние ночи: %d", got)
	}
	// Ночь в 02:59 — считается, 03:00 — уже нет.
	if n := LateNights([]time.Time{at(24, 2, 59)}, now); n != 1 {
		t.Fatalf("02:59 — ночь: %d", n)
	}
	if n := LateNights([]time.Time{at(24, 3, 0)}, now); n != 0 {
		t.Fatalf("03:00 — уже не ночь: %d", n)
	}
	// За окном недели — не считается: недосып полугодовой давности не её дело.
	if n := LateNights([]time.Time{now.Add(-10 * 24 * time.Hour)}, now); n != 0 {
		t.Fatalf("старше недели — мимо: %d", n)
	}
	// Несколько сообщений одной ночью — одна ночь, а не три.
	if n := LateNights([]time.Time{at(24, 1, 0), at(24, 1, 30), at(24, 2, 10)}, now); n != 1 {
		t.Fatalf("одна ночь считается один раз: %d", n)
	}
}

func TestNotable(t *testing.T) {
	if Notable(2) {
		t.Fatal("две ночи — не паттерн")
	}
	if !Notable(3) || !Notable(5) {
		t.Fatal("три и больше — замечено")
	}
}
