package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSTT — распознавание-подделка: проверяет, что ему принёсся wav-файл, и
// возвращает фиксированный текст.
type fakeSTT struct{ text string }

func (f *fakeSTT) Available() bool { return true }
func (f *fakeSTT) Recognize(_ context.Context, wavPath string) (string, error) {
	data, err := os.ReadFile(wavPath)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(string(data), "RIFF") {
		return "", context.DeadlineExceeded // в тесте сигнал: пришёл не wav
	}
	return f.text, nil
}

func TestSTTRouteRecognizesPost(t *testing.T) {
	dir := t.TempDir()
	_, _, hub := newTestServerWith(t, nil)
	srv, err := New(Options{Hub: hub, Log: testLog(), STT: &fakeSTT{text: "привет, мира"}, STTDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	body := "RIFF" + strings.Repeat("\x00", 200) // волновая «запись»
	resp, err := http.Post(ts.URL+"/stt", "application/octet-stream", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/stt должен принимать запись, получен статус %d", resp.StatusCode)
	}
	var got struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Text != "привет, мира" {
		t.Fatalf("текст распознавания не тот: %q", got.Text)
	}
	// Временный wav обязан стираться: в каталоге данных не должно оставаться
	// записей с микрофона.
	left, _ := filepath.Glob(filepath.Join(dir, "stt-*.wav"))
	if len(left) != 0 {
		t.Fatalf("временная запись не удалена: %v", left)
	}
}

func TestSTTRouteRejectsGarbage(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	srv, err := New(Options{Hub: hub, Log: testLog(), STT: &fakeSTT{}, STTDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	// GET — не метод для расшифровки чужого голоса.
	resp, err := http.Get(ts.URL + "/stt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /stt обязан отклоняется, получен статус %d", resp.StatusCode)
	}

	// Короткое тело (< заголовка WAV) — отказ, движок не дёргается.
	resp2, err := http.Post(ts.URL+"/stt", "application/octet-stream", strings.NewReader("no"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("короткое тело обязано отклоняться, получен статус %d", resp2.StatusCode)
	}
}

func TestSTTRouteAbsentWithoutService(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	srv, err := New(Options{Hub: hub, Log: testLog()})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+"/stt", "application/octet-stream", strings.NewReader("RIFF"+strings.Repeat("\x00", 200)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("без распознающей службы /stt не должен работать: %s", body)
	}
}

// TestWelcomeCarriesSTT — флаг голосового ввода приезжает в welcome.
func TestWelcomeCarriesSTT(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	hub.SetSTT(&STTInfo{Available: true, Engine: "faster-whisper"})
	ts := newTestWS(t, hub)
	conn := dial(t, ts)
	send(t, conn, map[string]any{"type": "hello", "client": "test", "version": 1})
	frame := recv(t, conn)
	if frame["type"] != "welcome" {
		t.Fatalf("ожидается welcome, получен кадр %v", frame)
	}
	stt, ok := frame["stt"].(map[string]any)
	if !ok {
		t.Fatalf("в welcome нет поля stt: %v", frame)
	}
	if stt["available"] != true || stt["engine"] != "faster-whisper" {
		t.Fatalf("stt не тот: %v", stt)
	}
}
