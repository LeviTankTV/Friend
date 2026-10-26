package server

import (
	"testing"
)

// Живая смена языка (этап 13): lang в канал → LangMsg в broadcast, welcome
// нового окна несёт новый язык, хаб помнит новый язык.
func TestLangSwitchOnWS(t *testing.T) {
	srv, _, hub := newTestServerWith(t, nil)
	conn := dial(t, srv)
	send(t, conn, map[string]any{"type": "hello", "client": "test"})
	w := recv(t, conn)
	if w["type"] != "welcome" {
		t.Fatalf("ожидал welcome, получил %v", w["type"])
	}
	av, _ := w["avatar"].(map[string]any)
	if av == nil || av["language"] != "ru" {
		t.Fatalf("welcome должен нести ru, получил %+v", av)
	}

	send(t, conn, map[string]any{"type": "lang", "language": "en"})
	langMsg := false
	for k := 0; k < 5 && !langMsg; k++ {
		msg := recv(t, conn)
		langMsg = msg["type"] == "lang"
		if langMsg && msg["language"] != "en" {
			t.Fatalf("broadcast lang: %+v", msg)
		}
	}
	if !langMsg {
		t.Fatal("broadcast lang не дошёл до окна")
	}
	if hub.Language() != "en" {
		t.Fatalf("язык хаба %s, ожидал en", hub.Language())
	}

	// Второе окно: его welcome уже на новом языке.
	conn2 := dial(t, srv)
	send(t, conn2, map[string]any{"type": "hello", "client": "test2"})
	w2 := recv(t, conn2)
	av2, _ := w2["avatar"].(map[string]any)
	if av2 == nil || av2["language"] != "en" {
		t.Fatalf("welcome нового окна language=%+v, ожидал en", av2)
	}
}

// Матчер её имени пересобирается вместе с языком и именем (этап 13):
// русское «Мира» слышна кириллицей, английская «Mira» — латиницей.
func TestLangSwitchRebuildsWakeMatcher(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	if !hub.WakeMatch("мира, ты тут?") {
		t.Fatal("ru-матчер не услышал имя")
	}
	hub.SetLanguage("en")
	hub.SetName("Mira")
	if !hub.WakeMatch("hey Mira, what time is it") {
		t.Fatal("en-матчер не услышал латинское имя")
	}
	if hub.WakeMatch("мира, ты тут?") {
		t.Fatal("en-матчер услышал кириллическое имя — пересборка не сработала")
	}
}

// Хук пересборки вызывается уже на новом языке: main.go перечитывает
// характер, опираясь на код, и язык к тому моменту обязан быть свежим.
func TestLangHookSeesNewLanguage(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	seen := ""
	hub.SetLangHook(func(code string) { seen = code + "/" + hub.Language() })
	hub.SetLanguage("en")
	if seen != "en/en" {
		t.Fatalf("хук увидел %q, ожидал en/en", seen)
	}
}

// Неизвестный код = ru (как i18n.For), а не сбой.
func TestLangUnknownCodeFallsBackRu(t *testing.T) {
	_, _, hub := newTestServerWith(t, nil)
	hub.SetLanguage("de")
	if hub.Language() != "ru" {
		t.Fatalf("язык %s, ожидал ru", hub.Language())
	}
}
