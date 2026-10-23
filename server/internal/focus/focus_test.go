package focus

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyKnown(t *testing.T) {
	// Имя процесса из Windows приходит с путем и .exe — ключ должен
	// пережить и то, и другое.
	kind, label := Classify(`C:\Program Files\Google\Chrome\Application\chrome.exe`)
	if kind != KindBrowser || !strings.Contains(label, "браузер") {
		t.Fatalf("chrome: kind=%s label=%s", kind, label)
	}
	if kind, label = Classify("Goland64.exe"); kind != KindCode || !strings.Contains(label, "GoLand") {
		t.Fatalf("goland: kind=%s label=%s", kind, label)
	}
	if kind, _ = Classify("electron.exe"); kind != KindCompanion {
		t.Fatalf("окно Миры не должно считаться приложением человека: %s", kind)
	}
	if kind, _ = Classify("explorer.exe"); kind != KindNone {
		t.Fatalf("рабочий стол не повод говорить: %s", kind)
	}
}

func TestClassifyUnknownIsOtherWithWarning(t *testing.T) {
	kind, label := Classify("C:\\games\\shaper\\Shaper.exe")
	if kind != KindOther {
		t.Fatalf("неизвестное приложение обязано пройти как other, а не как выдуманное %s", kind)
	}
	if label != "shaper" {
		t.Fatalf("label неизвестного — имя процесса как есть, получено %q", label)
	}
}

func TestCleanTitle(t *testing.T) {
	got := CleanTitle("  main.go\t—\nVisual Studio Code ")
	if got != "main.go — Visual Studio Code" {
		t.Fatalf("заголовок не причёсан: %q", got)
	}
	long := CleanTitle(strings.Repeat("a", 500))
	if n := len([]rune(long)); n > MaxTitleRunes+1 {
		t.Fatalf("заголовок не обрезан: %d знаков", n)
	}
	if !strings.HasSuffix(long, "…") {
		t.Fatalf("обрезанный заголовок обязан заканчиваться многоточием: %q", long)
	}
}

func TestTrackerTimeCountsFromAppSwitch(t *testing.T) {
	tr := New()
	t0 := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)

	tr.Update("code.exe", "a.go — VS Code", t0)
	tr.Update("code.exe", "b.go — VS Code", t0.Add(40*time.Minute)) // сменилась вкладка
	s, ok := tr.Snapshot()
	if !ok {
		t.Fatal("трекер обязан помнить приложение")
	}
	if !s.Since.Equal(t0) {
		t.Fatalf("смена заголовка не обнуляет счёт: since=%v, ожидалось %v", s.Since, t0)
	}

	tr.Update("chrome.exe", "ТАСС", t0.Add(80*time.Minute)) // сменилось приложение
	s, _ = tr.Snapshot()
	if s.Kind != KindBrowser || s.Process != "chrome.exe" {
		t.Fatalf("трекер не перешёл на новое приложение: %+v", s)
	}
	if !s.Since.Equal(t0.Add(80 * time.Minute)) {
		t.Fatalf("счёт «уже» обязан начаться с перехода: %v", s.Since)
	}
}

func TestTrackerEmptyAppIgnored(t *testing.T) {
	tr := New()
	tr.Update("", "что-то", time.Now())
	if _, ok := tr.Snapshot(); ok {
		t.Fatal("пустое приложение не должно появляться в снимке")
	}
}

func TestLineForKnownApp(t *testing.T) {
	now := time.Date(2026, 10, 22, 15, 30, 0, 0, time.UTC)
	line := Line(Snapshot{
		Process: "goland64.exe",
		Title:   "main.go — GoLand",
		Kind:    KindCode,
		Label:   "GoLand (IDE для Go)",
		Since:   now.Add(-2*time.Hour - 15*time.Minute),
	}, now, "ru")
	for _, want := range []string{
		"GoLand (IDE для Go)",
		"main.go — GoLand",
		"уже 2 ч 15 мин",
		"не перебивай",
		"не описывай и не угадывай",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("в строке промпта нет %q:\n%s", want, line)
		}
	}
}

func TestLineSilentForDeskAndOwnWindow(t *testing.T) {
	now := time.Now()
	if line := Line(Snapshot{Process: "explorer.exe", Kind: KindNone}, now, "ru"); line != "" {
		t.Fatalf("рабочий стол не строка контекста: %q", line)
	}
	if line := Line(Snapshot{Process: "electron.exe", Kind: KindCompanion}, now, "ru"); line != "" {
		t.Fatalf("окно самой Миры не строка контекста: %q", line)
	}
}

func TestLineNoFakeMinutes(t *testing.T) {
	// «уже 30 секунд» звучит слежкой; минуты упоминаем только от одной.
	now := time.Now()
	line := Line(Snapshot{Process: "chrome.exe", Kind: KindBrowser, Label: "Chrome (браузер)", Since: now.Add(-30 * time.Second)}, now, "ru")
	if strings.Contains(line, "уже") {
		t.Fatalf("меньше минуты упоминать незачем: %q", line)
	}
	line = Line(Snapshot{Process: "chrome.exe", Kind: KindBrowser, Label: "Chrome (браузер)", Since: now.Add(-90 * time.Minute)}, now, "ru")
	if !strings.Contains(line, "уже 1 ч 30 мин") {
		t.Fatalf("полтора часа читаются как «1 ч 30 мин»: %q", line)
	}
}

func TestLineUnknownAppForbidsGuessing(t *testing.T) {
	line := Line(Snapshot{Process: "shaper.exe", Kind: KindOther, Label: "shaper", Since: time.Now().Add(-time.Hour)}, time.Now(), "ru")
	if !strings.Contains(line, "не выдумывай") {
		t.Fatalf("о неизвестном приложении промпт обязан запретить догадки: %q", line)
	}
}
