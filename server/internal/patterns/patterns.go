// Package patterns — паттерны жизни человека за ДНИ (этап 11а, глубина
// чувств). Одна ночь — событие, три ночи за неделю — усталость, которая
// копится: она это замечает и бережёт его в тоне.
//
// Только ФАКТЫ из локальной памяти: разговор с ней глубокой ночью означает,
// что человек не спал в этот час. Никаких догадок о том, что он делал, —
// ровно как с активным окном.
//
// Чистые функции на подменяемых часах: неделя проживётся в тестах за
// миллисекунды (тот же принцип, что mood и proactive).
package patterns

import "time"

// DeepNightHours — час, до которого «ещё не поздно». Сообщение в 00:xx–02:xx
// значит, что человек не спал за полночь; 23:xx вечером — норма, не считаем.
const deepNightHour = 3

// Window — за сколько ночей смотрим. Неделя: больше — уже не «он устал на
// этой неделе», а образ жизни, о котором she не имеет данных.
const Window = 7 * 24 * time.Hour

// MinNights — с скольких ночей паттерн считается замеченным. Одна-две ночи —
// случается у всех; три — недосып, который слышно в тоне.
const MinNights = 3

// LateNights — сколько разных ночей за последнее окно (7 дней) человек
// говорил с ней за полночь (00:00–02:59). Ночь относится к дате, на которую
// выпала: 02:00 вторника — ночь со вторника на среду, дата — вторник.
func LateNights(ats []time.Time, now time.Time) int {
	from := now.Add(-Window)
	nights := make(map[string]bool)
	for _, at := range ats {
		if at.Before(from) || at.After(now) {
			continue
		}
		if at.Hour() < deepNightHour {
			nights[at.Format("2006-01-02")] = true
		}
	}
	return len(nights)
}

// Notable — стоит ли говорить об этом паттерне: скрытые единичные ночи
// молчим, накопленный недосып — нет.
func Notable(nights int) bool { return nights >= MinNights }

// --- Недельный ритм (этап 6, «weekly rhythms» из плана README) -------------
//
// Ритм недели человека, видимый в том, КОГДА он с ней говорит. Не «что он
// делал» — только факт разговора в определённом 4-часовом блоке на
// определённом дне недели, повторившийся большую часть наблюдаемых недель.
// Тот же принцип, что у недосыпа: факты из локальной памяти, без догадок.

const (
	// RhythmWindow — четыре недели: четыре наблюдения каждого дня недели.
	// Меньше — «ритм» неотличим от двух случайных вечеров.
	RhythmWindow = 28 * 24 * time.Hour
	// RhythmBlock — ширина блока: «поздний вечер», «раннее утро», не час в лоб.
	RhythmBlock = 4 * time.Hour
	// RhythmMinWeeks — блок жил минимум в 3 из 4 недель.
	RhythmMinWeeks = 3
	// RhythmMinCount — и это не один случайный вечер: хотя бы шесть реплик.
	RhythmMinCount = 6
	// RhythmMinShare — половина разговоров этого дня недели живёт в блоке.
	RhythmMinShare = 0.5
	// RhythmGap — наблюдение ритма звучит не чаще раза в неделю.
	RhythmGap = 7 * 24 * time.Hour
)

// Rhythm — обнаруженный ритм дня недели: блок, где живут его разговоры,
// и сколько недель он держится.
type Rhythm struct {
	Weekday time.Weekday // 0 = воскресенье
	Hour    int          // начало блока (0, 4, 8, ..., 20)
	Weeks   int          // в скольких из 4 недель блок жил
	Count   int          // реплик в блоке за всё окно
}

// WeeklyRhythm — самый устойчивый блок «день недели + 4 часа» за окно.
// ok=false — на ритм не похоже (первые недели жизни с ней его и не может
// быть — это честно, а не поломка).
func WeeklyRhythm(ats []time.Time, now time.Time) (Rhythm, bool) {
	from := now.Add(-RhythmWindow)
	// weekIdx — номер недели наблюдения: 0 — текущая, 3 — самая старая.
	weekIdx := func(t time.Time) int { return int(now.Sub(t) / (7 * 24 * time.Hour)) }

	type key struct{ wd, block int }
	counts := map[key]int{}
	weeks := map[key]map[int]bool{}
	weekdayTotal := map[time.Weekday]int{}
	for _, at := range ats {
		if at.Before(from) || at.After(now) {
			continue
		}
		k := key{int(at.Weekday()), at.Hour() * int(time.Hour) / int(RhythmBlock)}
		counts[k]++
		weekdayTotal[at.Weekday()]++
		if weeks[k] == nil {
			weeks[k] = map[int]bool{}
		}
		weeks[k][weekIdx(at)] = true
	}

	best := Rhythm{}
	found := false
	for k, count := range counts {
		wk := len(weeks[k])
		total := weekdayTotal[time.Weekday(k.wd)]
		if total == 0 || wk < RhythmMinWeeks || count < RhythmMinCount {
			continue
		}
		if float64(count)/float64(total) < RhythmMinShare {
			continue
		}
		if !found || count > best.Count || (count == best.Count && wk > best.Weeks) {
			best = Rhythm{Weekday: time.Weekday(k.wd), Hour: k.block * int(RhythmBlock/time.Hour), Weeks: wk, Count: count}
			found = true
		}
	}
	return best, found
}
