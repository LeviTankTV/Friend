package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"friend/internal/chat"
	"friend/internal/llm"
)

// fakeResponder — поддельный компаньон. Настоящий требует ключа и интернета, а
// проверяем мы канал: порядок кадров, очередь реплик и то, что окно не встаёт
// колом, пока модель думает.
type fakeResponder struct {
	ready  bool
	answer string
	err    error
	delay  time.Duration
	hist   []chat.Message

	mu    sync.Mutex
	asked []string
	calls int
}

func (f *fakeResponder) Answer(ctx context.Context, text string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.asked = append(f.asked, text)
	f.mu.Unlock()
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

func (f *fakeResponder) Ready() bool { return f.ready }
func (f *fakeResponder) Status() string {
	return "поддельная модель, ключ есть"
}
func (f *fakeResponder) Timeout() time.Duration { return time.Second }
func (f *fakeResponder) History() []chat.Message {
	return append([]chat.Message(nil), f.hist...)
}

func (f *fakeResponder) askedTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

func (f *fakeResponder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Реплика человека: окно получает «думает» → ответ → «думает» выключен.
// Порядок важен: индикатор, который гаснет раньше ответа, выглядит как сбой.
func TestSayGetsThinkingThenReply(t *testing.T) {
	resp := &fakeResponder{ready: true, answer: "Я тут. Как ты?"}
	srv, _, _ := newTestServerWith(t, resp)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	send(t, conn, map[string]any{"type": TypeSay, "text": "привет"})

	first := recv(t, conn)
	if first["type"] != TypeThinking || first["on"] != true {
		t.Fatalf("первый кадр после реплики = %v, ожидался thinking{on:true}", first)
	}
	second := recv(t, conn)
	if second["type"] != TypeReply {
		t.Fatalf("второй кадр = %v, ожидался %s", second["type"], TypeReply)
	}
	if second["text"] != resp.answer {
		t.Errorf("текст ответа %q, ожидался %q", second["text"], resp.answer)
	}
	if second["proactive"] != nil {
		t.Errorf("ответ на реплику помечен как инициатива: %v", second["proactive"])
	}
	third := recv(t, conn)
	if third["type"] != TypeThinking || third["on"] != false {
		t.Fatalf("третий кадр = %v, ожидался thinking{on:false}", third)
	}
	if got := resp.askedTexts(); len(got) != 1 || got[0] != "привет" {
		t.Errorf("к компаньону ушло %v", got)
	}
}

// Без ключа: мгновенный понятный ответ вместо «думает» и молчания. Текст
// обязан говорить, что делать — иначе человек видит сломанного компаньона.
func TestSayWithoutKeyExplains(t *testing.T) {
	resp := &fakeResponder{ready: false}
	srv, _, hub := newTestServerWith(t, resp)
	hub.SetKeyHint("Положи ключ в файл .env рядом с start.bat.")
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	send(t, conn, map[string]any{"type": TypeSay, "text": "привет"})
	msg := recv(t, conn)
	if msg["type"] != TypeReplyError {
		t.Fatalf("кадр = %v, ожидался %s", msg["type"], TypeReplyError)
	}
	text, _ := msg["error"].(string)
	if !strings.Contains(text, "поддельная модель") || !strings.Contains(text, ".env") {
		t.Errorf("текст ошибки ничего не объясняет: %q", text)
	}
	if resp.callCount() != 0 {
		t.Errorf("без ключа компаньона всё равно спросили %d раз", resp.callCount())
	}
}

// Сбой модели объясняется человеческим текстом, а не «ошибкой 500».
func TestModelFailureIsExplained(t *testing.T) {
	resp := &fakeResponder{ready: true, err: &llm.StatusError{Code: 401, Body: "invalid key"}}
	srv, _, _ := newTestServerWith(t, resp)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	send(t, conn, map[string]any{"type": TypeSay, "text": "привет"})
	_ = recv(t, conn) // thinking
	msg := recv(t, conn)
	if msg["type"] != TypeReplyError {
		t.Fatalf("кадр = %v, ожидался %s", msg["type"], TypeReplyError)
	}
	text, _ := msg["error"].(string)
	if !strings.Contains(text, "ключ не принят") {
		t.Errorf("текст ошибки %q не объясняет причину", text)
	}
}

// Пока модель думает, окно продолжает жить: позиция сохраняется и
// подтверждается. Если бы ответ ждали в цикле чтения, перетаскивание окна
// вставало бы на всё время ответа.
func TestSayDoesNotBlockWindow(t *testing.T) {
	resp := &fakeResponder{ready: true, answer: "готово", delay: 600 * time.Millisecond}
	srv, store, _ := newTestServerWith(t, resp)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	send(t, conn, map[string]any{"type": TypeSay, "text": "привет"})
	_ = recv(t, conn) // thinking

	// Пока ответа нет, окно тащат мышью — этот кадр должен быть обслужен.
	send(t, conn, map[string]any{"type": TypePosition, "x": 321, "y": 654})
	msg := recv(t, conn)
	if msg["type"] != TypeState {
		t.Fatalf("во время ответа окно получило %v, ожидался %s", msg["type"], TypeState)
	}
	if got := store.Snapshot().Position; got.X != 321 || got.Y != 654 {
		t.Errorf("позиция на диске %+v, ожидалась 321/654", got)
	}

	// И только потом приходит ответ.
	deadline := time.Now().Add(3 * time.Second)
	for {
		reply := recv(t, conn)
		if reply["type"] == TypeReply {
			if reply["text"] != "готово" {
				t.Errorf("текст ответа %v", reply["text"])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ответ не пришёл, последний кадр %v", reply)
		}
	}
}

// Вторая реплика не теряется, а ждёт своей очереди: разговор должен быть
// последовательным (и история — без перепутанного порядка).
func TestSecondSayIsQueued(t *testing.T) {
	resp := &fakeResponder{ready: true, answer: "ага", delay: 200 * time.Millisecond}
	srv, _, _ := newTestServerWith(t, resp)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	send(t, conn, map[string]any{"type": TypeSay, "text": "первая"})
	send(t, conn, map[string]any{"type": TypeSay, "text": "вторая"})

	// Собираем кадры, пока не увидим два ответа.
	replies := 0
	errFrames := 0
	deadline := time.Now().Add(4 * time.Second)
	for replies < 2 && time.Now().Before(deadline) {
		msg := recv(t, conn)
		switch msg["type"] {
		case TypeReply:
			replies++
		case TypeReplyError:
			errFrames++
		}
	}
	if replies != 2 {
		t.Fatalf("ответов пришло %d, ожидалось 2 (ошибок: %d)", replies, errFrames)
	}
	asked := resp.askedTexts()
	if len(asked) != 2 || asked[0] != "первая" || asked[1] != "вторая" {
		t.Errorf("порядок реплик у компаньона: %v", asked)
	}
}

// Поток реплик не должен копить бесконечную очередь: лишние получают отказ.
func TestFloodIsRejected(t *testing.T) {
	resp := &fakeResponder{ready: true, answer: "ага", delay: 300 * time.Millisecond}
	srv, _, _ := newTestServerWith(t, resp)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	for i := 0; i < chatQueue+4; i++ {
		send(t, conn, map[string]any{"type": TypeSay, "text": "реплика"})
	}

	sawBusy := false
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && !sawBusy {
		msg := recv(t, conn)
		if msg["type"] == TypeReplyError {
			if text, _ := msg["error"].(string); strings.Contains(text, "договорила") {
				sawBusy = true
			}
		}
	}
	if !sawBusy {
		t.Fatalf("очередь не ограничена: все %d реплик приняты", chatQueue+4)
	}
	if calls := resp.callCount(); calls > chatQueue {
		t.Errorf("к компаньону ушло %d реплик, хотя очередь вмещает %d", calls, chatQueue)
	}
}

// Пустая и слишком длинная реплики отсекаются до обращения к модели.
func TestBadSayIsRejected(t *testing.T) {
	resp := &fakeResponder{ready: true, answer: "ага"}
	srv, _, _ := newTestServerWith(t, resp)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	for _, text := range []string{"   ", strings.Repeat("я", maxSayRunes+1)} {
		send(t, conn, map[string]any{"type": TypeSay, "text": text})
		msg := recv(t, conn)
		if msg["type"] != TypeReplyError {
			t.Fatalf("кадр на плохую реплику = %v, ожидался %s", msg["type"], TypeReplyError)
		}
	}
	if resp.callCount() != 0 {
		t.Errorf("плохие реплики дошли до модели: %d", resp.callCount())
	}
}

// Разговор выключен (хаб без компаньона) — окно должно узнать это текстом, а
// не тишиной.
func TestSayWithoutResponderIsExplained(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	send(t, conn, map[string]any{"type": TypeSay, "text": "привет"})
	msg := recv(t, conn)
	if msg["type"] != TypeReplyError {
		t.Fatalf("кадр = %v, ожидался %s", msg["type"], TypeReplyError)
	}
	if text, _ := msg["error"].(string); !strings.Contains(text, "не настроен") {
		t.Errorf("текст %q не объясняет, что разговор выключен", text)
	}
}

// История разговора уезжает окну в welcome: после перезагрузки страницы
// разговор не выглядит начатым с нуля, пока жив сервер.
func TestWelcomeCarriesHistory(t *testing.T) {
	resp := &fakeResponder{ready: true, answer: "ага", hist: []chat.Message{
		{Role: chat.RoleUser, Text: "привет", At: time.Now()},
		{Role: chat.RoleAssistant, Text: "привет-привет", At: time.Now()},
	}}
	srv, _, _ := newTestServerWith(t, resp)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})

	msg := recv(t, conn)
	hist, ok := msg["history"].([]any)
	if !ok || len(hist) != 2 {
		t.Fatalf("история в welcome = %v, ожидалось 2 реплики", msg["history"])
	}
	first := hist[0].(map[string]any)
	if first["role"] != chat.RoleUser || first["text"] != "привет" {
		t.Errorf("первая реплика истории %v", first)
	}
}

// Ошибка канала связи (модель молчит до дедлайна) обязана гасить индикатор:
// «думает» навсегда — это баг, который человек не может ни увидеть в логе, ни
// исправить.
func TestThinkingIsAlwaysTurnedOff(t *testing.T) {
	resp := &fakeResponder{ready: true, err: errors.New("что-то сломалось")}
	srv, _, _ := newTestServerWith(t, resp)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	send(t, conn, map[string]any{"type": TypeSay, "text": "привет"})
	on := recv(t, conn)
	if on["type"] != TypeThinking || on["on"] != true {
		t.Fatalf("ожидался thinking{on:true}, получено %v", on)
	}
	errFrame := recv(t, conn)
	if errFrame["type"] != TypeReplyError {
		t.Fatalf("ожидался %s, получено %v", TypeReplyError, errFrame)
	}
	off := recv(t, conn)
	if off["type"] != TypeThinking || off["on"] != false {
		t.Fatalf("индикатор не выключился: %v", off)
	}
}
