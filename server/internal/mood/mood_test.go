package mood

import (
	"math"
	"testing"
	"time"
)

func almost(v, want float64) bool { return math.Abs(v-want) < 0.02 }

func TestApplyUserBoostsMoodAndAffection(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	st := Default(now)
	st.Affection = 0.5
	got := Apply(st, EvUser, now.Add(time.Minute))
	if got.Mood <= st.Mood {
		t.Fatalf("сообщение человека должно поднимать настроение: было %.2f, стало %.2f", st.Mood, got.Mood)
	}
	if got.Affection <= st.Affection {
		t.Fatalf("привязанность должна расти: %.2f -> %.2f", st.Affection, got.Affection)
	}
	if !got.LastUser.Equal(now.Add(time.Minute)) {
		t.Fatalf("LastUser должен обновиться: %+v", got.LastUser)
	}
	// Прежний снимок не испорчен (чистая функция).
	if st.Affection != 0.5 {
		t.Fatalf("Apply должен возвращать новый снимок, а не править старый")
	}
}

func TestApplyBoostStrongerWithAffection(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	lo := Default(now)
	lo.Mood, lo.Affection = 0.4, 0.1
	hi := Default(now)
	hi.Mood, hi.Affection = 0.4, 0.9
	dLo := Apply(lo, EvUser, now).Mood - lo.Mood
	dHi := Apply(hi, EvUser, now).Mood - hi.Mood
	if dHi <= dLo {
		t.Fatalf("для привязанной к человеку реплика должна радоваться сильнее: %.3f vs %.3f", dHi, dLo)
	}
}

func TestApplyClamps(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	st := State{Mood: 0.98, Energy: 0.5, Affection: 0.99, LastUser: now, At: now}
	got := Apply(st, EvUser, now)
	if got.Mood > 1 || got.Affection > 1 {
		t.Fatalf("значения должны оставаться в 0..1: %+v", got)
	}
	low := State{Mood: 0.02, Affection: 0.01}
	got = Apply(low, EvIgnored, now)
	if got.Mood < 0 || got.Affection < 0 {
		t.Fatalf("значения не могут стать отрицательными: %+v", got)
	}
}

func TestDriftEnergyToTarget(t *testing.T) {
	// Полдень, энергия высокая — к вечеру должна сползти к вечерней цели.
	noon := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	st := State{Mood: 0.55, Energy: 0.9, Affection: 0.5, LastUser: noon, At: noon}
	got := Drift(st, noon.Add(3*time.Hour))
	if got.Energy >= 0.9 || got.Energy < 0.6 {
		t.Fatalf("за 3 часа энергия должна стечь к вечерней цели (~0.60, шаг 0.08/ч): %.3f", got.Energy)
	}
	if !almost(got.Energy, 0.66) {
		t.Fatalf("скорость стекания ~0.08/час: ожидалось ~0.66, получилось %.3f", got.Energy)
	}
	// Ночь — энергия ползёт вверх к низкой цели, а не прыгает.
	night := time.Date(2026, 10, 22, 23, 30, 0, 0, time.UTC)
	st = State{Mood: 0.5, Energy: 0.6, Affection: 0.5, LastUser: night, At: night}
	got = Drift(st, night.Add(time.Hour))
	if got.Energy < 0.51 || got.Energy >= 0.6 {
		t.Fatalf("к ночи энергия должна снижаться плавно (~0.52 за час): %.3f", got.Energy)
	}
}

func TestDriftMoodRisesAfterContactAndFallsAfterSilence(t *testing.T) {
	// Свежий контакт (окно «свежести» считается по моменту внутри шага):
	// короткая серия тиков сразу после сообщения поднимает настроение.
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	st := State{Mood: 0.45, Energy: 0.6, Affection: 0.5, LastUser: now.Add(10 * time.Minute), At: now}
	got := Drift(st, now.Add(20*time.Minute))
	if got.Mood <= 0.45 {
		t.Fatalf("после свежего контакта настроение должно расти: %.2f -> %.2f", 0.45, got.Mood)
	}
	// Долгое молчание: падает к полу.
	st = State{Mood: 0.6, Energy: 0.6, Affection: 0.5, LastUser: now.Add(-8 * time.Hour), At: now}
	got = Drift(st, now.Add(2*time.Hour))
	if got.Mood >= 0.6 {
		t.Fatalf("после долгого молчания настроение должно падать: %.2f -> %.2f", 0.6, got.Mood)
	}
	// Посередине (между «свежо» и «долго») — стоит.
	st = State{Mood: 0.55, Energy: 0.6, Affection: 0.5, LastUser: now.Add(-2 * time.Hour), At: now}
	got = Drift(st, now.Add(time.Hour))
	if got.Mood != 0.55 {
		t.Fatalf("в промежутке настроение не дрейфует: %.3f", got.Mood)
	}
}

func TestDriftAffectionFade(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	st := State{Mood: 0.5, Energy: 0.6, Affection: 0.8, LastUser: now.Add(-20 * time.Hour), At: now}
	got := Drift(st, now.Add(time.Hour))
	if got.Affection >= 0.8 {
		t.Fatalf("за 20 часов отсутствия привязанность должна таять: %.3f -> %.3f", 0.8, got.Affection)
	}
	// Пол: таяние не доходит до нуля.
	floor := State{Mood: 0.5, Energy: 0.6, Affection: 0.21, LastUser: now.Add(-20 * time.Hour), At: now}
	got = Drift(floor, now.Add(20*time.Hour))
	if got.Affection < 0.2 {
		t.Fatalf("таяние должно иметь пол 0.20: %.3f", got.Affection)
	}
}

func TestIgnored(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	sent := now.Add(-40 * time.Minute)
	st := State{LastUser: now.Add(-2 * time.Hour)}
	if !Ignored(st, sent, now) {
		t.Fatal("реплика 40-минутной давности без ответа — игнор")
	}
	if Ignored(State{LastUser: sent.Add(5 * time.Minute)}, sent, now) {
		t.Fatal("если человек написал ПОСЛЕ её реплики — это не игнор")
	}
	if Ignored(st, now.Add(-10*time.Minute), now) {
		t.Fatal("раньше 30 минут игнор не считать: он мог просто отвечать")
	}
	if Ignored(st, time.Time{}, now) {
		t.Fatal("нулевой sentAt — повода не было")
	}
}

func TestBand(t *testing.T) {
	cases := []struct {
		mood float64
		want string
	}{
		{0.9, "high"}, {0.7, "high"}, {0.5, "mid"}, {0.36, "mid"}, {0.2, "low"},
	}
	for _, c := range cases {
		st := State{Mood: c.mood}
		if got := Band(st); got != c.want {
			t.Fatalf("mood=%.2f: полоска %q, ожидалось %q", c.mood, got, c.want)
		}
	}
}

func TestDescribeNoDigitsHasRule(t *testing.T) {
	line := Describe(State{Mood: 0.85, Energy: 0.2, Affection: 0.9})
	for _, want := range []string{"приподнятое", "усталость", "привязана", "Не пересказывай"} {
		if !contains(line, want) {
			t.Fatalf("в описании состояния нет %q: %s", want, line)
		}
	}
	for _, d := range "0123456789" {
		for i := 0; i < len(line); i++ {
			if line[i] == byte(d) {
				t.Fatalf("описание состояния не должно содержать цифр (%q): %s", string(d), line)
			}
		}
	}
}

func TestChangedThreshold(t *testing.T) {
	a := State{Mood: 0.5, Energy: 0.5, Affection: 0.5}
	b := State{Mood: 0.53, Energy: 0.5, Affection: 0.5}
	if Changed(a, b) {
		t.Fatal("сдвиг на 0.03 — шум, сохранять незачем")
	}
	c := State{Mood: 0.5, Energy: 0.56, Affection: 0.5}
	if !Changed(a, c) {
		t.Fatal("сдвиг на 0.06 — заметное изменение")
	}
}

func TestDriftZeroAtFirstSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	st := State{Mood: 0.55, Energy: 0.9, Affection: 0.5, LastUser: now} // At нулевой
	got := Drift(st, now.Add(3*time.Hour))
	if got.Energy != 0.9 {
		t.Fatalf("первый снимок с диска не должен мгновенно менять значения: %.3f", got.Energy)
	}
	if !got.At.Equal(now.Add(3 * time.Hour)) {
		t.Fatal("At должен заполниться моментом тика")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
