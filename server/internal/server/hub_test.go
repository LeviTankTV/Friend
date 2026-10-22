package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"friend/internal/config"
	"friend/internal/state"
)

// newTestServer поднимает живой httptest-сервер с хабом и временным файлом
// состояния — проверяем настоящий канал, а не моки: ошибка в апгрейде или в
// формате кадра должна ловиться здесь.
func newTestServer(t *testing.T) (*httptest.Server, *state.Store) {
	t.Helper()
	srv, store, _ := newTestServerWith(t, nil)
	return srv, store
}

// newTestServerWith — то же, но с заданным «кто отвечает» (nil — разговор
// выключен).
func newTestServerWith(t *testing.T, responder Responder) (*httptest.Server, *state.Store, *Hub) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: slog.LevelError}))
	hub := NewHub(log, store, config.Avatar{Name: "Мира", Width: 300, Height: 400}, responder)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.HandleWS)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store, hub
}

func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("подключение к /ws: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func send(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	if err := conn.WriteJSON(v); err != nil {
		t.Fatalf("отправка: %v", err)
	}
}

func recv(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ожидался кадр от сервера: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("кадр не разобрался (%s): %v", string(raw), err)
	}
	return m
}

// TestHelloReturnsState — после hello окно получает версию протокола, персонажа
// и сохранённое состояние.
func TestHelloReturnsState(t *testing.T) {
	srv, store := newTestServer(t)
	if _, err := store.SetPosition(state.Position{X: 700, Y: 420}); err != nil {
		t.Fatal(err)
	}
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})

	msg := recv(t, conn)
	if msg["type"] != TypeWelcome {
		t.Fatalf("первый кадр = %v, want %s", msg["type"], TypeWelcome)
	}
	if int(msg["protocol"].(float64)) != protocolVersion {
		t.Errorf("protocol = %v, want %d", msg["protocol"], protocolVersion)
	}
	st, ok := msg["state"].(map[string]any)
	if !ok {
		t.Fatalf("в welcome нет state: %v", msg)
	}
	pos := st["position"].(map[string]any)
	if pos["x"].(float64) != 700 || pos["y"].(float64) != 420 {
		t.Errorf("позиция в welcome = %v, want 700/420", pos)
	}
	av := msg["avatar"].(map[string]any)
	if av["name"] != "Мира" {
		t.Errorf("имя персонажа = %v, want Мира", av["name"])
	}
}

// TestPositionIsPersisted — движение окна сохраняется на диск, и клиент видит
// подтверждение именно с записанной позицией.
func TestPositionIsPersisted(t *testing.T) {
	srv, store := newTestServer(t)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Client: "test", Version: protocolVersion})
	_ = recv(t, conn)

	send(t, conn, map[string]any{"type": TypePosition, "x": 100, "y": 250})
	msg := recv(t, conn)
	if msg["type"] != TypeState {
		t.Fatalf("ответ на position = %v, want %s", msg["type"], TypeState)
	}
	if msg["saved"] != true {
		t.Errorf("saved = %v, want true", msg["saved"])
	}
	if got := store.Snapshot().Position; got.X != 100 || got.Y != 250 {
		t.Errorf("на диске позиция %+v, want 100/250", got)
	}
}

// TestPositionWithoutCoordsIsRejected — неполное сообщение не должно молча
// двигать окно в 0,0.
func TestPositionWithoutCoordsIsRejected(t *testing.T) {
	srv, store := newTestServer(t)
	if _, err := store.SetPosition(state.Position{X: 10, Y: 20}); err != nil {
		t.Fatal(err)
	}
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypeHello, Version: protocolVersion})
	_ = recv(t, conn)

	send(t, conn, map[string]any{"type": TypePosition})
	msg := recv(t, conn)
	if msg["type"] != TypeError {
		t.Fatalf("ответ = %v, want %s", msg["type"], TypeError)
	}
	if got := store.Snapshot().Position; got.X != 10 || got.Y != 20 {
		t.Errorf("позиция изменилась на %+v, хотя координат не было", got)
	}
}

// TestUnknownTypeAnswered — неизвестное сообщение получает внятный ответ, а не
// тишину: иначе фронтенд «зависает» без объяснений.
func TestUnknownTypeAnswered(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "телепорт"})
	msg := recv(t, conn)
	if msg["type"] != TypeError {
		t.Fatalf("ответ на неизвестный тип = %v, want %s", msg["type"], TypeError)
	}
}

// TestForeignOriginRejected — страница с чужого сайта не должна управлять
// компаньоном через ws://127.0.0.1.
func TestForeignOriginRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	h := http.Header{}
	h.Set("Origin", "https://evil.example")
	conn, resp, err := websocket.DefaultDialer.Dial(url, h)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("подключение с чужого Origin прошло — любой сайт смог бы управлять окном")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("статус отказа = %v, want 403", resp)
	}
}

// TestLocalOriginAllowed — свой фронтенд обязан подключаться.
func TestLocalOriginAllowed(t *testing.T) {
	srv, _ := newTestServer(t)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	h := http.Header{}
	h.Set("Origin", "http://127.0.0.1:8787")
	conn, _, err := websocket.DefaultDialer.Dial(url, h)
	if err != nil {
		t.Fatalf("свой Origin отклонён: %v", err)
	}
	_ = conn.Close()
}

// TestPingPong — канал живой: фронтенд использует ping для индикатора связи.
func TestPingPong(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dial(t, srv)
	send(t, conn, Incoming{Type: TypePing})
	msg := recv(t, conn)
	if msg["type"] != TypePong {
		t.Fatalf("ответ на ping = %v, want %s", msg["type"], TypePong)
	}
}
