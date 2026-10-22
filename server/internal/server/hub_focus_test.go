package server

import (
	"testing"
	"time"

	"friend/internal/focus"
)

// Кадр focus обязан дойти до трекера: по нему компаньон узнаёт, чем человек
// занят, и по нему же самопроверка оболочки судит о всей цепочке
// focuswatch → страница → сервер.
func TestFocusUpdatesTracker(t *testing.T) {
	srv, _, hub := newTestServerWith(t, nil)
	hub.SetFocusTracker(focus.New())
	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "hello", "client": "web"})
	if m := recv(t, conn); m["type"] != "welcome" {
		t.Fatalf("ожидался welcome, получено %+v", m)
	}

	send(t, conn, map[string]any{"type": "focus", "app": "goland64.exe", "title": "main.go\t— GoLand"})
	deadline := time.Now().Add(3 * time.Second)
	var s focus.Snapshot
	for time.Now().Before(deadline) {
		if got, ok := hub.GetFocus(); ok {
			s = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s.Process != "goland64.exe" {
		t.Fatalf("трекер не получил процесс: %+v", s)
	}
	if s.Kind != focus.KindCode {
		t.Fatalf("GoLand обязан классифицироваться как код: %+v", s)
	}
	if s.Title != "main.go — GoLand" {
		t.Fatalf("заголовок не причёсан: %q", s.Title)
	}
	if s.Since.IsZero() {
		t.Fatal("трекер обязан помнить, когда человек перешёл в приложение")
	}
}

// Строка тестового окна (помощник с -selftest) не имеет права менять контекст
// рабочей Миры: маркер в заголовке — и строка выбрасывается, как будто её не
// было. Канал при этом обязан остаться живым.
func TestFocusSelftestLineDropped(t *testing.T) {
	srv, _, hub := newTestServerWith(t, nil)
	hub.SetFocusTracker(focus.New())
	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "hello", "client": "web"})
	recv(t, conn) // welcome

	// Живое окно — принимается, чтобы тест отличал «дроп» от «трекер молчит».
	send(t, conn, map[string]any{"type": "focus", "app": "notepad.exe", "title": "Текст.txt"})
	deadline := time.Now().Add(3 * time.Second)
	got := false
	for time.Now().Before(deadline) {
		if s, ok := hub.GetFocus(); ok && s.Process == "notepad.exe" {
			got = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !got {
		t.Fatal("живое окно должно доходить до трекера")
	}

	send(t, conn, map[string]any{"type": "focus", "app": "SelftestTarget.exe", "title": "SELFTEST-окно-посадки", "idle": 1})
	send(t, conn, map[string]any{"type": "ping"})
	m := recv(t, conn)
	if m["type"] != "pong" {
		t.Fatalf("тестовая строка не обязана ломать канал, получен %+v", m)
	}
	// Дай каналу время обработать focus: раньше двух тиков трекер мог его
	// ещё не разобрать, а после — обязан остаться на notepad.
	time.Sleep(200 * time.Millisecond)
	s, ok := hub.GetFocus()
	if !ok || s.Process != "notepad.exe" {
		t.Fatalf("тестовая строка изменила контекст рабочей Миры: %+v", s)
	}
}

// Пустой app и отсутствие трекера — не повод ронять канал: оболочка на Linux
// вообще не присылает focus, а страница может прислать обрывок.
func TestFocusIsQuietlyIgnored(t *testing.T) {
	srv, _, hub := newTestServerWith(t, nil) // трекера нет вовсе
	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "hello", "client": "web"})
	recv(t, conn) // welcome
	send(t, conn, map[string]any{"type": "focus", "app": "", "title": "x"})
	send(t, conn, map[string]any{"type": "ping"})
	m := recv(t, conn)
	if m["type"] != "pong" {
		t.Fatalf("кадр focus не обязан ломать канал, получен %+v", m)
	}
	if _, ok := hub.GetFocus(); ok {
		t.Fatal("без трекера GetFocus обязан возвращать «ничего нет»")
	}
}
