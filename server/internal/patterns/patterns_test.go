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
		at(21, 14, 0),  // днём — не ночь
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

// WeeklyRhythm (этап 6): четыре субботы подряд с разговорами в 23–24 →
// ритм «суббота, блок 20:00–24:00» найден.
func TestWeeklyRhythmDetectsHabit(t *testing.T) {
	now := time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC) // суббота
	var ats []time.Time
	// Четыре субботы, по два разговора в 23:00–23:59 и один в 22:xx.
	for w := 0; w < 4; w++ {
		base := now.Add(-time.Duration(w) * 7 * 24 * time.Hour)
		ats = append(ats, base, base.Add(20*time.Minute), base.Add(-40*time.Minute))
	}
	// Немного шума в другие дни — не должно перебить ритм.
	for w := 0; w < 4; w++ {
		ats = append(ats, now.Add(-time.Duration(w)*7*24*time.Hour-time.Duration(24+w)*time.Hour))
	}
	r, ok := WeeklyRhythm(ats, now)
	if !ok {
		t.Fatal("ритм не найден")
	}
	if r.Weekday != time.Saturday || r.Hour != 20 {
		t.Fatalf("ритм %v %d:00, ожидал субботу 20:00", r.Weekday, r.Hour)
	}
	if r.Weeks < 3 || r.Count < 6 {
		t.Fatalf("weeks=%d count=%d, ожидал >=3 и >=6", r.Weeks, r.Count)
	}
}

// Первые две недели жизни с ней ритма быть не может — и это честно.
func TestWeeklyRhythmNeedsWeeks(t *testing.T) {
	now := time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC)
	var ats []time.Time
	for w := 0; w < 2; w++ {
		base := now.Add(-time.Duration(w) * 7 * 24 * time.Hour)
		ats = append(ats, base, base.Add(15*time.Minute), base.Add(-30*time.Minute))
	}
	if _, ok := WeeklyRhythm(ats, now); ok {
		t.Fatal("ритм нашёлся на двух неделях наблюдений")
	}
}

// Разбросанные разговоры без повторяющегося блока — не ритм.
func TestWeeklyRhythmIgnoresScatter(t *testing.T) {
	now := time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC)
	var ats []time.Time
	for d := 0; d < 28; d++ {
		// каждый день по разговору в разное время
		ats = append(ats, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).
			Add(time.Duration(d)*24*time.Hour+time.Duration(d*3)*time.Hour))
	}
	if _, ok := WeeklyRhythm(ats, now); ok {
		t.Fatal("разброс принят за ритм")
	}
}
