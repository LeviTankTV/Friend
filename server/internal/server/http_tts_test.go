package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeTTS — озвучка-подделка: пишет «mp3» в файл и отдаёт путь;
// запоминает интонацию последнего запроса (этап 9б).
type fakeTTS struct {
	file      string
	lastRate  int
	lastPitch int
}

func (f *fakeTTS) Available() bool { return true }
func (f *fakeTTS) Synth(_ context.Context, _ string, rateDelta, pitchDelta, volDelta int) (string, error) {
	f.lastRate, f.lastPitch = rateDelta, pitchDelta
	return f.file, nil
}

func TestTTSRouteServesAudio(t *testing.T) {
	dir := t.TempDir()
	mp3 := filepath.Join(dir, "voice.mp3")
	if err := os.WriteFile(mp3, []byte("FAKE-MP3-BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, hub := newTestServerWith(t, nil)
	fake := &fakeTTS{file: mp3}
	srv, err := New(Options{Hub: hub, Log: testLog(), TTS: fake})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/tts?text=привет&rate=-12&pitch=-6")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("озвучка должна отдаваться, получен статус %d", resp.StatusCode)
	}
	// Интонация (этап 9б): страница сообщает сдвиги темпа и тона — сервер
	// обязан передать их в озвучку.
	if fake.lastRate != -12 || fake.lastPitch != -6 {
		t.Fatalf("интонация не дошла до озвучки: %+d/+%d", fake.lastRate, fake.lastPitch)
	}

	// Пустой текст — отказ, а не пустой mp3.
	resp2, err := http.Get(ts.URL + "/tts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("пустой текст обязан отклоняться, получен статус %d", resp2.StatusCode)
	}
}

func TestTTSRouteAbsentWithoutService(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	srv, err := New(Options{Hub: hub, Log: testLog()})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/tts?text=привет")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("без озвучивающей службы /tts не должен работать")
	}
}

// TestWelcomeCarriesVoice — флаг доступности озвучки приезжает в welcome:
// фронтенд по нему решает, просить ли /tts.
func TestWelcomeCarriesVoice(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	hub.SetVoice(&VoiceInfo{Available: true, Engine: "edge-tts", Voice: "ru-RU-SvetlanaNeural"})
	ts := newTestWS(t, hub)
	conn := dial(t, ts)
	send(t, conn, map[string]any{"type": "hello", "client": "test", "version": 1})
	frame := recv(t, conn)
	if frame["type"] != "welcome" {
		t.Fatalf("ожидается welcome, получен кадр %v", frame)
	}
	voice, ok := frame["voice"].(map[string]any)
	if !ok {
		t.Fatalf("в welcome нет поля voice: %v", frame)
	}
	if voice["available"] != true || voice["voice"] != "ru-RU-SvetlanaNeural" {
		t.Fatalf("voice не тот: %v", voice)
	}
}
