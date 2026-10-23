// Package proactive — когда она заговаривает ПЕРВОЙ.
//
// Компаньон, который молчит до первого слова человека, — половина компаньона.
// Этап 4 даёт ей поводы: ночь на дворе, человек пропал, два часа в редакторе,
// пора бы воды, утро началось. Но у той же медали есть обратная сторона, и она
// опаснее: компаньон, который строчит по любому поводу, превращается в
// оповещатель и раздражает за неделю. Поэтому правила здесь жёстче самой
// логики триггеров:
//
//   - ОДНА реплика за тик, и тот, что самый важный по приоритету;
//   - анти-спам: не чаще одной инициативы в MinGap (а когда человек занят —
//     вдвое реже);
//   - окно тишины (по умолчанию 23:00–07:00): ночью она молчит. Единственные
//     исключения — «иди спать» (одна за ночь) и «я засыпаю» (он тоже пропал);
//   - «занят» (код/игра/офис/терминал): мелочи (вода, молчание, приветствия)
//     молчат, крупные напоминания — только с удвоенной дистанцией.
//
// Tick — чистая функция на подменяемых часах: тесты гоняют «ускоренные сутки»
// без ожидания, и каждый триггер проверяется числом, а не «подождём до вечера».
// Сам цикл (тики и разговор с моделью) живёт снаружи: пакет решает «можно ли
// говорить и о чём», а «сказать» — задача вызывающего.
package proactive

import (
	"fmt"
	"sync"
	"time"

	"friend/internal/focus"
	"friend/internal/i18n"
	"friend/internal/persona"
)

// d — словарь языка персоны из входа тика (этап 10).
func d(in Inputs) *i18n.Dict { return i18n.For(in.Language) }

// Kind — какой триггер сработал. Нужен вызывающему: после ОТПРАВЛЕННОЙ
// реплики он отмечает именно этот факт (у «иди спать» и «два часа в GoLand»
// свои правила повтора).
type Kind string

const (
	KindSleep   Kind = "sleep"   // «иди спать» (после 3:00, одна за ночь)
	KindIdle    Kind = "idle"    // он пропал — она засыпает на рабочем столе
	KindWater   Kind = "water"   // пора пить/есть
	KindApp     Kind = "app"     // слишком долго в одном деле (код)
	KindMorning Kind = "morning" // утро началось
	KindEvening Kind = "evening" // вечер: как прошёл день
	KindSilence Kind = "silence" // он рядом, но молчит
	// KindNight — ритуал глубокой ночи (этап 9г): «пора спать, я рядом».
	// Отличается от KindSleep: «иди спать» — про 3 часа ночи и уговор,
	// ритуал — про начало ночи (23:00) и про «я рядом», без уговора.
	KindNight Kind = "night"
	// KindCare — ночная бережность (этап 9д): поздно ночью он в редакторе
	// не первый час. Не «иди спать» и не ритуал — про него лично:
	// «ты здесь с 23:00, передышка?».
	KindCare Kind = "care"
	// KindGame — поддразнивание игрой (этап 9д): долго в игре — короткая
	// тёплая реплика с улыбкой («опять?»), без упрёка.
	KindGame Kind = "game"
)

// Пороги триггеров. Всё, что стоит за числом с объяснением.
const (
	NightFromHour  = 23               // с этого часа — ритуал «пора спать, я рядом»
	SleepFromHour  = 3                // с этого часа можно сказать «иди спать»
	SleepToHour    = 7                // до этого (ночью она в целом молчит)
	SleepGap       = 3 * time.Hour    // и одна, и вторая ночная реплика не чаще
	IdleSleepMin   = 25 * time.Minute // пропал на 25 минут — она засыпает
	IdleGap        = 2 * time.Hour    // «засыпать» дважды за короткий прогон нельзя
	WaterGap       = 3 * time.Hour    // если в конфиге FoodReminder меньше, он и есть
	AppMarathonMin = 2 * time.Hour    // «в редакторе уже 2 часа»
	GameMarathon   = 90 * time.Minute // «в игре уже полтора часа» — поддразнить
	CareMarathon   = 60 * time.Minute // час в редакторе ночью — бережность
	PresenceMax    = 20 * time.Minute // после этого он уже «не рядом»
	SilenceMin     = 60 * time.Minute // час молчания при нём — повод мягко появиться
)

// Config — настройки из конфига (proactive.*), уже переваренные в длительности.
// Enabled=false — она никогда не заговаривает первой (самопроверка глушит
// планировщик, чтобы проактивные реплики не шумели в середину проверок).
type Config struct {
	Enabled       bool
	MinGap        time.Duration // анти-спам: минимум между её инициативами
	FoodReminder  time.Duration // как часто напоминать про воду
	QuietFromHour int           // окно тишины, час начала (23)
	QuietToHour   int           // окно тишины, час конца (7)
}

// Inputs — обстановка на момент тика. Все часы — подменяемые, тесты гоняют
// сутки за миллисекунды.
type Inputs struct {
	Now time.Time
	// LastInput — когда в последний раз двигали мышью/клавиатурой.
	// Нулевой — данных нет (focuswatch не на Windows): тогда считаем,
	// что человек рядом, и «он пропал» не срабатывает никогда.
	LastInput time.Time
	// LastSay — когда человек последний раз говорил с ней. Нулевой — за
	// время жизни сервера он не сказал ничего.
	LastSay time.Time
	// Focus — активное окно (см. internal/focus). ok=false — данных нет.
	Focus    focus.Snapshot
	HasFocus bool
	// Language — язык персоны (этап 10): на нём собираются тексты поводов.
	Language string
}

// Event — сработавший повод. Reason — короткий текст-установка для модели
// (см. companion.Speak): что произошло и какого тона реплику она должна дать.
type Event struct {
	Kind   Kind
	Reason string
}

type rule struct {
	allowQuiet bool // можно ли говорить в окно тишины («иди спать» — можно)
}

type Scheduler struct {
	cfg Config

	mu sync.Mutex
	// Состояние «что уже было»: именно оно отличает третий раз за вечер
	// от первого.
	started       time.Time
	lastProactive time.Time // когда была ОТПРАВЛЕНА последняя её инициатива
	lastAttempt   time.Time // когда она пыталась сказать (успешно или нет)
	lastSleep     time.Time // «иди спать» сегодня уже было?
	lastNight     time.Time // ночной ритуал «пора спать, я рядом» уже был за эту ночь?
	lastCare      time.Time // ночная бережность про редактор уже была за эту ночь?
	lastAppNag    time.Time // «чай в редакторе» уже было? (для этого захода в приложение)
	lastGameNag   time.Time // «опять в игре?» уже было? (для этого захода в игру)
	lastDayPart   persona.DayPart
	seeded        bool
}

// New создаёт планировщик. started = сейчас: отсчёты «сколько он молчит»
// начинаются с запуска сервера, а не из воздуха.
func New(cfg Config) *Scheduler {
	if cfg.MinGap < 5*time.Minute {
		cfg.MinGap = 25 * time.Minute
	}
	if cfg.FoodReminder < 30*time.Minute {
		cfg.FoodReminder = 3 * time.Hour
	}
	return &Scheduler{cfg: cfg, started: time.Now()}
}

// SetStarted подменяет «момент запуска сервера» (для тестов на фейковых часах:
// все отсчёты молчания тянутся от запуска, и тесту нужно задать его точкой).
func (s *Scheduler) SetStarted(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = t
}

// quiet — окно тишины (часы, wrap через полночь).
func (s *Scheduler) quiet(t time.Time) bool {
	h := t.Hour()
	return h >= s.cfg.QuietFromHour || h < s.cfg.QuietToHour
}

// present — человек рядом. Данных о мыши нет — верим, что рядом (на Linux у
// нас idle не бывает; выгонять триггеры «человек пропал» вслепую нельзя).
func present(in Inputs) bool {
	return in.LastInput.IsZero() || in.Now.Sub(in.LastInput) <= PresenceMax
}

// busy — он занят делом (код, игра, офис, терминал): мелочи молчат,
// крупные — вдвое реже.
func busy(in Inputs) bool {
	if !in.HasFocus {
		return false
	}
	switch in.Focus.Kind {
	case focus.KindCode, focus.KindGame, focus.KindOffice, focus.KindTerminal:
		return true
	}
	return false
}

// gap — требуемая дистанция от последней инициативы.
func (s *Scheduler) gap(in Inputs) time.Duration {
	g := s.cfg.MinGap
	if busy(in) {
		g *= 2
	}
	return g
}

// Tick оценивает обстановку и решает, заговаривать ли.
//
// Один тик — максимум одна реплика: самый старший по приоритету триггер,
// который проходит ВСЕ свои условия. Tick ничего не отправляет и не помнит —
// состояние обновляется только отметками MarkSent/MarkAttempt, которые
// ставит вызывающий.
func (s *Scheduler) Tick(in Inputs) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.cfg.Enabled {
		return Event{}, false // планировщик выключен человеком (или самопроверкой)
	}

	if !s.seeded {
		// Первый тик: запоминаем текущую часть суток, не считая её «сменой».
		// Иначе каждое включение сервера вечером начиналось бы репликой
		// «вечер, как прошёл день?» — как приветствие по расписанию.
		s.lastDayPart = persona.PartOf(in.Now)
		s.seeded = true
		return Event{}, false
	}

	// Порог анти-спама. Between attempts держим ту же дистанцию: модель
	// без ключа не должна превращать тик в пулемёт.
	if !s.lastAttempt.IsZero() && in.Now.Sub(s.lastAttempt) < 5*time.Minute {
		return Event{}, false
	}

	type cand struct {
		kind Kind
		f    func(Inputs) (Event, bool)
		rule rule
	}
	cands := []cand{
		{KindNight, s.night, rule{allowQuiet: true}},
		{KindCare, s.care, rule{allowQuiet: true}},
		{KindSleep, s.sleep, rule{allowQuiet: true}},
		{KindIdle, s.idle, rule{allowQuiet: true}},
		{KindWater, s.water, rule{}},
		{KindApp, s.app, rule{}},
		{KindGame, s.game, rule{}},
		{KindMorning, s.morning, rule{}},
		{KindEvening, s.evening, rule{}},
		{KindSilence, s.silence, rule{}},
	}

	for _, c := range cands {
		if s.quiet(in.Now) && !c.rule.allowQuiet {
			continue
		}
		ev, ok := c.f(in)
		if !ok {
			continue
		}
		if !s.lastProactive.IsZero() && in.Now.Sub(s.lastProactive) < s.gap(in) {
			return Event{}, false // анти-спам: повод есть, но говорить рано
		}
		return ev, true
	}
	return Event{}, false
}

// night — ритуал глубокой ночи (этап 9г): начало ночи в 23:00, он ещё не
// ложится — ОДНА тёплая короткая реплика «пора спать, я рядом». Это не уговор
// (уговор — KindSleep в три часа): здесь она сама засыпает и показывает, что
// она рядом. Разрешена в окно тишины, одна за ночь (ночь = от 23:00; для
// часов после полуночи ночь началась «вчера»).
func (s *Scheduler) night(in Inputs) (Event, bool) {
	h := in.Now.Hour()
	if h >= SleepFromHour && h < NightFromHour {
		return Event{}, false // 3:00–23:00 — глубокая ночь не идёт
	}
	nightDay := in.Now
	if h < SleepFromHour {
		nightDay = in.Now.Add(-24 * time.Hour) // 01:30 — ночь началась вчера в 23:00
	}
	nightStart := time.Date(nightDay.Year(), nightDay.Month(), nightDay.Day(),
		NightFromHour, 0, 0, 0, in.Now.Location())
	if !s.lastNight.IsZero() && !s.lastNight.Before(nightStart) {
		return Event{}, false // в эту ночь ритуал уже сказан
	}
	if !present(in) {
		return Event{}, false // ритуал — тому, кто ещё бодрствует рядом
	}
	return Event{Kind: KindNight, Reason: fmt.Sprintf(d(in).NightReason, in.Now.Format("15:04"))}, true
}

// sleep — «иди спать». Разрешена в окно тишины: смысл реплики именно в том,
// чтобы человек услышал её НОЧЬЮ. Одна за ночь (ночь = от 3:00 сегодняшних).
func (s *Scheduler) sleep(in Inputs) (Event, bool) {
	h := in.Now.Hour()
	if h < SleepFromHour || h >= SleepToHour {
		return Event{}, false
	}
	nightStart := time.Date(in.Now.Year(), in.Now.Month(), in.Now.Day(), SleepFromHour, 0, 0, 0, in.Now.Location())
	if !s.lastSleep.IsZero() && !s.lastSleep.Before(nightStart) {
		return Event{}, false // сегодня в эту ночь она уже это говорила
	}
	if !s.lastSleep.IsZero() && in.Now.Sub(s.lastSleep) < SleepGap {
		return Event{}, false
	}
	if !present(in) {
		return Event{}, false // «иди спать» говорят тому, кто не спит, а не пустоте
	}
	return Event{Kind: KindSleep, Reason: fmt.Sprintf(d(in).SleepReason, in.Now.Format("15:04"))}, true
}

// idle — он пропал: не трогает мышь. Она засыпает у него на рабочем столе.
// Требует ДАННЫХ о мыши: без них «пропал» было бы вечной правдой.
func (s *Scheduler) idle(in Inputs) (Event, bool) {
	if in.LastInput.IsZero() {
		return Event{}, false
	}
	idle := in.Now.Sub(in.LastInput)
	if idle < IdleSleepMin {
		return Event{}, false
	}
	if !s.lastProactive.IsZero() && in.Now.Sub(s.lastProactive) < IdleGap {
		return Event{}, false
	}
	minutes := int(idle.Minutes())
	return Event{Kind: KindIdle, Reason: fmt.Sprintf(d(in).IdleReason, minutes)}, true
}

// water — пора пить. Порог из конфига (по умолчанию 3 часа); молчит ночью и
// когда занят (тогда работает глобальный анти-спам с двойной дистанцией).
func (s *Scheduler) water(in Inputs) (Event, bool) {
	last := s.lastProactive
	if !in.LastSay.IsZero() && last.Before(in.LastSay) {
		last = in.LastSay // с ним только что говорили — «пора пить» не свежа
	}
	if last.Before(s.started) {
		last = s.started
	}
	if in.Now.Sub(last) < s.cfg.FoodReminder {
		return Event{}, false
	}
	if !present(in) {
		return Event{}, false
	}
	if busy(in) {
		return Event{}, false // «занят → реже»: занятому про воду не напоминать
	}
	return Event{Kind: KindWater, Reason: fmt.Sprintf(d(in).WaterReason,
		int(in.Now.Sub(s.started).Minutes()))}, true
}

// care — ночная бережность (этап 9д): глубокая ночь (то же окно, что и у
// ритуала: 23:00–3:00), он в редакторе кода час и больше, рядом. Одна за
// ночь. Это не «иди спать» — про него лично: мягкая бережность и передышка.
func (s *Scheduler) care(in Inputs) (Event, bool) {
	h := in.Now.Hour()
	if h >= SleepFromHour && h < NightFromHour {
		return Event{}, false // не глубокая ночь
	}
	if !in.HasFocus || in.Focus.Kind != focus.KindCode {
		return Event{}, false // бережность — про редактор, не про браузер
	}
	since := in.Now.Sub(in.Focus.Since)
	if since < CareMarathon {
		return Event{}, false // засиделся недолго
	}
	nightDay := in.Now
	if h < SleepFromHour {
		nightDay = in.Now.Add(-24 * time.Hour)
	}
	nightStart := time.Date(nightDay.Year(), nightDay.Month(), nightDay.Day(),
		NightFromHour, 0, 0, 0, in.Now.Location())
	if !s.lastCare.IsZero() && !s.lastCare.Before(nightStart) {
		return Event{}, false // в эту ночь она это уже сказала
	}
	if !present(in) {
		return Event{}, false
	}
	hours := int(since.Hours())
	return Event{Kind: KindCare, Reason: fmt.Sprintf(d(in).CareReason,
		in.Now.Format("15:04"), in.Focus.Label, hours, in.Focus.Since.Format("15:04"))}, true
}

// game — поддразнивание игрой (этап 9д): полтора часа в игре — короткая
// тёплая реплика с улыбкой. Окно тишины её подавляет (ночью игры не
// комментируем), одна реплика на заход в игру, как с чаем в редакторе.
func (s *Scheduler) game(in Inputs) (Event, bool) {
	if !in.HasFocus || in.Focus.Kind != focus.KindGame {
		return Event{}, false
	}
	since := in.Now.Sub(in.Focus.Since)
	if since < GameMarathon {
		return Event{}, false
	}
	if !s.lastGameNag.IsZero() && !s.lastGameNag.Before(in.Focus.Since) {
		return Event{}, false // про этот заход в игру она уже подделa
	}
	if !present(in) {
		return Event{}, false
	}
	half := int(since.Minutes() / 60)
	return Event{Kind: KindGame, Reason: fmt.Sprintf(d(in).GameReason, in.Focus.Label, half)}, true
}

// app — два часа в редакторе кода. Одна реплика на заход в приложение
// (счёт Since — от последней смены приложения).
func (s *Scheduler) app(in Inputs) (Event, bool) {
	if !in.HasFocus || in.Focus.Kind != focus.KindCode {
		return Event{}, false
	}
	if in.Now.Sub(in.Focus.Since) < AppMarathonMin {
		return Event{}, false
	}
	if !s.lastAppNag.IsZero() && !s.lastAppNag.Before(in.Focus.Since) {
		return Event{}, false // про этот заход в приложение она уже сказала
	}
	if !present(in) {
		return Event{}, false
	}
	hours := int(in.Now.Sub(in.Focus.Since).Hours())
	return Event{Kind: KindApp, Reason: fmt.Sprintf(d(in).AppReason, in.Focus.Label, hours)}, true
}

// editorFact — факт про редактор для ритуальных поводов (этап 9г): если активное
// окно правда «код», повод получает это как факт. Реплику по факту строит модель
// — планировщик только передаёт, что видит.
func editorFact(in Inputs) string {
	if in.HasFocus && in.Focus.Kind == focus.KindCode {
		return fmt.Sprintf(d(in).EditorFact, in.Focus.Label)
	}
	return ""
}

// morning / evening — смена части суток. Реакция на СМЕНУ, а не на факт:
// заход сервера утром не должен выглядеть как «она заметила утро». Ритуал
// (этап 9г) несёт время суток и факт про редактор — реплика короткая, по факту.
func (s *Scheduler) morning(in Inputs) (Event, bool) {
	if s.lastDayPart == persona.Morning || persona.PartOf(in.Now) != persona.Morning {
		return Event{}, false
	}
	if !present(in) {
		return Event{}, false
	}
	reason := fmt.Sprintf(d(in).MorningReason, in.Now.Format("15:04"))
	if fact := editorFact(in); fact != "" {
		reason += " " + fact + " " + d(in).MorningFact
	}
	return Event{Kind: KindMorning, Reason: reason}, true
}

func (s *Scheduler) evening(in Inputs) (Event, bool) {
	if s.lastDayPart == persona.Evening || persona.PartOf(in.Now) != persona.Evening {
		return Event{}, false
	}
	if !present(in) {
		return Event{}, false
	}
	reason := fmt.Sprintf(d(in).EveningReason, in.Now.Format("15:04"))
	if fact := editorFact(in); fact != "" {
		reason += " " + fact + " " + d(in).EveningFact
	}
	return Event{Kind: KindEvening, Reason: reason}, true
}

// silence — он рядом, но молчит. Самый мягкий триггер: и молчание занятого
// человека священно (занятым молчит), и дистанция у него самая большая.
func (s *Scheduler) silence(in Inputs) (Event, bool) {
	if busy(in) {
		return Event{}, false
	}
	if !present(in) {
		return Event{}, false
	}
	last := in.LastSay
	if last.IsZero() {
		last = s.started
	}
	if in.Now.Sub(last) < SilenceMin {
		return Event{}, false
	}
	minutes := int(in.Now.Sub(last).Minutes())
	return Event{Kind: KindSilence, Reason: fmt.Sprintf(d(in).SilenceReason, minutes)}, true
}

// MarkSent отмечает ОТПРАВЛЕННУЮ реплику: она обнуляет отсчёт анти-спама,
// а для «иди спать» и «чая в редакторе» помнит ещё и факт по ночи/заходу.
// now передаётся параметром, а не берётся из time.Now: тесты на фейковых
// часах обязаны ставить ровно те же часы, что передали в Tick.
func (s *Scheduler) MarkSent(kind Kind, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastProactive = now
	s.lastAttempt = now
	switch kind {
	case KindSleep:
		s.lastSleep = now
	case KindNight:
		s.lastNight = now
	case KindCare:
		s.lastCare = now
	case KindApp:
		s.lastAppNag = now
	case KindGame:
		s.lastGameNag = now
	case KindMorning, KindEvening:
		s.lastDayPart = persona.PartOf(now)
	}
}

// MarkAttempt отмечает ПОПЫТКУ (успешную или провалившуюся): провал модели не
// должен превращать каждый тик в новый запрос к ней.
func (s *Scheduler) MarkAttempt(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAttempt = now
}

// NoteDayPart вызывается вместе с mark-отметками частей суток (см. MarkSent).
// Отдельно нужен для «отметить смену, не отправляя» — когда реплику съел
// анти-спам: утро всё равно началось, и «утро, привет» не должно вспылить
// через полчаса.
func (s *Scheduler) NoteDayPart(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastDayPart = persona.PartOf(now)
}
