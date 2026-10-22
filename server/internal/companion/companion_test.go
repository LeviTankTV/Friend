package companion

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"friend/internal/chat"
	"friend/internal/llm"
	"friend/internal/memory"
	"friend/internal/mood"
	"friend/internal/persona"
)

// fakeModel — поддельная модель: запоминает, ЧТО у неё спросили, и отвечает
// заранее известным. Живой NIM в тестах не годится: он медленный, платный и
// требует ключа, а проверяем мы сборку запроса, а не чужой сервис.
type fakeModel struct {
	ready  bool
	answer string
	err    error
	got    [][]llm.Message
	calls  int
	delay  time.Duration
}

func (f *fakeModel) Chat(ctx context.Context, msgs []llm.Message) (string, error) {
	f.calls++
	f.got = append(f.got, append([]llm.Message(nil), msgs...))
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if f.err != nil {
		return "", f.err
	}
	return f.answer, nil
}

func (f *fakeModel) Ready() bool            { return f.ready }
func (f *fakeModel) Status() string         { return "поддельная модель, ключ есть" }
func (f *fakeModel) Timeout() time.Duration { return time.Second }

func testProfile(t *testing.T) persona.Profile {
	t.Helper()
	p, err := persona.Default()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Запрос к модели: первым идёт системный промпт личности, дальше разговор,
// последней — только что сказанная человеком реплика.
func TestAnswerBuildsRequest(t *testing.T) {
	m := &fakeModel{ready: true, answer: "Привет. Я тебя с утра не видела."}
	c := New(testProfile(t), m, chat.New(10))

	got, err := c.Answer(context.Background(), "привет", "")
	if err != nil {
		t.Fatalf("Answer вернул ошибку: %v", err)
	}
	if got != m.answer {
		t.Errorf("ответ %q, ожидался %q", got, m.answer)
	}
	if len(m.got) != 1 {
		t.Fatalf("к модели ушло %d запросов, ожидался 1", len(m.got))
	}
	msgs := m.got[0]
	if len(msgs) != 2 {
		t.Fatalf("в запросе %d сообщений, ожидалось 2 (system + реплика): %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Errorf("первое сообщение роли %q, ожидалась system", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "Мира") {
		t.Errorf("в системном промпте нет характера: %q", msgs[0].Content)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "привет" {
		t.Errorf("второе сообщение %+v — реплика человека должна быть в ЭТОМ же запросе", msgs[1])
	}
}

// Контекст разговора: предыдущие реплики уходят в следующий запрос, а история
// сервера — та же, что видел человек.
func TestHistoryGoesToModel(t *testing.T) {
	m := &fakeModel{ready: true, answer: "первый ответ"}
	c := New(testProfile(t), m, chat.New(10))

	if _, err := c.Answer(context.Background(), "как дела?", ""); err != nil {
		t.Fatal(err)
	}
	m.answer = "второй ответ"
	if _, err := c.Answer(context.Background(), "а ты?", ""); err != nil {
		t.Fatal(err)
	}

	second := m.got[1]
	if len(second) != 4 { // system + 2 пары
		t.Fatalf("во втором запросе %d сообщений, ожидалось 4: %+v", len(second), second)
	}
	if second[1].Content != "как дела?" || second[2].Content != "первый ответ" || second[3].Content != "а ты?" {
		t.Errorf("порядок истории сломан: %+v", second)
	}

	hist := c.History()
	if len(hist) != 4 {
		t.Fatalf("в истории компаньона %d реплик, ожидалось 4", len(hist))
	}
	if hist[0].Role != chat.RoleUser || hist[1].Role != chat.RoleAssistant {
		t.Errorf("роли в истории перепутаны: %+v", hist)
	}
}

// Без ключа запрос НЕ уходит, а человек получает понятный текст с именем
// переменной окружения: молчание или «думает» на минуту — худший вариант.
func TestWithoutKeyNoRequest(t *testing.T) {
	m := &fakeModel{ready: false}
	c := New(testProfile(t), m, chat.New(10))

	_, err := c.Answer(context.Background(), "привет", "")
	if !errors.Is(err, llm.ErrNoKey) {
		t.Fatalf("ожидалась ErrNoKey, получено %v", err)
	}
	if m.calls != 0 {
		t.Errorf("без ключа ушло %d запросов к модели, ожидалось 0", m.calls)
	}
	if c.Ready() {
		t.Error("Ready() = true без ключа")
	}
	if len(c.History()) != 0 {
		t.Errorf("реплика человека осталась в истории без ответа: %+v", c.History())
	}
}

// Сбой модели: ошибка наружу, но «молчание модели» в историю не пишется —
// иначе в следующий раз она увидит свою пустую реплику.
func TestFailureDoesNotAddAssistantTurn(t *testing.T) {
	m := &fakeModel{ready: true, err: errors.New("NIM недоступен")}
	c := New(testProfile(t), m, chat.New(10))

	if _, err := c.Answer(context.Background(), "ты тут?", ""); err == nil {
		t.Fatal("ошибка модели не дошла до вызывающей стороны")
	}
	hist := c.History()
	if len(hist) != 1 {
		t.Fatalf("в истории %d реплик, ожидалась 1 (только сказанное человеком): %+v", len(hist), hist)
	}
	if hist[0].Role != chat.RoleUser {
		t.Errorf("в истории оказался ответ ассистента: %+v", hist[0])
	}
}

// Пустая реплика: ни запроса, ни записи в историю.
func TestEmptyTextIsRejected(t *testing.T) {
	m := &fakeModel{ready: true, answer: "не важно"}
	c := New(testProfile(t), m, chat.New(10))

	if _, err := c.Answer(context.Background(), "   \n  ", ""); err == nil {
		t.Fatal("пустая реплика не дала ошибки")
	}
	if m.calls != 0 || len(c.History()) != 0 {
		t.Errorf("пустая реплика дошла до модели (calls=%d, история=%+v)", m.calls, c.History())
	}
}

// Ответ из одних пробелов — тоже «нечего сказать»: пустое облачко в окне
// выглядит как сломанный компаньон.
func TestWhitespaceAnswerIsEmptyReply(t *testing.T) {
	m := &fakeModel{ready: true, answer: "   "}
	c := New(testProfile(t), m, chat.New(10))

	_, err := c.Answer(context.Background(), "привет", "")
	if !errors.Is(err, llm.ErrEmptyReply) {
		t.Fatalf("ожидалась ErrEmptyReply, получено %v", err)
	}
}

// Реплика человека обрезается по краям: переносы в конце строки превратили бы
// историю в «она отвечает на пустую строку».
func TestTrimmedTextGoesToHistory(t *testing.T) {
	m := &fakeModel{ready: true, answer: "ага"}
	c := New(testProfile(t), m, chat.New(10))

	if _, err := c.Answer(context.Background(), "\n  привет  \n", ""); err != nil {
		t.Fatal(err)
	}
	if c.History()[0].Text != "привет" {
		t.Errorf("в истории %q, ожидалось «привет»", c.History()[0].Text)
	}
}

// Remember — для инициативы (этап 4): сказанное персонажем первым тоже должно
// попадать в контекст.
func TestRememberAddsAssistantTurn(t *testing.T) {
	m := &fakeModel{ready: true, answer: "ага"}
	c := New(testProfile(t), m, chat.New(10))

	c.Remember("Ты давно не вставал.")
	if _, err := c.Answer(context.Background(), "знаю", ""); err != nil {
		t.Fatal(err)
	}
	msgs := m.got[0]
	if len(msgs) != 3 || msgs[1].Content != "Ты давно не вставал." {
		t.Fatalf("реплика, сказанная первой, не попала в контекст: %+v", msgs)
	}
}

// Дедлайн контекста доходит до модели: без этого окно осталось бы с индикатором
// «думает» навсегда, если API молчит.
func TestContextDeadlineReachesModel(t *testing.T) {
	m := &fakeModel{ready: true, answer: "поздно", delay: 2 * time.Second}
	c := New(testProfile(t), m, chat.New(10))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Answer(ctx, "привет", ""); err == nil {
		t.Fatal("молчащая модель не дала ошибки")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("ожидание заняло %v — дедлайн не подействовал", elapsed)
	}
}

// Обстановка (активное окно человека) доходит до системного промпта и стоит
// после личности и времени суток — как самое быстроменяющееся.
func TestContextGoesToSystemPrompt(t *testing.T) {
	m := &fakeModel{ready: true, answer: "Поняла, не мешаю."}
	c := New(testProfile(t), m, chat.New(10))
	c.SetContext(func() string {
		return "Он сейчас в GoLand (IDE для Go), уже 2 ч 10 мин."
	})
	if _, err := c.Answer(context.Background(), "тихий вечер", ""); err != nil {
		t.Fatal(err)
	}
	system := m.got[0][0].Content
	if !strings.Contains(system, "Обстановка сейчас: Он сейчас в GoLand") {
		t.Fatalf("строка обстановки не попала в промпт:\n%s", system)
	}
	if strings.Index(system, "Обстановка") < strings.Index(system, "Сейчас ") {
		t.Fatal("обстановка обязана идти после времени суток")
	}
}

// Без обстановки (focuswatch нет, трекер пуст) промпт обязан собираться чисто:
// пустой строке негде превратиться в «Обстановка сейчас: ».
func TestNoContextNoMarker(t *testing.T) {
	m := &fakeModel{ready: true, answer: "Привет."}
	c := New(testProfile(t), m, chat.New(10))
	if _, err := c.Answer(context.Background(), "привет", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.got[0][0].Content, "Обстановка") {
		t.Fatalf("пустая обстановка оставила след в промпте:\n%s", m.got[0][0].Content)
	}
	c.SetContext(func() string { return "" })
	m.got = nil
	if _, err := c.Answer(context.Background(), "ещё раз", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.got[0][0].Content, "Обстановка") {
		t.Fatal("пустая строка от поставщика не должна попадать в промпт")
	}
}

// Speak: повод доходит до модели, в историю попадает ТОЛЬКО её реплика —
// иначе следующий ответ модель прочитает «повод» как слова человека.
func TestSpeakKeepsReasonOutOfHistory(t *testing.T) {
	m := &fakeModel{ready: true, answer: "Ты чего заснул на клавиатуре? Иди спать."}
	c := New(testProfile(t), m, chat.New(10))
	answer, err := c.Speak(context.Background(), "Сейчас 3:20 ночи, он всё ещё не спит.")
	if err != nil {
		t.Fatal(err)
	}
	if answer == "" {
		t.Fatal("реплики нет")
	}
	// Последний кадр запроса — установка «заговори первой» с поводом.
	last := m.got[0][len(m.got[0])-1]
	if last.Role != "user" || !strings.Contains(last.Content, "3:20 ночи") {
		t.Fatalf("повод не дошёл до модели как установка: %+v", last)
	}
	// А в истории повода быть не должно — только её реплика.
	hist := c.History()
	if len(hist) != 1 {
		t.Fatalf("в истории должна быть только её реплика, получили %d записей", len(hist))
	}
	if hist[0].Role != chat.RoleAssistant {
		t.Fatalf("в историю ушло не её слово: %+v", hist[0])
	}
}

func TestSpeakEmptyReasonRejected(t *testing.T) {
	m := &fakeModel{ready: true, answer: "что-то"}
	c := New(testProfile(t), m, chat.New(10))
	if _, err := c.Speak(context.Background(), "   "); !errors.Is(err, ErrEmptyReply) {
		t.Fatalf("пустой повод должен отклоняться до запроса, получили %v", err)
	}
	if m.calls != 0 {
		t.Fatal("на пустом поводе модель дёргать нельзя")
	}
}

func TestSpeakWithoutKeyNoRequest(t *testing.T) {
	m := &fakeModel{ready: false}
	c := New(testProfile(t), m, chat.New(10))
	if _, err := c.Speak(context.Background(), "он пропал на час"); !errors.Is(err, llm.ErrNoKey) {
		t.Fatalf("без ключа Speak обязан вернуть ErrNoKey, получили %v", err)
	}
	if m.calls != 0 {
		t.Fatal("без ключа запрос к модели идти не должен")
	}
}

// fakeMemory — поддельная долгая память: помнит, что просили вспомнить, и
// что архивировали.
type fakeMemory struct {
	facts      []memory.Fact
	summaries  []memory.Summary
	recallQ    []string
	archived   []chat.Message
	recallErr  error
	summaryErr error
}

func (f *fakeMemory) Recall(query string, limit int) ([]memory.Fact, error) {
	f.recallQ = append(f.recallQ, query)
	return f.facts, f.recallErr
}

func (f *fakeMemory) Summaries(n int) ([]memory.Summary, error) {
	return f.summaries, f.summaryErr
}

func (f *fakeMemory) Archive(role, text string, at time.Time) error {
	f.archived = append(f.archived, chat.Message{Role: role, Text: text, At: at})
	return nil
}

// Долгая память в промпте: факт, подобранный по последней реплике человека,
// попадает в системный промпт с маркером и с просьбой не пересказывать.
func TestMemoryFactsGoToSystemPrompt(t *testing.T) {
	m := &fakeModel{ready: true, answer: "Как отец? Поправилось?"}
	mem := &fakeMemory{
		facts:     []memory.Fact{{ID: 1, Text: "отец болел, жаловался в пятницу"}},
		summaries: []memory.Summary{{ID: 1, Text: "Тим кодил Шейпер допоздна"}},
	}
	c := New(testProfile(t), m, chat.New(10))
	c.SetMemory(mem)
	if _, err := c.Answer(context.Background(), "как там отец?", ""); err != nil {
		t.Fatal(err)
	}
	system := m.got[0][0].Content
	if !strings.Contains(system, "отец болел") {
		t.Fatalf("факт не попал в промпт:\n%s", system)
	}
	if !strings.Contains(system, "саммари") {
		t.Fatalf("саммари не попало в промпт:\n%s", system)
	}
	if len(mem.recallQ) != 1 || !strings.Contains(mem.recallQ[0], "отец") {
		t.Fatalf("память обязана искаться по последней реплике человека: %v", mem.recallQ)
	}
	// В историю факты не уходят — только реплики.
	for _, h := range c.History() {
		if strings.Contains(h.Text, "отец болел") {
			t.Fatal("факт полез в историю разговора")
		}
	}
}

// Реплики человека и её ответы пишутся в архив — из него потом саммари и
// восстановление истории после перезапуска.
func TestAnswerArchivesBothTurns(t *testing.T) {
	m := &fakeModel{ready: true, answer: "Приветик"}
	mem := &fakeMemory{}
	c := New(testProfile(t), m, chat.New(10))
	c.SetMemory(mem)
	if _, err := c.Answer(context.Background(), "привет", ""); err != nil {
		t.Fatal(err)
	}
	if len(mem.archived) != 2 || mem.archived[0].Role != chat.RoleUser ||
		mem.archived[1].Role != chat.RoleAssistant {
		t.Fatalf("архив обязан содержать обе реплики: %+v", mem.archived)
	}
}

func TestSpeakArchivesAnswer(t *testing.T) {
	m := &fakeModel{ready: true, answer: "Уже полночь, а ты всё в коде."}
	mem := &fakeMemory{}
	c := New(testProfile(t), m, chat.New(10))
	c.SetMemory(mem)
	if _, err := c.Speak(context.Background(), "он в редакторе четыре часа"); err != nil {
		t.Fatal(err)
	}
	if len(mem.archived) != 1 || mem.archived[0].Role != chat.RoleAssistant {
		t.Fatalf("проактивная реплика обязана уйти в архив: %+v", mem.archived)
	}
}

// Без памяти компаньон работает молча (файл не открылся — не повод ломать
// разговор), с памятью — работает. Ошибка памяти не должна стоить ответа.
func TestMemoryFailureDoesNotBreakAnswer(t *testing.T) {
	m := &fakeModel{ready: true, answer: "хорошо"}
	c := New(testProfile(t), m, chat.New(10))
	if _, err := c.Answer(context.Background(), "привет", ""); err != nil {
		t.Fatal(err)
	} // без SetMemory — чисто
	c2 := New(testProfile(t), m, chat.New(10))
	c2.SetMemory(&fakeMemory{recallErr: errors.New("файл занят")})
	if _, err := c2.Answer(context.Background(), "привет", ""); err != nil {
		t.Fatalf("ошибка памяти не должна ломать ответ: %v", err)
	}
	if strings.Contains(m.got[len(m.got)-1][0].Content, "Что ты помнишь") {
		t.Fatal("при ошибке Recall в промпт нечего вставлять")
	}
}

// Summarize: сжатие идёт отдельным запросом, в историю и архив не попадает.
func TestSummarizeKeepsHistoryClean(t *testing.T) {
	m := &fakeModel{ready: true, answer: "Тим весь вечер кодил и ругался со мной."}
	mem := &fakeMemory{}
	c := New(testProfile(t), m, chat.New(10))
	c.SetMemory(mem)
	out, err := c.Summarize(context.Background(), "он: привет\nмира: привет\nон: код не идёт")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("саммари пустое")
	}
	if len(c.History()) != 0 {
		t.Fatal("саммари не должно появляться в истории")
	}
	if len(mem.archived) != 0 {
		t.Fatal("саммари не архив реплик")
	}
	// Запрос — установка сжечь с текстом разговора.
	last := m.got[0][len(m.got[0])-1]
	if !strings.Contains(last.Content, "Сожми") || !strings.Contains(last.Content, "код не идёт") {
		t.Fatalf("установка сжатия не дошла: %+v", last)
	}
}

func TestSummarizeEmptyRejected(t *testing.T) {
	m := &fakeModel{ready: true, answer: "что-то"}
	c := New(testProfile(t), m, chat.New(10))
	if _, err := c.Summarize(context.Background(), "   "); !errors.Is(err, ErrEmptyReply) {
		t.Fatalf("пустой разговор отклоняется до запроса: %v", err)
	}
	if m.calls != 0 {
		t.Fatal("на пустом разговоре модель дёргать нельзя")
	}
}

// TestMoodLineInSystemPrompt — внутреннее состояние (этап 5) одной строкой
// попадает в системный промпт.
func TestMoodLineInSystemPrompt(t *testing.T) {
	m := &fakeModel{ready: true, answer: "ага"}
	c := New(testProfile(t), m, chat.New(10))
	c.SetMoodSource(func() mood.State {
		return mood.State{Mood: 0.85, Energy: 0.2, Affection: 0.9}
	})
	if _, err := c.Answer(context.Background(), "привет", ""); err != nil {
		t.Fatal(err)
	}
	sys := m.got[len(m.got)-1][0].Content
	for _, want := range []string{"Твоё внутреннее состояние", "приподнятое", "усталость", "Не пересказывай"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("в системном промпте нет %q:\n%s", want, sys)
		}
	}
	// Состояние — в промпте, а не отдельным сообщением истории.
	for _, msg := range m.got[len(m.got)-1][1:] {
		if strings.Contains(msg.Content, "внутреннее состояние") {
			t.Fatal("строка состояния не должна попадать в историю")
		}
	}
}

// TestMoodLineInSpeakPrompt — проактивная реплика тоже собирается со
// строкой состояния: тон её собственной реплики решает то же настроение.
func TestMoodLineInSpeakPrompt(t *testing.T) {
	m := &fakeModel{ready: true, answer: "зеваю"}
	c := New(testProfile(t), m, chat.New(10))
	c.SetMoodSource(func() mood.State { return mood.State{Mood: 0.3, Energy: 0.25} })
	if _, err := c.Speak(context.Background(), "он пропал на два часа"); err != nil {
		t.Fatal(err)
	}
	sys := m.got[len(m.got)-1][0].Content
	for _, want := range []string{"Твоё внутреннее состояние", "упавшее", "усталость"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("в промпте Speak нет %q:\n%s", want, sys)
		}
	}
}

// TestNoMoodSourceNoLine — без источника состояния промпт не содержит
// строки состояния (сборка без цикла — норма).
func TestNoMoodSourceNoLine(t *testing.T) {
	m := &fakeModel{ready: true, answer: "ага"}
	c := New(testProfile(t), m, chat.New(10))
	if _, err := c.Answer(context.Background(), "привет", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.got[len(m.got)-1][0].Content, "внутреннее состояние") {
		t.Fatal("без источника состояния строки быть не должно")
	}
}
