// Package companion — сам компаньон: личность + модель + история.
//
// Отдельный пакет нужен, чтобы «как отвечать» не расползалось между сервером
// (WebSocket) и клиентом API. Сервер знает только «пришла реплика — верни
// ответ»; как собирается промпт, что помним и что делаем при сбое модели —
// решается здесь.
//
// Компаньон — не ассистент: он отвечает от лица персонажа (persona.Profile), а
// время суток влияет на тон, поэтому системный промпт пересобирается на каждую
// реплику, а не собирается один раз при запуске.
package companion

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"friend/internal/chat"
	"friend/internal/i18n"
	"friend/internal/llm"
	"friend/internal/memory"
	"friend/internal/mood"
	"friend/internal/moodread"
	"friend/internal/persona"
)

// Model — то, что умеет отвечать. В рабочем запуске это клиент NVIDIA NIM, в
// тестах — подделка: проверять сборку промпта и порядок истории на живом API
// нельзя (он медленный, платный и требует ключа).
type Model interface {
	Chat(ctx context.Context, msgs []llm.Message) (string, error)
	Ready() bool
	Status() string
	Timeout() time.Duration
}

// Companion отвечает на реплики человека.
type Companion struct {
	profile persona.Profile
	model   Model
	sess    *chat.Session
	now     func() time.Time
	// context — источник «обстановки на момент ответа»: строки про текущее
	// приложение человека и другие живые обстоятельства (сон, еда — этап 4).
	// Ставится извне (main.go собирает из трекеров), промпт пересобирается на
	// каждую реплику, поэтому обстановка всегда свежая.
	context func() string
	// memory — долгая память (этап 3): факты и саммари из SQLite. Nil —
	// компаньон живёт без долгой памяти (сборка без файла, тесты).
	memory Memory
	// mood — источник внутреннего состояния (этап 5): строка настроения/
	// энергии/привязанности в системный промпт. Nil — состояние не участвует
	// в промпте (тесты, сборка без цикла состояния).
	mood func() mood.State
	// userState — ярлык тона реплик человека на время ОДНОГО ответа
	// (этап 9в): «устал», «расстроился», «рад», «злится», «тревожится».
	// Краткий, живёт одну реплику — поэтому мьютекс не нужен (Answer
	// сериализован chatLoop'ом хаба), но поле отделено от истории.
	userState string
}

// Memory — долгая память (internal/memory). Компаньону нужны три операции:
// вспомнить факты по текущей реплике, достать последние саммари и записать
// сказанное в архив.
type Memory interface {
	Recall(query string, limit int) ([]memory.Fact, error)
	Summaries(n int) ([]memory.Summary, error)
	Archive(role, text string, at time.Time) error
}

// New собирает компаньона. sess == nil — заводим свою историю.
func New(p persona.Profile, m Model, sess *chat.Session) *Companion {
	if sess == nil {
		sess = chat.New(chat.DefaultMax)
	}
	return &Companion{profile: p, model: m, sess: sess, now: time.Now}
}

// SetContext задаёт источник обстановки (func() — строка для промпта; пустая
// строка приравнивается к «обстановки нет»). Nil — обстановка выключена:
// так работает сборка без focuswatch (Linux/macOS) и любые тесты без трекеров.
func (c *Companion) SetContext(fn func() string) { c.context = fn }

// SetMemory подключает долгую память (internal/memory): факты и саммари в
// системный промпт, реплики — в архив. Nil — компаньон живёт без долгой
// памяти: так работает сборка, где файл памяти не открылся (битый файл,
// права) — разговор при этом полностью работает.
func (c *Companion) SetMemory(m Memory) { c.memory = m }

// SetMoodSource подключает источник внутреннего состояния (этап 5):
// настроение/энергия/привязанность одной строкой в системный промпт.
// Nil — состояние не участвует в промпте.
func (c *Companion) SetMoodSource(fn func() mood.State) { c.mood = fn }

// rememberInstruction — как просить модель сжать разговор в саммари (этап 3).
// Просьба намеренно про человека и его жизнь: так из разговора остаётся то,
// что пригодится в следующий раз, а не её собственные реплики. Текст — в
// словаре (i18n.SummarizeInstruction), язык следует за персоной.

// Summarize сжимает переданный текст разговора в саммари (заметку о человеке).
// В историю и архив НЕ попадает: саммари — отдельный слой памяти, его пишет
// вызвавший (проактивный цикл, internal/proactive+main.go).
func (c *Companion) Summarize(ctx context.Context, convo string) (string, error) {
	convo = strings.TrimSpace(convo)
	if convo == "" {
		return "", ErrEmptyReply
	}
	if !c.model.Ready() {
		return "", fmt.Errorf("%w — %s", llm.ErrNoKey, c.model.Status())
	}
	prompt := c.profile.SystemPrompt(c.now())
	msgs := []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: fmt.Sprintf(c.dict().SummarizeInstruction, convo)},
	}
	out, err := c.model.Chat(ctx, msgs)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ErrEmptyReply — писать нечего: пустую реплику и отправлять незачем (деньги и
// время), и помнить о ней нечего.
var ErrEmptyReply = errors.New("нечего отвечать: реплика пустая")

// speakInstruction — как модель должна ответить на «повод» (см. Speak).
//
// Текст нарочно не оставляет модели выбора формата «ответить человеку»:
// иначе на проактивный повод она отвечает как на реплику («Чем помочь?»).
// Строка — в словаре (i18n.SpeakInstruction), язык следует за персоной.

// Speak — её собственная реплика по поводу (этап 4, инициатива).
//
// От Answer отличается порядком памяти: повод — это не слова человека, и в
// историю он не попадает (иначе следующий ответ модель читала бы как «он
// сказал»). В историю попадает только её собственная реплика, и следующий
// ответ уже знает, что она заговорила первой.
func (c *Companion) Speak(ctx context.Context, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", ErrEmptyReply
	}
	if !c.model.Ready() {
		return "", fmt.Errorf("%w — %s", llm.ErrNoKey, c.model.Status())
	}
	// Долгая память по проактивному поводу та же, что и по реплике
	// человека: она в системном промпте, её нет в истории.
	msgs := c.memoryMessages(reason)
	msgs = append(msgs, llm.Message{Role: "user", Content: fmt.Sprintf(c.dict().SpeakInstruction, reason)})
	answer, err := c.model.Chat(ctx, msgs)
	if err != nil {
		return "", err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", llm.ErrEmptyReply
	}
	c.sess.Add(chat.RoleAssistant, answer)
	c.archive(chat.RoleAssistant, answer)
	return answer, nil
}

// Answer отвечает на реплику человека.
//
// Порядок шагов здесь не случаен:
//   - реплика человека попадает в историю ДО запроса, иначе её не будет в этом
//     же запросе и модель ответит на предыдущую;
//   - ответ модели добавляется в историю только при успехе: при сбое помнить
//     «её молчание» нечего, а в следующий раз в историю уйдёт только сказанное
//     человеком — это честнее, чем пустая реплика ассистента;
//   - ключ проверяется здесь же, до запроса: без ключа не должно быть ни
//     «думает» на минуту, ни лишней задержки — только понятный текст.
//
// userState (этап 9в) — ярлык тона реплик человека («устал», «расстроился»,
// «рад», «злится», «тревожится», "" — нейтрально): в историю НЕ попадает
// (это не его слова), живёт одной репликой: в промпт идёт «он сейчас: …».
func (c *Companion) Answer(ctx context.Context, text, userState string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ErrEmptyReply
	}
	if !c.model.Ready() {
		return "", fmt.Errorf("%w — %s", llm.ErrNoKey, c.model.Status())
	}

	c.sess.Add(chat.RoleUser, text)
	c.archive(chat.RoleUser, text)
	c.userState = strings.TrimSpace(userState)
	answer, err := c.model.Chat(ctx, c.messages())
	c.userState = ""
	if err != nil {
		return "", err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", llm.ErrEmptyReply
	}
	c.sess.Add(chat.RoleAssistant, answer)
	c.archive(chat.RoleAssistant, answer)
	return answer, nil
}

// messages собирает запрос к модели: системный промпт (личность + время суток
// + обстановка + долгая память) и история разговора.
//
// Обстановка идёт ПОСЛЕ личности и времени: она самая быстроменяющаяся (окно
// сменилось — и следующий ответ уже про другое), и в конце промпта модель
// держит её лучше, чем в середине.
//
// Память — строкой между обстановкой и историей: «что о нём помнить»
// меняется медленнее обстановки, и формулировка обязана быть такой, чтобы
// модель не пересказывала память человеку.
func (c *Companion) messages() []llm.Message {
	return c.messagesWithQuery(lastUserText(c.sess.Messages()))
}

// memoryMessages — то же, что messages, но долгая память ищется по
// проактивному поводу (Speak): своего текста человека там нет, а обстановка
// («он в редакторе 2 ч») — есть.
func (c *Companion) memoryMessages(query string) []llm.Message {
	return c.messagesWithQuery(query)
}

// messagesWithQuery — сборка запроса; query — текст, по которому ищем память
// (пустой — память не ищем).
func (c *Companion) messagesWithQuery(query string) []llm.Message {
	history := c.sess.Messages()
	d := c.dict()
	prompt := c.profile.SystemPrompt(c.now())
	if c.context != nil {
		if ctx := strings.TrimSpace(c.context()); ctx != "" {
			prompt += d.CtxHeader + ctx
		}
	}
	if c.mood != nil {
		if line := mood.Describe(c.mood(), c.profile.Language); line != "" {
			prompt += "\n\n" + line
		}
	}
	// Эмпатия (этап 9в): код тона реплик человека (moodread) — одна строка
	// промпта, слово на языке персоны. Формулировка обязана быть такой,
	// чтобы она ЧУВСТВОВАЛА состояние, а не допрашивала: «он устал» ≠
	// «спроси, почему он устал».
	if s := strings.TrimSpace(c.userState); s != "" {
		prompt += fmt.Sprintf(d.UserStateLine, usWord(d, s))
	}
	if query != "" {
		prompt += c.memorySection(query)
	}
	msgs := make([]llm.Message, 0, len(history)+1)
	msgs = append(msgs, llm.Message{Role: "system", Content: prompt})
	for _, m := range history {
		msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Text})
	}
	return msgs
}

// dict — словарь языка персоны.
func (c *Companion) dict() *i18n.Dict { return i18n.For(c.profile.Language) }

// usWord — код тона (moodread.Tired…) в слово промпта на языке словаря.
func usWord(d *i18n.Dict, code string) string {
	switch code {
	case moodread.Tired:
		return d.USTired
	case moodread.Sad:
		return d.USSad
	case moodread.Happy:
		return d.USHappy
	case moodread.Angry:
		return d.USAngry
	case moodread.Anxious:
		return d.USAnxious
	}
	return code
}

// memorySection — кусок промпта с долгой памятью: факты, подходящие к
// текущей реплике, и последние саммари. Ошибка памяти не должна стоить
// ответа: промпт просто собирается без этого куска.
func (c *Companion) memorySection(query string) string {
	if c.memory == nil {
		return ""
	}
	d := c.dict()
	out := ""
	if facts, err := c.memory.Recall(query, 5); err == nil && len(facts) > 0 {
		out += d.MemFactsHead
		for _, f := range facts {
			out += "\n— " + f.Text
		}
	}
	if sum, err := c.memory.Summaries(1); err == nil && len(sum) > 0 {
		out += fmt.Sprintf(d.MemSummary, sum[0].Text)
	}
	if out == "" {
		return ""
	}
	return out + d.MemTail
}

// lastUserText — последняя реплика человека в истории (по ней ищем память).
func lastUserText(history []chat.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == chat.RoleUser {
			return history[i].Text
		}
	}
	return ""
}

// archive — записать реплику в долгую память. Без памяти — простой вызов.
// Ошибка архива не видна говорящему: разговор не должен падать из-за файла.
func (c *Companion) archive(role, text string) {
	if c.memory == nil {
		return
	}
	_ = c.memory.Archive(role, text, c.now())
}

// History — история разговора (копия). Нужна окну: после перезапуска оболочки
// разговор не должен выглядеть начатым с чистого листа, пока жив сервер.
func (c *Companion) History() []chat.Message { return c.sess.Messages() }

// Remember добавляет в историю реплику персонажа, сказанную НЕ в ответ на
// реплику человека. Это понадобится инициативе (этап 4): она заговорила первой
// — и это должно быть в контексте следующего ответа.
func (c *Companion) Remember(text string) {
	if t := strings.TrimSpace(text); t != "" {
		c.sess.Add(chat.RoleAssistant, t)
		c.archive(chat.RoleAssistant, t)
	}
}

// Ready — можно ли сейчас получить ответ (есть ли ключ).
func (c *Companion) Ready() bool { return c.model.Ready() }

// Status — человекочитаемое состояние: модель и есть ли ключ. Ключа здесь нет
// и быть не может.
func (c *Companion) Status() string { return c.model.Status() }

// Timeout — сколько максимум ждём модель. По этому времени сервер ставит
// дедлайн на всю операцию, поэтому окно не останется с индикатором «думает»
// навсегда.
func (c *Companion) Timeout() time.Duration { return c.model.Timeout() }

// Name — имя персонажа (для логов и окна).
func (c *Companion) Name() string { return c.profile.Name }
