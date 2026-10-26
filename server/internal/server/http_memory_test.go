package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"friend/internal/config"
	"friend/internal/memory"
	"friend/internal/state"
)

// newMemoryHTTP — сервер с подключённой памятью: маршрут /memory живёт
// только с хаба, у которого стоит SetMemory.
func newMemoryHTTP(t *testing.T) (*httptest.Server, *Hub, *memory.Store) {
	t.Helper()
	store, err := state.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: slog.LevelError}))
	hub := NewHub(log, store, config.Avatar{Name: "Мира", Width: 300, Height: 400}, nil)
	mem, err := memory.Open(t.TempDir() + "/memory.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mem.Close() })
	hub.SetMemory(mem)
	srv, err := New(Options{Hub: hub, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts, hub, mem
}

func TestMemoryGetListsFacts(t *testing.T) {
	ts, _, mem := newMemoryHTTP(t)
	base := time.Now().UTC()
	id1, _ := mem.Remember("Кота зовут Барсик", "user", base.Add(-24*time.Hour))
	id2, _ := mem.Remember("Любит джаз", "user", base)

	resp, err := http.Get(ts.URL + "/memory")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /memory: код %d", resp.StatusCode)
	}
	var out struct {
		Facts []struct {
			ID      int64  `json:"id"`
			Text    string `json:"text"`
			Created string `json:"created"`
		} `json:"facts"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 || len(out.Facts) != 2 {
		t.Fatalf("ожидал 2 факта,total=2, получил %d/%d", len(out.Facts), out.Total)
	}
	// свежие сверху
	if out.Facts[0].ID != id2 || out.Facts[1].ID != id1 {
		t.Fatalf("порядок не свежий сверху: %+v", out.Facts)
	}
	if out.Facts[0].Text != "Любит джаз" {
		t.Fatalf("текст факта не тот: %q", out.Facts[0].Text)
	}
}

func TestMemoryPagination(t *testing.T) {
	ts, _, mem := newMemoryHTTP(t)
	now := time.Now()
	for _, s := range []string{"факт один", "факт два", "факт три"} {
		if _, err := mem.Remember(s, "user", now); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := http.Get(ts.URL + "/memory?limit=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var full struct {
		Facts []struct {
			ID int64 `json:"id"`
		} `json:"facts"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&full); err != nil {
		t.Fatal(err)
	}
	if len(full.Facts) != 2 {
		t.Fatalf("limit=2 вернул %d фактов", len(full.Facts))
	}
	if full.Total != 3 {
		t.Fatalf("total должен быть 3, получил %d", full.Total)
	}
	// вторая страница — ровно тот, что не попал в первую
	resp2, err := http.Get(ts.URL + "/memory?limit=2&offset=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var page2 struct {
		Facts []struct {
			ID   int64  `json:"id"`
			Text string `json:"text"`
		} `json:"facts"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&page2); err != nil {
		t.Fatal(err)
	}
	if len(page2.Facts) != 1 || page2.Facts[0].Text != "факт один" {
		t.Fatalf("вторая страница: %+v", page2.Facts)
	}
}

func TestMemoryDeleteFlow(t *testing.T) {
	ts, _, mem := newMemoryHTTP(t)
	id, _ := mem.Remember("Кота зовут Барсик", "user", time.Now())

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/memory/"+itoa(id), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE существующего: код %d", resp.StatusCode)
	}

	// факт исчез из списка и из FTS-поиска
	respGet, err := http.Get(ts.URL + "/memory")
	if err != nil {
		t.Fatal(err)
	}
	defer respGet.Body.Close()
	var full struct {
		Facts []struct {
			ID int64 `json:"id"`
		} `json:"facts"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(respGet.Body).Decode(&full); err != nil {
		t.Fatal(err)
	}
	if full.Total != 0 {
		t.Fatalf("после удаления total=%d", full.Total)
	}
	found, err := mem.Recall("Барсик", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("FTS после удаления всё ещё находит: %+v", found)
	}

	// повторный DELETE того же id — 404, не падение
	req2, _ := http.NewRequest(http.MethodDelete, ts.URL+"/memory/"+itoa(id), nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("повторный DELETE: ожидал 404, получил %d", resp2.StatusCode)
	}

	// нечисловой id — 400
	req3, _ := http.NewRequest(http.MethodDelete, ts.URL+"/memory/abc", nil)
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("id=abc: ожидал 400, получил %d", resp3.StatusCode)
	}

	// метод не GET и не DELETE — 405
	resp4, err := http.Post(ts.URL+"/memory", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /memory: ожидал 405, получил %d", resp4.StatusCode)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// /healthz несёт язык персонажа (этап 13): им меряет самопроверка, что
// переключение из меню дошло до сервера.
func TestHealthzCarriesLanguage(t *testing.T) {
	ts, hub, _ := newMemoryHTTP(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Language != "ru" {
		t.Fatalf("language=%q, ожидал ru", out.Language)
	}
	hub.SetLanguage("en")
	resp2, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var out2 struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&out2); err != nil {
		t.Fatal(err)
	}
	if out2.Language != "en" {
		t.Fatalf("language=%q, ожидал en", out2.Language)
	}
}

// CORS (этап 15, оболочка Tauri): страница живёт в ассет-протоколе
// tauri://localhost и ходит на сервер чужим origin. Preflight OPTIONS
// обязан отвечать 204 с разрешениями; фактический запрос — с ACAO.
func TestCorsForTauriPage(t *testing.T) {
	ts, _, _ := newMemoryHTTP(t)

	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/memory", nil)
	req.Header.Set("Origin", "http://tauri.localhost")
	req.Header.Set("Access-Control-Request-Method", "DELETE")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS: код %d, ожидал 204", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight без ACAO: %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}

	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/memory", nil)
	req2.Header.Set("Origin", "http://tauri.localhost")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("GET без ACAO: %q", resp2.Header.Get("Access-Control-Allow-Origin"))
	}
}
