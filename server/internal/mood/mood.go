// Package mood — внутреннее состояние Миры: настроение, энергия, привязанность.
//
// Этап 5. Состояние живёт ЧИСТЫМИ функциями: вход — прежний снимок, событие
// (или время), выход — новый снимок. Никаких горутин, файлов и часов: их
// добавляет вызывающий (main.go), и именно поэтому модель проверяется на
// фейковых часах, а не ожиданием настоящих суток.
//
// Что состояние делает с речью: из него собирается ОДНА строка в системный
// промпт (Describe) — модель слышит тон, но не пересказывает цифры человеку.
// На фронтенд уезжает полоска (Band) — по ней выбирается «лицо покоя».
//
// Почему три числа, а не одна «эмоция»: эмоция мгновенная (сказали приятное —
// улыбка), состояние медленное (вечер без разговоров — грусть). Модель должна
// отличать одно от другого: сегодня она обрадовалась, а настроение упало.
package mood

import (
	"math"
	"time"
)

// State — снимок состояния. Все значения в диапазоне 0..1.
type State struct {
	Mood      float64 `json:"mood"`
	Energy    float64 `json:"energy"`
	Affection float64 `json:"affection"`
	// LastUser — когда человек последний раз с ней говорил. Молчание
	// считается именно по нему: её собственные проактивные реплики молчание
	// не прерывают (иначе «она сама написала» занижало бы счёт игнорирования).
	LastUser time.Time `json:"last_user"`
	// At — момент снимка: по нему Drift считает прошедшее время. Нулевой At
	// (первый снимок после загрузки с диска) означает «время не считаем».
	At time.Time `json:"at"`
}

// Параметры модели. Все скорости — в единицах за ЧАС: состояние должно быть
// заметным за вечер, но не должно перещёлкиваться от одного сообщения.
const (
	userAffectionGain    = 0.04 // привязанность за одно сообщение
	ignoredMoodLoss      = 0.15 // настроение за проигнорированную реплику
	ignoredAffectionLoss = 0.02 // привязанность за игнор (вдвое слабее, с полом)
	ignoredAfter         = 30 * time.Minute

	recentContact   = 30 * time.Minute
	longSilence     = 4 * time.Hour
	moodCeiling     = 0.80
	moodFloor       = 0.25
	moodContactRate = 0.04 // к потолку после свежего контакта
	moodSilenceRate = 0.02 // к полу после долгого молчания

	affectionFadeAfter = 16 * time.Hour
	affectionFloor     = 0.20
	affectionFadeRate  = 0.002

	energyMaxPerHour = 0.08
	driftCap         = 12 * time.Hour // максимальный шаг времени за один Drift
)

// Default — состояние свежесозданной Миры: спокойная, бодрость обычная,
// к незнакомому человеку сдержанно-нейтральная.
func Default(now time.Time) State {
	return State{Mood: 0.55, Energy: 0.6, Affection: 0.35, LastUser: now, At: now}
}

// Event — событие, меняющее состояние.
type Event int

const (
	// EvUser — человек сам написал ей: поднимает настроение (сильнее при
	// высокой привязанности) и понемногу растит привязанность.
	EvUser Event = iota
	// EvIgnored — она заговорила сама, а человек НЕ ответил (см. Ignored):
	// реплика повисла в пустоте, настроение падает.
	EvIgnored
)

// Apply применяет событие к снимку. Чистая функция: возвращает новый снимок,
// прежний не меняет.
func Apply(st State, ev Event, at time.Time) State {
	switch ev {
	case EvUser:
		// Всплеск настроения привязан к близости: одно и то же сообщение
		// радостнее для той, кто к человеку уже теплая.
		st.Mood = clamp(st.Mood + 0.10 + 0.10*st.Affection)
		st.Affection = clamp(st.Affection + userAffectionGain)
		st.LastUser = at
	case EvIgnored:
		st.Mood = clamp(st.Mood - ignoredMoodLoss)
		st.Affection = clamp(st.Affection - ignoredAffectionLoss)
	}
	if !st.At.IsZero() {
		st.At = at
	}
	return st
}

// Drift — продвижение состояния за время (вызывается раз в тик цикла).
//
// Правила:
//   - энергия всегда ползёт к «целевой по части суток» (ночью низкая, утром
//     высокая) с потолком скорости: резких скачков не бывает;
//   - настроение после свежего контакта поднимается к потолку, после долгого
//     молчания опускается к полу; между — стоит (события и так делают своё);
//   - привязанность за долгое отсутствие человека медленно тает, с полом:
//     неделя без общения не превращает её в равнодушие.
func Drift(st State, now time.Time) State {
	if st.At.IsZero() || now.Sub(st.At) < 0 {
		st.At = now
		return st
	}
	dt := now.Sub(st.At)
	if dt > driftCap {
		dt = driftCap
	}
	st.At = now

	// Правила применяются по часовым отрезкам: каждый отрезок оценивается по
	// своему концу (условие «свежести» контакта, посчитанное по старому
	// началу длинного шага, терялось бы, когда контакт «протухает» посреди
	// drift-шага).
	for from := now.Add(-dt); from.Before(now); {
		to := from.Add(time.Hour)
		if to.After(now) {
			to = now
		}
		h := to.Sub(from).Hours()

		// Энергия — к целевой по часам отрезка.
		target := energyTarget(to)
		st.Energy = driftToward(st.Energy, target, energyMaxPerHour, h)

		// Настроение — по молчанию на конце отрезка.
		silence := to.Sub(st.LastUser)
		switch {
		case silence <= recentContact:
			st.Mood = driftToward(st.Mood, moodCeiling, moodContactRate, h)
		case silence >= longSilence:
			st.Mood = driftToward(st.Mood, moodFloor, moodSilenceRate, h)
		}

		// Привязанность — тает за отсутствие.
		if silence >= affectionFadeAfter {
			st.Affection = driftToward(st.Affection, affectionFloor, affectionFadeRate, h)
		}
		from = to
	}
	st.Energy = round3(st.Energy)
	st.Mood = round3(st.Mood)
	st.Affection = round3(st.Affection)
	return st
}

// Ignored — проигнорирована ли её проактивная реплика, посланная в sentAt:
// с тех пор прошло достаточно времени, а человек после неё не написал.
func Ignored(st State, sentAt time.Time, now time.Time) bool {
	if sentAt.IsZero() || now.Sub(sentAt) < ignoredAfter {
		return false
	}
	return st.LastUser.Before(sentAt)
}

// Band — полоска для фронтенда: «лицо покоя» выбирается по ней
// (low — грустноватое, high — лучезарное, mid — спокойное).
func Band(st State) string {
	switch {
	case st.Mood < 0.35:
		return "low"
	case st.Mood > 0.65:
		return "high"
	default:
		return "mid"
	}
}

// Describe — строка для системного промпта. Цифры модели не показываются:
// ей нужен тон, а не таблица.
func Describe(st State) string {
	mood := "ровное"
	switch {
	case st.Mood >= 0.7:
		mood = "приподнятое"
	case st.Mood < 0.35:
		mood = "упавшее, грустинка"
	}
	energy := "обычная"
	switch {
	case st.Energy >= 0.7:
		energy = "бодрость"
	case st.Energy < 0.35:
		energy = "усталость, сонливость"
	}
	aff := "ровная"
	switch {
	case st.Affection >= 0.7:
		aff = "очень привязана к человеку"
	case st.Affection < 0.3:
		aff = "сдержанная, человека знает недавно"
	}
	return "Твоё внутреннее состояние сейчас: настроение — " + mood +
		"; энергия — " + energy + "; отношение к человеку — " + aff + "." +
		" Это слышно в твоём тоне и длине реплик. Не пересказывай своё состояние" +
		" цифрами, не считай его вслух и не жалуйся на него — оно просто звучит в речи."
}

// Changed — заметно ли состояние сдвинулось (порог: сохранять и показывать
// только значимое, файл не должен переписываться каждые полминуты).
func Changed(a, b State) bool {
	return math.Abs(a.Mood-b.Mood) >= 0.05 ||
		math.Abs(a.Energy-b.Energy) >= 0.05 ||
		math.Abs(a.Affection-b.Affection) >= 0.05
}

// energyTarget — целевая энергия по части суток (границы — как в persona).
func energyTarget(t time.Time) float64 {
	switch h := t.Hour(); {
	case h >= 6 && h < 12:
		return 0.75
	case h >= 12 && h < 18:
		return 0.60
	case h >= 18 && h < 23:
		return 0.45
	default:
		return 0.20
	}
}

// driftToward — сдвиг значения к цели со скоростью rate в час за dtHours.
func driftToward(v, target, ratePerHour, dtHours float64) float64 {
	d := target - v
	step := ratePerHour * dtHours
	if math.Abs(d) <= step {
		return target
	}
	return clamp(v + math.Copysign(step, d))
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// round3 — три знака после точки: состояния хранятся и пересылаются как числа,
// шум за последней цифрой не должен плодить «изменения».
func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
