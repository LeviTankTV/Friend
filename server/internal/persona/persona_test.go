package persona

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"friend/internal/i18n"
)

// Встроенный характер обязан читаться и быть похожим на характер, а не на
// заготовку: без имени, тона и запретов промпт превращается в пустую вежливость.
func TestDefaultProfileIsUsable(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatalf("встроенный характер не прочитался: %v", err)
	}
	if strings.TrimSpace(p.Name) == "" {
		t.Fatal("у встроенного характера нет имени")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("встроенный характер не проходит проверку: %v", err)
	}
	for _, part := range []struct {
		name string
		len  int
	}{
		{"tone", len(p.Tone)},
		{"style", len(p.Style)},
		{"likes", len(p.Likes)},
		{"dislikes", len(p.Dislikes)},
		{"examples", len(p.Examples)},
		{"rules", len(p.Rules)},
	} {
		if part.len == 0 {
			t.Errorf("во встроенном характере пусто: %s", part.name)
		}
	}
}

func TestPartOfBoundaries(t *testing.T) {
	cases := []struct {
		hour int
		want DayPart
	}{
		{0, Night}, {5, Night}, {6, Morning}, {11, Morning},
		{12, Day}, {17, Day}, {18, Evening}, {22, Evening},
		{23, Night},
	}
	for _, c := range cases {
		got := PartOf(time.Date(2026, 10, 16, c.hour, 30, 0, 0, time.Local))
		if got != c.want {
			t.Errorf("%02d:30 → %q, ожидалось %q", c.hour, got, c.want)
		}
	}
}

// Тон в промпте должен МЕНЯТЬСЯ вместе с частью суток: иначе «адаптация под
// время суток» существует только в конфиге, а не в ответах модели.
func TestSystemPromptChangesWithDayPart(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	night := p.SystemPrompt(time.Date(2026, 10, 16, 3, 0, 0, 0, time.Local))
	morning := p.SystemPrompt(time.Date(2026, 10, 16, 9, 0, 0, 0, time.Local))
	if night == morning {
		t.Fatal("промпт ночью и утром одинаковый")
	}
	if !strings.Contains(night, "ночь") || !strings.Contains(morning, "утро") {
		t.Fatal("в промпте не названа часть суток")
	}
}

func TestSystemPromptCarriesIdentity(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	txt := p.SystemPrompt(time.Now())
	for _, want := range []string{p.Name, "Не ассистент", p.Examples[0].Reply, "Запреты"} {
		if !strings.Contains(txt, want) {
			t.Errorf("в системном промпте нет %q", want)
		}
	}
	// Время должно попадать в промпт: иначе модель не знает, день сейчас или ночь.
	if !strings.Contains(txt, time.Now().Format("15:04")) {
		t.Error("в промпте нет текущего времени")
	}
}

// Свой файл характера перекрывает встроенный ЦЕЛИКОМ — иначе «я убрал
// церемонии» превращалось бы в «убрал, но старые остались».
func TestLoadOwnFileOverridesEmbedded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "own.json")
	own := Profile{
		Name:     "Тестовый характер",
		Tone:     "сухо и коротко",
		Language: "en",
		Style:    []string{"одно слово"},
		Rules:    []string{"не шутить"},
	}
	raw, err := json.Marshal(own)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	p, note, err := Load(path)
	if err != nil {
		t.Fatalf("свой файл не загрузился: %v", err)
	}
	if p.Name != own.Name {
		t.Errorf("имя %q, ожидалось %q", p.Name, own.Name)
	}
	if !strings.Contains(note, path) {
		t.Errorf("в пояснении нет пути к файлу: %q", note)
	}
	// Встроенные примеры речи не должны просачиваться в свой характер.
	if len(p.Examples) != 0 {
		t.Errorf("в свой характер попали встроенные примеры: %+v", p.Examples)
	}
}

// Опечатка в пути — ошибка, а не «тихо работаю встроенным»: иначе правки
// характера просто не применяются, и это выглядит как «промпт не работает».
func TestLoadBrokenFileIsError(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "нет-такого.json")); err == nil {
		t.Fatal("отсутствующий файл характера не дал ошибки")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{это не json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(bad); err == nil {
		t.Fatal("битый файл характера не дал ошибки")
	}
	noName := filepath.Join(dir, "noname.json")
	if err := os.WriteFile(noName, []byte(`{"tone":"тихо"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(noName); err == nil {
		t.Fatal("характер без имени не дал ошибки")
	}
}

// Род речи о себе: в русском он виден сразу, и модель без указания пола меняет
// его случайно. Проверяем и встроенный характер, и саму сборку промпта.
func TestGenderRuleInPrompt(t *testing.T) {
	p, _, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Gender != "female" {
		t.Errorf("пол встроенного характера %q, ожидался female", p.Gender)
	}
	prompt := p.SystemPrompt(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	if !strings.Contains(prompt, "женском роде") {
		t.Errorf("в промпте нет требования про женский род:\n%s", prompt)
	}
	for _, form := range []string{"я пришла", "я рада", "я устала"} {
		if !strings.Contains(prompt, form) {
			t.Errorf("в промпте нет примера женского рода %q", form)
		}
	}
}

// Мужской пол тоже описан — на случай другого персонажа: правило общее, а не
// пришитое к Мире.
func TestMaleRule(t *testing.T) {
	if got := genderRule(&i18n.RU, "male"); !strings.Contains(got, "мужском роде") {
		t.Errorf("правило для male: %q", got)
	}
	if got := genderRule(&i18n.RU, ""); got != "" {
		t.Errorf("без пола правило не должно ничего добавлять, получено %q", got)
	}
}

func TestEmptyPathUsesEmbedded(t *testing.T) {
	p, note, err := Load("")
	if err != nil {
		t.Fatalf("пустой путь должен брать встроенный характер: %v", err)
	}
	if p.Name == "" || !strings.Contains(note, "встроенный") {
		t.Fatalf("неожиданный результат: имя=%q пояснение=%q", p.Name, note)
	}
}
