package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"friend/internal/config"
	"friend/internal/state"
)

// testLog — тихий лог для тестов: консоль теста не должна читать служебные
// строки сервера.
func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newTestWS — ws-сервер вокруг готового хаба (для проверки кадров).
func newTestWS(t *testing.T, h *Hub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.HandleWS)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func newHTTPServer(t *testing.T, spritesDir string) *httptest.Server {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: slog.LevelError}))
	hub := NewHub(log, store, config.Avatar{Name: "Мира", Width: 300, Height: 400}, nil)
	srv, err := New(Options{Hub: hub, Log: log, SpritesDir: spritesDir})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestSpritesRouteServesSheet(t *testing.T) {
	dir := t.TempDir()
	sheet := `{"frames":{"stand":"stand.png"}}`
	if err := os.WriteFile(filepath.Join(dir, "sheet.json"), []byte(sheet), 0o644); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	if err := os.WriteFile(filepath.Join(dir, "stand.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newHTTPServer(t, dir)

	resp, err := http.Get(ts.URL + "/sprites/sheet.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("лист должен отдаваться, получен статус %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("арт обязан отдаваться без кэша (переложил кадры — видно сразу), получено %q", got)
	}
	body := make([]byte, 0, 64)
	buf := make([]byte, 64)
	for {
		n, err := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	if !strings.Contains(string(body), "stand.png") {
		t.Errorf("sheet.json не тот: %q", body)
	}

	// Кадры раздаются тем же каталогом.
	resp2, err := http.Get(ts.URL + "/sprites/stand.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("кадр должен отдаваться, получен статус %d", resp2.StatusCode)
	}
}

func TestSpritesRouteEmptyWhenNoArt(t *testing.T) {
	ts := newHTTPServer(t, t.TempDir()) // пустая папка: человек арт не подкладывал
	resp, err := http.Get(ts.URL + "/sprites/sheet.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("без арта должен быть 404, получен статус %d", resp.StatusCode)
	}
}

func TestSpritesRouteBlocksTraversal(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "..", "secret.txt")
	if err := os.WriteFile(secret, []byte("секрет вне папки арта"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(secret) })
	ts := newHTTPServer(t, dir)
	resp, err := http.Get(ts.URL + "/sprites/../secret.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("выход за пределы папки арта не должен удаваться")
	}
}
