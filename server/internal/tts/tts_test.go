package tts

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

// fakeCLI кладёт в указанный каталог псевдо-edge-tts: скрипт, который пишет
// «mp3» заданного размера. Тесты не зависят от сети и от настоящего пакета.
func fakeCLI(t *testing.T, dir string) {
	t.Helper()
	// Аргументы команды: --voice v --rate r --pitch p --text t --write-media f.
	// Файл-приёмник — последний токен: в bat %10 — это аргумент 1 и цифра 0,
	// поэтому забираем его циклом по %*.
	cmd := `@echo off
set last=
for %%f in (%*) do set last=%%f
set /a n=0
:loop
set /a n+=1
echo fake-mp3-line>> "%last%"
if %n% LSS 60 goto loop
exit /b 0
`
	if err := os.WriteFile(filepath.Join(dir, "edge-tts.cmd"), []byte(cmd), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestSynthProducesCachedFile(t *testing.T) {
	fake := t.TempDir()
	fakeCLI(t, fake)
	t.Setenv("PATH", fake)
	cache := t.TempDir()

	s := New(quietLog(), true, "ru-RU-SvetlanaNeural", "+0%", cache)
	if !s.Available() {
		t.Fatal("edge-tts из PATH должен быть доступен")
	}
	path, err := s.Synth(context.Background(), "Привет, это проверка.", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < 200 {
		t.Fatalf("mp3 слишком маленький: %d байт", fi.Size())
	}
	// Тот же текст второй раз — из кэша, тем же файлом.
	path2, err := s.Synth(context.Background(), "Привет, это проверка.", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if path2 != path {
		t.Fatalf("повтор той же фразы должен браться из кэша: %q vs %q", path2, path)
	}
	// Другой текст — другой файл.
	path3, err := s.Synth(context.Background(), "Другая фраза.", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if path3 == path {
		t.Fatal("разный текст не должен лежать в одном файле")
	}
	// Интонация (этап 9б): та же фраза, сказанная другим тоном, — ДРУГОЙ
	// файл кэша (радость ≠ покой), и повтор того же тона — из кэша.
	path4, err := s.Synth(context.Background(), "Привет, это проверка.", 12, 8)
	if err != nil {
		t.Fatal(err)
	}
	if path4 == path {
		t.Fatal("та же фраза другим тоном не должна лежать в одном файле")
	}
	path5, err := s.Synth(context.Background(), "Привет, это проверка.", 12, 8)
	if err != nil {
		t.Fatal(err)
	}
	if path5 != path4 {
		t.Fatal("повтор той же фразы тем же тоном обязан браться из кэша")
	}
}

// MoodParams — таблица интонаций (этап 9б): сон важнее полоски настроения.
func TestMoodParams(t *testing.T) {
	if r, p := MoodParams("high", false); r != 12 || p != 8 {
		t.Fatalf("радость: %+d/+%d", r, p)
	}
	if r, p := MoodParams("low", false); r != -12 || p != -6 {
		t.Fatalf("грусть: %+d/+%d", r, p)
	}
	if r, p := MoodParams("mid", false); r != 0 || p != 0 {
		t.Fatalf("покой: %+d/+%d", r, p)
	}
	if r, p := MoodParams("high", true); r != -25 || p != -10 {
		t.Fatalf("сон обязан быть тише самой радостной полоски: %+d/+%d", r, p)
	}
}

func TestDisabledAndMissing(t *testing.T) {
	fake := t.TempDir()
	fakeCLI(t, fake)
	t.Setenv("PATH", fake)
	if s := New(quietLog(), false, "", "", t.TempDir()); s.Available() {
		t.Fatal("выключенная озвучка не должна быть доступна")
	}
	t.Setenv("PATH", t.TempDir()) // каталог без edge-tts
	if s := New(quietLog(), true, "", "", t.TempDir()); s.Available() {
		t.Fatal("без edge-tts в PATH озвучка обязана честно быть недоступной")
	}
}

func TestEmptyTextRejected(t *testing.T) {
	fake := t.TempDir()
	fakeCLI(t, fake)
	t.Setenv("PATH", fake)
	s := New(quietLog(), true, "v", "+0%", t.TempDir())
	if _, err := s.Synth(context.Background(), "   ", 0, 0); err == nil {
		t.Fatal("пустой текст озвучивать нельзя")
	}
}
