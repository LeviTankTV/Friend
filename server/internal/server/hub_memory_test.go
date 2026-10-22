package server

import (
	"strings"
	"testing"
	"time"

	"friend/internal/memory"
	"friend/internal/mood"
)

// openTestMemory — рабочая SQLite-память во временном каталоге.
func openTestMemory(t *testing.T) *memory.Store {
	t.Helper()
	m, err := memory.Open(t.TempDir() + "/memory.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// «запомни: …» — команда: факт уходит в память, в окно приходит
// подтверждение, и команда с подтверждением записываются в архив разговора.
func TestRememberCommandStoresFact(t *testing.T) {
	mem := openTestMemory(t)
	srv, _, hub := newTestServerWith(t, nil)
	hub.SetMemory(mem)

	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "hello", "client": "web"})
	if m := recv(t, conn); m["type"] != "welcome" {
		t.Fatalf("ожидался welcome: %+v", m)
	}

	send(t, conn, map[string]any{"type": "say", "text": "запомни: кота зовут Барсик"})
	m := recv(t, conn)
	text, _ := m["text"].(string)
	if m["type"] != "reply" || !strings.Contains(text, "Барсик") {
		t.Fatalf("подтверждение не пришло: %+v", m)
	}

	// Факт — в памяти и находится по своему слову.
	facts, err := mem.Recall("барсик", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || !strings.Contains(facts[0].Text, "Барсик") {
		t.Fatalf("факт не запомнился: %+v", facts)
	}
	// Команда и подтверждение — в архиве разговора (саммари видит весь диалог).
	list, err := mem.RecentConvo(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Role != "user" || list[1].Role != "assistant" {
		t.Fatalf("архив команды: %+v", list)
	}
}

// «запомни:» без ключа модели работает: память локальная, разговорная часть
// здесь ни при чём. responder — nil (разговор не настроен вовсе).
func TestRememberWorksWithoutKey(t *testing.T) {
	mem := openTestMemory(t)
	srv, _, hub := newTestServerWith(t, nil) // responder nil
	hub.SetMemory(mem)

	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "hello", "client": "web"})
	recv(t, conn) // welcome
	send(t, conn, map[string]any{"type": "say", "text": "Запомни: Завтра к 15:00 визит к врачу"})
	m := recv(t, conn)
	if m["type"] != "reply" {
		t.Fatalf("без ключа «запомни:» обязана сработать, получено %+v", m)
	}
	facts, _ := mem.Recall("врач", 5)
	if len(facts) != 1 {
		t.Fatalf("факт не записан: %+v", facts)
	}
}

// Пустой факт — понятный отказ, а не молчание; без подключённой памяти —
// команда честно говорит, что её нет.
func TestRememberEmptyFactRejected(t *testing.T) {
	mem := openTestMemory(t)
	srv, _, hub := newTestServerWith(t, nil)
	hub.SetMemory(mem)
	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "hello", "client": "web"})
	recv(t, conn)
	send(t, conn, map[string]any{"type": "say", "text": "запомни:   "})
	m := recv(t, conn)
	if m["type"] != "reply_error" {
		t.Fatalf("пустой факт обязан отклоняться текстом: %+v", m)
	}
	if s, _ := m["error"].(string); !strings.Contains(s, "нужен текст") {
		t.Fatalf("текст отказа непонятен: %q", s)
	}

	// Памяти нет вовсе.
	srv2, _, _ := newTestServerWith(t, nil)
	conn2 := dial(t, srv2)
	send(t, conn2, map[string]any{"type": "hello", "client": "web"})
	recv(t, conn2)
	send(t, conn2, map[string]any{"type": "say", "text": "запомни: что-то"})
	m2 := recv(t, conn2)
	if m2["type"] != "reply_error" {
		t.Fatalf("без памяти команда обязана объяснить статус: %+v", m2)
	}
}

// Команда не ловится на середине фразы («не забывай: ...» — не команда).
func TestRememberPrefixIsStrict(t *testing.T) {
	mem := openTestMemory(t)
	srv, _, hub := newTestServerWith(t, nil)
	hub.SetMemory(mem)
	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "hello", "client": "web"})
	recv(t, conn)
	// responder nil → не-командная реплика получит reply_error про ключ, а
	// НЕ подтверждение запоминания.
	send(t, conn, map[string]any{"type": "say", "text": "не забывай про врача в пятницу"})
	m := recv(t, conn)
	if m["type"] == "reply" {
		t.Fatalf("не-команда запомнилась: %+v", m)
	}
	if facts, _ := mem.Recall("врач", 5); len(facts) != 0 {
		t.Fatalf("фраза записана как факт: %+v", facts)
	}
	_ = time.Now
}

// TestSayEmitsMoodEvent — реплика человека становится событием внутреннего
// состояния (этап 5): хаб шлёт EvUser в подключённый канал, и переполненный
// канал не блокирует обработку say.
func TestSayEmitsMoodEvent(t *testing.T) {
	srv, _, hub := newTestServerWith(t, nil)
	events := make(chan mood.Event, 2)
	hub.SetMoodEvents(events)

	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "hello", "client": "web"})
	if m := recv(t, conn); m["type"] != "welcome" {
		t.Fatalf("ожидался welcome: %+v", m)
	}
	// Ключа нет — say всё равно рождает событие: человек написал, это факт
	// обстановки независимо от ответа модели.
	send(t, conn, map[string]any{"type": "say", "text": "привет"})
	recv(t, conn) // reply_error — ожидаемый ответ без ключа
	select {
	case ev := <-events:
		if ev != mood.EvUser {
			t.Fatalf("ожидалось событие EvUser: %d", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("событие состояния не пришло")
	}
}
