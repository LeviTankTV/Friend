package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeWakeSTT — распознаватель-подделка с фиксированным текстом сегмента.
type fakeWakeSTT struct{ text string }

func (f *fakeWakeSTT) Available() bool { return true }
func (f *fakeWakeSTT) Recognize(_ context.Context, _ string) (string, error) {
	return f.text, nil
}

func TestWakeMention(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"Мира, приветы!", true},            // имя в начале
		{"Мира", true},                       // только имя
		{"Привет, Мира!", true},              // имя после запятой
		{"мира", true},                       // регистр не важен
		{"Мира, ну привет", true},            // запятая после имени
		{"Мир, приветы!", true},              // whisper съел гласную в начале
		{"мир приветы", true},                // то же без запятой
		{"картина мира", false},              // «мира» — родительный от «мир»
		{"нет мира.", false},                 // тот же падеж в конце
		{"красота мира", false},              // «мира» в середине фразы
		{"мираж", false},                     // за именем буква
		{"в мир", false},                     // предлог + «мир» — не обращение
		{"о мир", false},                     // то же
		{"про мир", false},                   // другой падеж
		{"Mup, приветы!", true},              // латинские M/u/p вместо кириллицы
		{"Мiр, приверьте.", true},            // «і» (U+0456) вместо «и» — как у живой записи
		{"mup, мир", true},                   // «m»=м, «u»=и, «p»=р — двойники
		{"картина mupы", false},              // двойники не делают обращение из падежа
		{"", false},                          // пусто
	}
	for _, c := range cases {
		if got := WakeMention(c.text); got != c.want {
			t.Errorf("WakeMention(%q) = %v, ожидалось %v", c.text, got, c.want)
		}
	}
}

func postWake(t *testing.T, ts *httptest.Server) (bool, string) {
	t.Helper()
	body := "RIFF" + strings.Repeat("\x00", 200)
	resp, err := http.Post(ts.URL+"/wake", "application/octet-stream", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/wake должен принимать сегмент, получен статус %d: %s", resp.StatusCode, raw)
	}
	var got struct {
		Wake bool   `json:"wake"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("ответ /wake не JSON: %s", raw)
	}
	return got.Wake, got.Text
}

func TestWakeRouteDetectsName(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	hub.SetWake(&WakeInfo{Available: true, Engine: "faster-whisper"})
	ts := newTestWS(t, hub)
	conn := dial(t, ts)
	send(t, conn, map[string]any{"type": "hello", "client": "test", "version": 1})
	if frame := recv(t, conn); frame["type"] != "welcome" {
		t.Fatalf("ожидается welcome, получен кадр %v", frame)
	}

	// Тот же сервер, что и WS — общий хаб, общий mux.
	srv, err := New(Options{
		Hub: hub, Log: testLog(), STT: &fakeWakeSTT{text: "Мира, приветы!"},
		STTDir: t.TempDir(), Wake: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler)
	t.Cleanup(hs.Close)

	waked, text := postWake(t, hs)
	if !waked {
		t.Fatalf("имя в сегменте обязано дать wake=true, текст %q", text)
	}
	// WakeMsg прилетел окну (хаб вещает всем подключённым).
	frame := recv(t, conn)
	if frame["type"] != "wake" {
		t.Fatalf("окно должно получить кадр wake, получен %v", frame)
	}
	if frame["heard"] != "Мира, приветы!" {
		t.Fatalf("heard не тот: %v", frame["heard"])
	}
}

func TestWakeRouteIgnoresOtherSpeech(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	srv, err := New(Options{
		Hub: hub, Log: testLog(), STT: &fakeWakeSTT{text: "картина мира"},
		STTDir: t.TempDir(), Wake: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	waked, text := postWake(t, ts)
	if waked {
		t.Fatalf("«картина мира» — не имя, wake=true на %q", text)
	}
}

func TestWakeRouteAbsentWhenDisabled(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	// STT есть, wake выключен (Wake=false в Options) — роута быть не должно.
	srv, err := New(Options{Hub: hub, Log: testLog(), STT: &fakeWakeSTT{}, STTDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+"/wake", "application/octet-stream", strings.NewReader("RIFF"+strings.Repeat("\x00", 200)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("без включённого wake роут /wake работать не должен: %s", body)
	}
}

func TestWelcomeCarriesWake(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	hub.SetWake(&WakeInfo{Available: true, Engine: "faster-whisper"})
	ts := newTestWS(t, hub)
	conn := dial(t, ts)
	send(t, conn, map[string]any{"type": "hello", "client": "test", "version": 1})
	frame := recv(t, conn)
	if frame["type"] != "welcome" {
		t.Fatalf("ожидается welcome, получен кадр %v", frame)
	}
	wake, ok := frame["wake"].(map[string]any)
	if !ok {
		t.Fatalf("в welcome нет поля wake: %v", frame)
	}
	if wake["available"] != true || wake["engine"] != "faster-whisper" {
		t.Fatalf("wake не тот: %v", wake)
	}
}
