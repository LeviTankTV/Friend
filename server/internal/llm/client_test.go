package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"friend/internal/config"
)

// Тестовый стенд: поддельный NIM, который запоминает, ЧТО ему прислали.
type fakeNIM struct {
	srv      *httptest.Server
	gotPath  string
	gotAuth  string
	gotBody  map[string]any
	answer   string
	status   int
	rawBody  string
	delay    time.Duration
	requests int
}

func newFakeNIM(t *testing.T) *fakeNIM {
	t.Helper()
	f := &fakeNIM{
		status: http.StatusOK,
		answer: "Привет, я тут.",
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests++
		f.gotPath = r.URL.Path
		f.gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &f.gotBody)
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		if f.rawBody != "" {
			_, _ = w.Write([]byte(f.rawBody))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": f.answer}}},
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func testCfg(f *fakeNIM) config.LLM {
	return config.LLM{
		BaseURL:     f.srv.URL + "/v1/", // со слэшем на конце — как бывает у людей в конфиге
		Model:       "test/model",
		APIKeyEnv:   "FRIEND_TEST_KEY",
		MaxTokens:   111,
		Temperature: 0.5,
		TimeoutSec:  5,
	}
}

func TestChatSendsExpectedRequest(t *testing.T) {
	f := newFakeNIM(t)
	t.Setenv("FRIEND_TEST_KEY", "секретный-ключ")
	c := New(testCfg(f))

	got, err := c.Chat(context.Background(), []Message{
		{Role: "system", Content: "ты Мира"},
		{Role: "user", Content: "привет"},
	})
	if err != nil {
		t.Fatalf("Chat вернул ошибку: %v", err)
	}
	if got != f.answer {
		t.Errorf("ответ %q, ожидался %q", got, f.answer)
	}
	// Путь: базовый адрес + /chat/completions, без двойного слэша.
	if f.gotPath != "/v1/chat/completions" {
		t.Errorf("путь %q, ожидался /v1/chat/completions", f.gotPath)
	}
	if f.gotAuth != "Bearer секретный-ключ" {
		t.Errorf("заголовок авторизации %q — ключ не уехал так, как надо", f.gotAuth)
	}
	if f.gotBody["model"] != "test/model" {
		t.Errorf("модель в запросе: %v", f.gotBody["model"])
	}
	if f.gotBody["stream"] != false {
		t.Errorf("stream должен быть выключен на этапе 2, получено %v", f.gotBody["stream"])
	}
	if int(f.gotBody["max_tokens"].(float64)) != 111 {
		t.Errorf("max_tokens: %v", f.gotBody["max_tokens"])
	}
	msgs, ok := f.gotBody["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("история уехала не целиком: %v", f.gotBody["messages"])
	}
}

// Ключа нет — это ОЖИДАЕМАЯ ситуация (пользователь ещё не настроил), поэтому
// она обязана быть отдельной ошибкой с понятным текстом, а не «что-то пошло не
// так». И запрос при этом вообще не должен уходить.
func TestChatWithoutKeyDoesNotCallAPI(t *testing.T) {
	f := newFakeNIM(t)
	t.Setenv("FRIEND_TEST_KEY", "")
	c := New(testCfg(f))

	if c.Ready() {
		t.Fatal("Ready() с пустым ключом вернул true")
	}
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}})
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("ожидалась ErrNoKey, получено %v", err)
	}
	if !strings.Contains(err.Error(), "FRIEND_TEST_KEY") {
		t.Errorf("в ошибке нет имени переменной окружения: %v", err)
	}
	if f.requests != 0 {
		t.Errorf("без ключа ушло %d запросов, ожидалось 0", f.requests)
	}
}

func TestStatusIsHumanReadableAndHasNoKey(t *testing.T) {
	f := newFakeNIM(t)
	t.Setenv("FRIEND_TEST_KEY", "секретный-ключ")
	c := New(testCfg(f))
	status := c.Status()
	if !strings.Contains(status, "test/model") {
		t.Errorf("в состоянии нет модели: %q", status)
	}
	if strings.Contains(status, "секретный-ключ") {
		t.Errorf("состояние выдало сам ключ: %q", status)
	}
}

func TestHTTPErrorsAreRecognisable(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "ключ не принят"},
		{http.StatusForbidden, "доступ запрещён"},
		// 451 — блокировка по региону. Человек должен понять, что дело не в ключе:
		// именно так ответил NVIDIA на этой машине, а ключ был рабочий.
		{http.StatusUnavailableForLegalReasons, "региона"},
		{http.StatusNotFound, "не найдены"},
		{http.StatusTooManyRequests, "слишком много запросов"},
		{http.StatusInternalServerError, "сбой"},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			f := newFakeNIM(t)
			f.status = tc.status
			f.rawBody = `{"error":"подробности от API"}`
			t.Setenv("FRIEND_TEST_KEY", "k")
			c := New(testCfg(f))

			_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}})
			var se *StatusError
			if !errors.As(err, &se) {
				t.Fatalf("ожидалась StatusError, получено %v", err)
			}
			if se.Code != tc.status {
				t.Errorf("код %d, ожидался %d", se.Code, tc.status)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("текст %q не содержит %q", err.Error(), tc.want)
			}
			if !strings.Contains(err.Error(), "подробности от API") {
				t.Errorf("в ошибке нет тела ответа: %q", err.Error())
			}
		})
	}
}

func TestBrokenResponses(t *testing.T) {
	t.Run("не json", func(t *testing.T) {
		f := newFakeNIM(t)
		f.rawBody = "<html>мы упали</html>"
		t.Setenv("FRIEND_TEST_KEY", "k")
		c := New(testCfg(f))
		_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}})
		if err == nil || !strings.Contains(err.Error(), "не разобран") {
			t.Fatalf("ожидалась ошибка разбора, получено %v", err)
		}
	})
	t.Run("нет choices", func(t *testing.T) {
		f := newFakeNIM(t)
		f.rawBody = `{"choices":[]}`
		t.Setenv("FRIEND_TEST_KEY", "k")
		c := New(testCfg(f))
		_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}})
		if !errors.Is(err, ErrEmptyReply) {
			t.Fatalf("ожидалась ErrEmptyReply, получено %v", err)
		}
	})
	t.Run("пустая реплика", func(t *testing.T) {
		f := newFakeNIM(t)
		f.rawBody = `{"choices":[{"message":{"role":"assistant","content":"   "}}]}`
		t.Setenv("FRIEND_TEST_KEY", "k")
		c := New(testCfg(f))
		if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}}); !errors.Is(err, ErrEmptyReply) {
			t.Fatalf("ожидалась ErrEmptyReply, получено %v", err)
		}
	})
}

// Ответ модели с обрезанными пробелами: лишние переносы в облачке выглядели бы
// как «она молчит пустой строкой».
func TestAnswerIsTrimmed(t *testing.T) {
	f := newFakeNIM(t)
	f.answer = "\n\n  Привет.  \n"
	t.Setenv("FRIEND_TEST_KEY", "k")
	c := New(testCfg(f))
	got, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Привет." {
		t.Errorf("ответ %q, ожидался обрезанный «Привет.»", got)
	}
}

// Дедлайн: если API молчит, разговор не должен висеть вечно — иначе окно
// компаньона показывает «думает» до перезапуска.
func TestChatRespectsContextDeadline(t *testing.T) {
	f := newFakeNIM(t)
	f.delay = 2 * time.Second
	t.Setenv("FRIEND_TEST_KEY", "k")
	c := New(testCfg(f))

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Chat(ctx, []Message{{Role: "user", Content: "привет"}})
	if err == nil {
		t.Fatal("молчащий API не дал ошибки")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("ожидание заняло %v — дедлайн не подействовал", elapsed)
	}
}

// «Модель думала вместо ответа»: пустой content при finish_reason=length — это
// не «пустая реплика», а исчерпанный лимит. Текст ошибки должен называть ручку,
// которую надо крутить (llm.max_tokens), иначе человек не знает, что делать.
func TestReasoningModelRunningOutOfTokens(t *testing.T) {
	f := newFakeNIM(t)
	f.rawBody = `{"choices":[{"message":{"role":"assistant","content":null,"reasoning_content":"Надо ответить по-русски..."},"finish_reason":"length"}]}`
	t.Setenv("FRIEND_TEST_KEY", "k")
	c := New(testCfg(f))

	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}})
	if !errors.Is(err, ErrEmptyReply) {
		t.Fatalf("ожидалась ErrEmptyReply, получено %v", err)
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("в ошибке нет подсказки про max_tokens: %v", err)
	}
}

// Ответ есть, но оборван по длине: это всё равно ответ — показываем его, а не
// ошибку (в облачке обрезанная фраза лучше молчания).
func TestTruncatedButNonEmptyAnswerIsUsed(t *testing.T) {
	f := newFakeNIM(t)
	f.rawBody = `{"choices":[{"message":{"role":"assistant","content":"Я тут, всё хо"},"finish_reason":"length"}]}`
	t.Setenv("FRIEND_TEST_KEY", "k")
	c := New(testCfg(f))

	got, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatalf("оборванный ответ дал ошибку: %v", err)
	}
	if got != "Я тут, всё хо" {
		t.Errorf("ответ %q", got)
	}
}

func TestEmptyHistoryIsRejected(t *testing.T) {
	f := newFakeNIM(t)
	t.Setenv("FRIEND_TEST_KEY", "k")
	c := New(testCfg(f))
	if _, err := c.Chat(context.Background(), nil); err == nil {
		t.Fatal("пустая история не дала ошибки")
	}
	if f.requests != 0 {
		t.Errorf("ушло %d запросов, ожидалось 0", f.requests)
	}
}

// Локальная модель: имя переменной с ключом не задано — значит ключ не нужен, и
// клиент обязан работать. Пока он требовал ключ всегда, Ollama была неотличима
// от ненастроенного API: конфиг верный, а в окне «задай переменную окружения».
func TestLocalAPINeedsNoKey(t *testing.T) {
	f := newFakeNIM(t)
	cfg := testCfg(f)
	cfg.APIKeyEnv = ""
	c := New(cfg)

	if !c.Ready() {
		t.Fatal("Ready() без ключа вернул false — локальную модель не позвать")
	}
	if !strings.Contains(c.Status(), "ключ не нужен") {
		t.Errorf("в состоянии нет «ключ не нужен»: %q", c.Status())
	}
	got, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatalf("Chat без ключа вернул ошибку: %v", err)
	}
	if got != f.answer {
		t.Errorf("ответ %q, ожидался %q", got, f.answer)
	}
	if f.gotAuth != "" {
		t.Errorf("уехал заголовок авторизации %q — локальному серверу он не нужен", f.gotAuth)
	}
}

// Имя переменной задано, а значения нет — это по-прежнему «ключа нет», а не
// «локальная модель»: иначе пустая строка в .env тихо превращала бы облачный API
// в анонимный запрос, и человек видел бы 401 вместо понятной подсказки.
func TestNamedButEmptyKeyEnvIsStillNoKey(t *testing.T) {
	f := newFakeNIM(t)
	t.Setenv("FRIEND_TEST_KEY", "")
	c := New(testCfg(f))
	if c.Ready() {
		t.Fatal("Ready() с пустым значением ключа вернул true")
	}
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "привет"}}); !errors.Is(err, ErrNoKey) {
		t.Fatalf("ожидалась ErrNoKey, получено %v", err)
	}
}

// Пустой base_url берёт адрес NIM по умолчанию: иначе забытая строка в конфиге
// означала бы «запрос в никуда».
func TestDefaultBaseURLAndTimeout(t *testing.T) {
	c := New(config.LLM{Model: "m", APIKeyEnv: "K"})
	if c.url != defaultBaseURL+"/chat/completions" {
		t.Errorf("адрес по умолчанию: %q", c.url)
	}
	if c.Timeout() != 30*time.Second {
		t.Errorf("таймаут по умолчанию: %v", c.Timeout())
	}
}
