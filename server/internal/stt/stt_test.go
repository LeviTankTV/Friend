package stt

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(&strings.Builder{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

// wav16kMono — тихий WAV: проверка цепочки python+faster-whisper+модель без
// голоса (и заодно формат-контракт, которым обязана страница).
func wav16kMono(t *testing.T, path string, seconds int) {
	t.Helper()
	data := make([]byte, 44+2*16000*seconds)
	copy(data, []byte("RIFF"))
	be32 := func(off int, v int) {
		data[off] = byte(v); data[off+1] = byte(v >> 8); data[off+2] = byte(v >> 16); data[off+3] = byte(v >> 24)
	}
	be32(4, len(data)-8)
	copy(data[8:], []byte("WAVEfmt "))
	be32(16, 16)
	data[20] = 1 // PCM
	data[22] = 1 // mono
	be32(24, 16000)
	be32(28, 32000)
	data[32] = 2
	data[34] = 16
	copy(data[36:], []byte("data"))
	be32(40, 2*16000*seconds)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChainWithRealWhisper(t *testing.T) {
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python не установлен — цепочку не проверить")
	}
	model := os.Getenv("FRIEND_TEST_STT_MODEL")
	if model == "" {
		model = filepath.Join(os.Getenv("APPDATA"), "Friend", "stt-model", "faster-whisper-base")
	}
	if _, err := os.Stat(filepath.Join(model, "model.bin")); err != nil {
		t.Skipf("модель faster-whisper не найдена (%s)", model)
	}
	if out, err := exec.Command("python", "-c", "import faster_whisper").CombinedOutput(); err != nil {
		t.Skipf("пакет faster-whisper не импортируется: %s", out)
	}

	dir := t.TempDir()
	s := New(quietLog(), true, model, dir)
	if !s.Available() {
		t.Fatal("при python + модели служба обязана быть доступна")
	}
	wav := filepath.Join(dir, "silence.wav")
	wav16kMono(t, wav, 1)
	text, err := s.Recognize(context.Background(), wav)
	if err != nil {
		t.Fatalf("цепочка python+faster-whisper+модель не прошла: %v", err)
	}
	if text != "" {
		t.Fatalf("в тишине речи быть не должно, получено %q", text)
	}
}

func TestUnavailableGracefully(t *testing.T) {
	if s := New(quietLog(), false, t.TempDir(), t.TempDir()); s.Available() {
		t.Fatal("выключенный ввод не должен быть доступен")
	}
	// Модель отсутствует (нет model.bin) — даже с python служба недоступна.
	if _, err := exec.LookPath("python"); err == nil {
		if s := New(quietLog(), true, filepath.Join(t.TempDir(), "нет-такой"), t.TempDir()); s.Available() {
			t.Fatal("без модели на диске ввод обязан быть недоступен")
		}
	}
}

func TestScriptIsSelfContained(t *testing.T) {
	data, err := assets.ReadFile("assets/whisper_recognize.py")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "WhisperModel") {
		t.Fatal("скрипт распознавания неполный")
	}
	// Проверка, что скрипт синтаксически живой (python его компилирует).
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python нет — компиляцию не проверить")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "s.py")
	os.WriteFile(p, data, 0o644)
	out, err := exec.Command("python", "-c", "import ast; ast.parse(open(r'"+p+"', encoding='utf-8').read())").CombinedOutput()
	if err != nil {
		t.Fatalf("python-скрипт не компилируется: %s", out)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	// Формат, которым скрипт обязан отвечать: Go-парсер живёт на нём.
	raw, _ := json.Marshal(recognized{Text: "привет мира"})
	var back recognized
	if err := json.Unmarshal(raw, &back); err != nil || back.Text != "привет мира" {
		t.Fatalf("формат ответа сломан: %s → %+v", raw, back)
	}
}
