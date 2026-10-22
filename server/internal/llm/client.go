// Package llm — клиент OpenAI-совместимого API: NVIDIA NIM по умолчанию, но
// подойдёт и локальная модель (Ollama, LM Studio, llama.cpp) — у неё тот же
// /chat/completions, только без ключа. Адрес и модель берутся из конфига, так
// что смена провайдера — это строка в config.json, а не правка кода.
//
// Один вызов, который нужен компаньону: «вот система, вот история — ответь
// репликой». Ничего больше: без стриминга, без инструментов и без хранения
// состояния. Так у этого пакета ровно одна причина меняться — формат запроса
// к API.
//
// Ключ НИКОГДА не хранится в конфиге и не попадает в логи: в конфиге лежит
// только ИМЯ переменной окружения (llm.api_key_env), а значение читается в
// момент запроса. Пустое имя означает «ключ не нужен» — так подключается
// локальный сервер (см. needsKey). Поэтому ключ можно поменять без правки конфига и пересборки,
// и он не окажется ни в git, ни в дампе настроек.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"friend/internal/config"
)

// Базовый адрес NIM, если в конфиге пусто (совпадает с config.Default()).
const defaultBaseURL = "https://integrate.api.nvidia.com/v1"

// maxResponseBytes — предел чтения ответа. Тело ошибки иногда приходит с
// целой HTML-страницей; держать это в памяти незачем.
const maxResponseBytes = 256 << 10

// snippetLen — сколько символов ответа показывать в сообщении об ошибке.
const snippetLen = 300

var (
	// ErrNoKey — ключа нет в окружении. Отдельная ошибка, потому что это
	// ожидаемая ситуация (пользователь ещё не настроил ключ), и её надо
	// показать человеку понятным текстом, а не «ошибка сети».
	ErrNoKey = errors.New("нет ключа к API")
	// ErrEmptyReply — модель ответила, но пустотой.
	ErrEmptyReply = errors.New("модель ответила пустой репликой")
)

// Message — одно сообщение диалога (роль: system | user | assistant).
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client — клиент OpenAI-совместимого API (NIM, локальная Ollama, LM Studio,
// другой провайдер). Безопасен для одновременного использования.
type Client struct {
	url string
	// base — базовый адрес без /chat/completions. Нужен только для сообщений
	// человеку: по «http://127.0.0.1:11434/v1» сразу видно, что модель локальная.
	base        string
	model       string
	apiKeyEnv   string
	maxTokens   int
	temperature float64
	hc          *http.Client
}

// New собирает клиент из конфига. Ключ здесь НЕ читается — только имя
// переменной: см. комментарий к пакету.
func New(cfg config.LLM) *Client {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = defaultBaseURL
	}
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 300
	}
	return &Client{
		url:         base + "/chat/completions",
		base:        base,
		model:       cfg.Model,
		apiKeyEnv:   cfg.APIKeyEnv,
		maxTokens:   maxTokens,
		temperature: cfg.Temperature,
		hc:          &http.Client{Timeout: timeout},
	}
}

// KeyEnv — имя переменной окружения с ключом (для подсказок человеку).
func (c *Client) KeyEnv() string { return c.apiKeyEnv }

// Model — какая модель настроена.
func (c *Client) Model() string { return c.model }

// Timeout — сколько ждём ответ. Нужен вызывающей стороне, чтобы ставить
// дедлайн на всю операцию, а не только на HTTP.
func (c *Client) Timeout() time.Duration { return c.hc.Timeout }

// needsKey — нужен ли этому API ключ ВООБЩЕ.
//
// Ключа нет у локальных OpenAI-совместимых серверов: Ollama, LM Studio,
// llama.cpp. Пока клиент требовал непустую переменную окружения всегда,
// локальная модель была неотличима от ненастроенной: человек указывал
// http://127.0.0.1:11434/v1, а в окне получал «задай переменную окружения».
// Поэтому признак «ключ не нужен» — это ПУСТОЕ имя переменной в конфиге, а не
// пустое её значение: имя задано, а значения нет — это по-прежнему «ключа нет».
func (c *Client) needsKey() bool { return strings.TrimSpace(c.apiKeyEnv) != "" }

// Ready — можно ли отвечать прямо сейчас. Читается из окружения на каждый
// вызов: человек может задать переменную и перезапустить сервер, не трогая
// конфиг. Локальному API ключ не нужен вовсе, поэтому он готов всегда.
func (c *Client) Ready() bool {
	if !c.needsKey() {
		return true
	}
	return strings.TrimSpace(os.Getenv(c.apiKeyEnv)) != ""
}

// Status — человекочитаемое состояние клиента (идёт в лог и в окно, без ключа).
func (c *Client) Status() string {
	if !c.needsKey() {
		return fmt.Sprintf("модель %s на %s, ключ не нужен", c.model, c.base)
	}
	if !c.Ready() {
		return fmt.Sprintf("модель %s, ключа нет: задай переменную окружения %s", c.model, c.apiKeyEnv)
	}
	return fmt.Sprintf("модель %s, ключ есть (%s)", c.model, c.apiKeyEnv)
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
		// FinishReason нужен, чтобы отличить «модель ответила» от «модель не
		// успела»: часть доступных моделей сначала думает вслух, и на маленьком
		// лимите вызов заканчивается по длине с пустым ответом. Без этого
		// человек видел бы «пустая реплика» и не понимал, что поправить.
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// StatusError — API ответил не 200. Отдельный тип, чтобы вызывающая сторона
// могла отличить «ключ не принят» от «сеть недоступна» и сказать это человеку.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	what := "API ответил ошибкой"
	switch e.Code {
	case http.StatusUnauthorized:
		what = "ключ не принят"
	case http.StatusForbidden:
		what = "доступ запрещён (403)"
	case http.StatusNotFound:
		what = "модель или адрес не найдены"
	case http.StatusTooManyRequests:
		what = "слишком много запросов"
	case http.StatusUnavailableForLegalReasons:
		// 451 — так отвечает провайдер, закрытый для страны запроса (NVIDIA
		// делает так же: даже список моделей отдаёт 451, ещё до проверки
		// ключа). Без этой ветки человек видел «ошибка 451», шёл проверять
		// ключ — и не находил, что с ним не так: с ключом всё в порядке.
		what = "провайдер закрыт для твоего региона (451): помогут VPN или прокси, " +
			"локальная модель (ollama) или другой провайдер"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		what = "API не ответил вовремя"
	default:
		if e.Code >= 500 {
			what = "на стороне API сбой"
		}
	}
	if e.Body == "" {
		return fmt.Sprintf("%s (%d)", what, e.Code)
	}
	return fmt.Sprintf("%s (%d): %s", what, e.Code, e.Body)
}

// Chat отправляет историю и возвращает ответ модели одной строкой.
//
// Дедлайн берётся из конфига (llm.timeout_sec) на уровне HTTP-клиента, поэтому
// отдельно ставить его здесь не нужно: вызывающая сторона ставит ещё и
// контекст, чтобы можно было передумать (например, окно закрыли).
func (c *Client) Chat(ctx context.Context, msgs []Message) (string, error) {
	if len(msgs) == 0 {
		return "", errors.New("пустой запрос: нет ни системного промпта, ни истории")
	}
	// Ключ спрашиваем только у тех API, у которых он есть (см. needsKey).
	key := ""
	if c.needsKey() {
		key = strings.TrimSpace(os.Getenv(c.apiKeyEnv))
		if key == "" {
			return "", fmt.Errorf("%w: переменная окружения %s не задана", ErrNoKey, c.apiKeyEnv)
		}
	}

	payload, err := json.Marshal(chatRequest{
		Model:       c.model,
		Messages:    msgs,
		MaxTokens:   c.maxTokens,
		Temperature: c.temperature,
		Stream:      false,
	})
	if err != nil {
		return "", fmt.Errorf("запрос к модели не собрался: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Заголовок отправляем только с ключом: локальному серверу «Bearer » без
	// значения не нужен, а некоторые шлюзы на пустой ключ отвечают 401.
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("модель недоступна (%s): %w", c.base, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("ответ модели не прочитался: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", &StatusError{Code: resp.StatusCode, Body: snippet(raw)}
	}

	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("ответ модели не разобран как JSON (%v), тело: %s", err, snippet(raw))
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%w: в ответе нет choices (тело: %s)", ErrEmptyReply, snippet(raw))
	}
	text := strings.TrimSpace(out.Choices[0].Message.Content)
	if text == "" {
		if out.Choices[0].FinishReason == "length" {
			return "", fmt.Errorf("%w: модель израсходовала лимит (%d токенов) на размышления и не дошла до ответа — увеличь llm.max_tokens в конфиге", ErrEmptyReply, c.maxTokens)
		}
		return "", ErrEmptyReply
	}
	return text, nil
}

// snippet — короткий кусок тела ответа для сообщения об ошибке. Ключ сюда
// попасть не может: в теле ответа его нет, а заголовки мы не печатаем.
func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > snippetLen {
		return s[:snippetLen] + "…"
	}
	return s
}
