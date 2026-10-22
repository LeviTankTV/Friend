package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeTTS — озвучка-подделка: пишет «mp3» в файл и отдаёт путь.
type fakeTTS struct{ file string }

func (f *fakeTTS) Available() bool { return true }
func (f *fakeTTS) Synth(_ context.Context, _ string) (string, error) {
	return f.file, nil
}

func TestTTSRouteServesAudio(t *testing.T) {
	dir := t.TempDir()
	mp3 := filepath.Join(dir, "voice.mp3")
	if err := os.WriteFile(mp3, []byte("FAKE-MP3-BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, hub := newTestServerWith(t, nil)
	srv, err := New(Options{Hub: hub, Log: testLog(), TTS: &fakeTTS{file: mp3}})
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("озвучка должна отдаваться, получен статус %d", resp.StatusCode)
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
