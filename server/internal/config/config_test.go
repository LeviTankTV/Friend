package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadMissingFileUsesDefaults — конфиг не обязателен: первый запуск должен
// работать без файла (иначе «поставил и не запускается»).
func TestLoadMissingFileUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, note, err := Load(filepath.Join(dir, "нет-такого.json"))
	if err != nil {
		t.Fatalf("Load без файла: %v", err)
	}
	if !strings.Contains(note, "значениях по умолчанию") {
		t.Errorf("note = %q, ожидалось упоминание значений по умолчанию", note)
	}
	def := Default()
	if cfg.Server.Addr != def.Server.Addr || cfg.Avatar.Name != def.Avatar.Name {
		t.Errorf("значения по умолчанию не применились: %+v", cfg)
	}
	// Путь к состоянию обязан быть непустым и не рядом с бинарником.
	if cfg.Paths.StateFile == "" || !strings.Contains(cfg.Paths.StateFile, "Friend") {
		t.Errorf("state_file = %q, ожидался путь в каталоге пользователя", cfg.Paths.StateFile)
	}
	// Память (SQLite) — в том же каталоге пользователя.
	if cfg.Paths.MemoryFile == "" || !strings.Contains(cfg.Paths.MemoryFile, "Friend") {
		t.Errorf("memory_file = %q, ожидался путь в каталоге пользователя", cfg.Paths.MemoryFile)
	}
	// Внешний арт — тоже в каталоге пользователя и в папке sprites: человек
	// подкладывает кадры в понятное место.
	if cfg.Paths.SpritesDir == "" || !strings.Contains(cfg.Paths.SpritesDir, "sprites") {
		t.Errorf("sprites_dir = %q, ожидалась папка sprites в каталоге пользователя", cfg.Paths.SpritesDir)
	}
}

// Путь к внешнему арту настраивается файлом и перекрывается окружением.
func TestSpritesDirFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body, err := json.Marshal(map[string]any{
		"paths": map[string]any{"sprites_dir": filepath.Join(dir, "art")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.SpritesDir != filepath.Join(dir, "art") {
		t.Fatalf("sprites_dir из файла не прочитался: %q", cfg.Paths.SpritesDir)
	}
	t.Setenv("FRIEND_SPRITES_DIR", filepath.Join(dir, "art-env"))
	cfg, _, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.SpritesDir != filepath.Join(dir, "art-env") {
		t.Fatalf("окружение обязано перекрывать файл: %q", cfg.Paths.SpritesDir)
	}
}

// FRIEND_MEMORY_FILE перекрывает путь к памяти — как и путь к состоянию.
func TestMemoryFileEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"paths":{"memory_file":"from-file.db"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.MemoryFile != "from-file.db" {
		t.Fatalf("memory_file из файла не прочитался: %q", cfg.Paths.MemoryFile)
	}
	t.Setenv("FRIEND_MEMORY_FILE", filepath.Join(dir, "from-env.db"))
	cfg, _, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.MemoryFile != filepath.Join(dir, "from-env.db") {
		t.Fatalf("окружение обязано перекрывать файл: %q", cfg.Paths.MemoryFile)
	}
}

// TestLoadFileThenEnvOverrides — переменные окружения перекрывают файл.
func TestLoadFileThenEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
	  "server": {"addr": "127.0.0.1:1111"},
	  "avatar": {"name": "Из файла", "width": 100, "height": 200},
	  "log": {"level": "warn"}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FRIEND_ADDR", "127.0.0.1:2222")
	t.Setenv("FRIEND_NAME", "Из окружения")

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:2222" {
		t.Errorf("addr = %q, окружение должно перекрывать файл", cfg.Server.Addr)
	}
	if cfg.Avatar.Name != "Из окружения" {
		t.Errorf("name = %q, окружение должно перекрывать файл", cfg.Avatar.Name)
	}
	if cfg.Avatar.Width != 100 || cfg.Avatar.Height != 200 {
		t.Errorf("поля файла потерялись: %+v", cfg.Avatar)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("log.level = %q, want warn", cfg.Log.Level)
	}
	// Ключа API в конфиге быть не должно — только имя переменной окружения.
	if strings.Contains(raw, "api_key") {
		t.Error("в конфиге не должно быть поля с ключом API")
	}
}

// TestConfigRejectsBrokenValues — валидация ловит то, что ломает запуск.
func TestConfigRejectsBrokenValues(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*Config)
		words string
	}{
		{"пустой адрес", func(c *Config) { c.Server.Addr = "  " }, "server.addr"},
		{"нулевой размер окна", func(c *Config) { c.Avatar.Width = 0 }, "avatar.width"},
		{"плохой уровень логов", func(c *Config) { c.Log.Level = "тихо" }, "log.level"},
		{"нулевой тик", func(c *Config) { c.Proactive.TickSeconds = 0 }, "tick_seconds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mut(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("валидация пропустила некорректный конфиг")
			}
			if !strings.Contains(err.Error(), tc.words) {
				t.Errorf("ошибка %q не называет поле %q", err, tc.words)
			}
		})
	}
	if err := Default().Validate(); err != nil {
		t.Errorf("конфиг по умолчанию не проходит собственную валидацию: %v", err)
	}
}

// TestLoadBrokenJSON — битый JSON это ошибка запуска, а не «тихо на дефолтах»:
// иначе человек правит конфиг и не понимает, почему ничего не меняется.
func TestLoadBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{ это не json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("битый JSON принят без ошибки")
	}
}
