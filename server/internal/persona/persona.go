// Package persona — характер компаньона: кто она, как говорит, что любит.
//
// Личность живёт ОТДЕЛЬНЫМ файлом (mira.json), а не строкой в коде: характер —
// это то, что пользователь правит под себя, а не то, что программист
// пересобирает. Файл по умолчанию встроен в бинарник (go:embed), поэтому
// собранный сервер работает и без него рядом; свой файл можно положить на диск
// и указать в конфиге (persona.file) — тогда он ПЕРЕКРЫВАЕТ встроенный целиком.
//
// Системный промпт НЕ хранится в файле. Файл отвечает на вопрос «кто она»,
// а сборка промпта (время суток, примеры речи, запреты) — задача кода: иначе
// правка формата промпта требовала бы правки характера у каждого пользователя.
package persona

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"friend/internal/i18n"
)

//go:embed mira.json
var embeddedDefault []byte

//go:embed mira.en.json
var embeddedEnglish []byte

// Example — пример речи: как она отвечает в похожей ситуации. Показывается
// модели прямо в системном промпте: примеры держат тон лучше, чем описание тона.
type Example struct {
	User  string `json:"user"`
	Reply string `json:"reply"`
}

// Profile — характер целиком. Все поля, кроме Name, необязательные: файл
// характера пишет человек, и он не обязан заполнять всё.
type Profile struct {
	Name string `json:"name"`
	Role string `json:"role"`
	// Gender решает род речи о себе («я пришла» или «я пришёл»). Это не
	// украшение: русский язык выдаёт ошибку рода сразу, а модель, которой пол
	// не назван, выбирает род случайно и меняет его посреди разговора.
	Gender   string    `json:"gender"` // female | male (пусто — не уточняется)
	Tone     string    `json:"tone"`
	Language string    `json:"language"`
	Style    []string  `json:"style"`
	Habits   []string  `json:"habits"`
	Likes    []string  `json:"likes"`
	Dislikes []string  `json:"dislikes"`
	Examples []Example `json:"examples"`
	Rules    []string  `json:"rules"`
}

// Default возвращает встроенный характер (mira.json из этого пакета).
func Default() (Profile, error) {
	var p Profile
	if err := json.Unmarshal(embeddedDefault, &p); err != nil {
		return Profile{}, fmt.Errorf("встроенный характер не читается: %w", err)
	}
	return p, nil
}

// DefaultFor — встроенный характер по языку (этап 10): «en» — английская
// Mira (mira.en.json), всё остальное — русский файл.
func DefaultFor(lang string) (Profile, error) {
	var raw []byte
	if strings.EqualFold(strings.TrimSpace(lang), "en") {
		raw = embeddedEnglish
	} else {
		raw = embeddedDefault
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, fmt.Errorf("встроенный характер не читается: %w", err)
	}
	return p, nil
}

// EmbeddedJSON отдаёт встроенный файл характера как есть — для команды
// -dump-persona: человек копирует его к себе и правит, ничего не пересобирая.
func EmbeddedJSON() []byte {
	out := make([]byte, len(embeddedDefault))
	copy(out, embeddedDefault)
	return out
}

// EmbeddedJSONFor — то же по языку (этап 10): -dump-persona=en отдаёт
// английский характер.
func EmbeddedJSONFor(lang string) []byte {
	if strings.EqualFold(strings.TrimSpace(lang), "en") {
		out := make([]byte, len(embeddedEnglish))
		copy(out, embeddedEnglish)
		return out
	}
	return EmbeddedJSON()
}

// LoadFor читает характер по языку (этап 10): явный путь перекрывает всё,
// иначе берётся встроенный файл для языка персоны («en» — mira.en.json).
// Поле language профиля приравнивается к lang: конфиг — единственный
// источник правды о языке, файл характера может оказаться любым.
func LoadFor(path, lang string) (Profile, string, error) {
	if lang != "en" {
		lang = "ru"
	}
	var p Profile
	var note string
	var err error
	if strings.TrimSpace(path) != "" {
		p, note, err = Load(path)
	} else {
		p, err = DefaultFor(lang)
		note = "характер: встроенный (" + p.Name + ", " + lang + ")"
	}
	if err != nil {
		return Profile{}, "", err
	}
	p.Language = lang
	return p, note, nil
}

// Load читает характер: явный путь, иначе встроенный.
//
// Возвращает сам характер, пояснение для лога и ошибку. Ошибка бывает только
// при ЯВНО указанном файле: битый или отсутствующий файл характера — это
// опечатка в пути, о которой надо сказать вслух, а не молча работать «не той»
// личностью. Без пути ошибки быть не может: встроенный файл всегда на месте.
func Load(path string) (Profile, string, error) {
	if strings.TrimSpace(path) == "" {
		p, err := Default()
		if err != nil {
			return Profile{}, "", err
		}
		return p, "характер: встроенный (" + p.Name + ")", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, "", fmt.Errorf("характер %s: %w", path, err)
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, "", fmt.Errorf("характер %s: %w", path, err)
	}
	if err := p.Validate(); err != nil {
		return Profile{}, "", fmt.Errorf("характер %s: %w", path, err)
	}
	return p, "характер: " + path + " (" + p.Name + ")", nil
}

// Validate проверяет характер на то, без чего он перестаёт быть характером.
func (p Profile) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("не задано имя персонажа (name)")
	}
	return nil
}

// DayPart — часть суток. Тон компаньона зависит от неё (этап 4 доведёт это до
// триггеров планировщика: «сменилось время суток» — повод заговорить).
type DayPart string

const (
	Morning DayPart = "morning" // 06:00–12:00
	Day     DayPart = "day"     // 12:00–18:00
	Evening DayPart = "evening" // 18:00–23:00
	Night   DayPart = "night"   // 23:00–06:00
)

// PartOf определяет часть суток по локальному времени.
func PartOf(t time.Time) DayPart {
	switch h := t.Hour(); {
	case h >= 6 && h < 12:
		return Morning
	case h >= 12 && h < 18:
		return Day
	case h >= 18 && h < 23:
		return Evening
	default:
		return Night
	}
}

// Label — как называть часть суток в промпте (русский; для других языков —
// LabelIn, этап 10).
func (p DayPart) Label() string { return p.LabelIn(&i18n.RU) }

// LabelIn — название части суток на языке словаря.
func (p DayPart) LabelIn(d *i18n.Dict) string {
	switch p {
	case Morning:
		return d.DayLabelMorning
	case Day:
		return d.DayLabelDay
	case Evening:
		return d.DayLabelEvening
	default:
		return d.DayLabelNight
	}
}

// Tone — как звучать в эту часть суток (русский; для других языков — ToneIn).
// Это не «вежливость», а причина: утром человек разгоняется, ночью он
// уставший — и разговор в 3 часа ночи не должен быть таким же бодрым, как в
// 10 утра.
func (p DayPart) Tone() string { return p.ToneIn(&i18n.RU) }

// ToneIn — тон части суток на языке словаря.
func (p DayPart) ToneIn(d *i18n.Dict) string {
	switch p {
	case Morning:
		return d.DayToneMorning
	case Day:
		return d.DayToneDay
	case Evening:
		return d.DayToneEvening
	default:
		return d.DayToneNight
	}
}

// SystemPrompt собирает системный промпт для модели на момент времени t.
//
// Правило: здесь есть только то, что влияет на ответ. Промпт отвечает на три
// вопроса — кто ты (личность), как звучать сейчас (время суток, язык) и чего
// делать нельзя (запреты).
func (p Profile) SystemPrompt(t time.Time) string {
	var b strings.Builder
	part := PartOf(t)
	d := i18n.For(p.Language)

	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "Мира"
	}
	fmt.Fprintf(&b, d.PIntro, name)
	if role := strings.TrimSpace(p.Role); role != "" {
		b.WriteString(role)
		b.WriteString(" ")
	}
	b.WriteString(d.PLives)

	if tone := strings.TrimSpace(p.Tone); tone != "" {
		fmt.Fprintf(&b, d.PCharacter, tone)
	}

	if line := genderRule(d, p.Gender); line != "" {
		fmt.Fprintf(&b, "\n%s\n", line)
	}

	fmt.Fprintf(&b, d.PNow, part.LabelIn(d), t.Format("15:04"), part.ToneIn(d))

	if lang := langName(d, p.Language); lang != "" {
		fmt.Fprintf(&b, d.PLanguage, lang)
	}

	writeList := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s\n", title)
		for _, it := range items {
			fmt.Fprintf(&b, "— %s\n", strings.TrimSpace(it))
		}
	}
	writeList(d.PStyleTitle, p.Style)
	writeList(d.PHabitsTitle, p.Habits)
	writeList(d.PLikesTitle, p.Likes)
	writeList(d.PDislikeTitle, p.Dislikes)

	if len(p.Examples) > 0 {
		fmt.Fprintf(&b, "\n%s\n", d.PExamplesBody)
		for _, ex := range p.Examples {
			fmt.Fprintf(&b, d.PExamplesPair, strings.TrimSpace(ex.User), strings.TrimSpace(ex.Reply))
		}
	}
	writeList(d.PRulesTitle, p.Rules)

	b.WriteString(d.PFinal)
	return b.String()
}

// genderRule — прямое указание про род речи. Формулировка нарочно с
// примерами: модель, которой просто сказать «ты девушка», всё равно норовит
// ответить «я пришёл» — род в русском задаётся формами слов, а не описанием.
func genderRule(d *i18n.Dict, g string) string {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case "female", "f", "ж", "женский", "девушка":
		return d.GenderFemale
	case "male", "m", "м", "мужской", "парень":
		return d.GenderMale
	default:
		return ""
	}
}

// langName — строка «Язык: …» на языке словаря.
func langName(d *i18n.Dict, code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "", "ru":
		return d.LangNameRu
	case "en":
		return d.LangNameEn
	default:
		return d.LangNameAny
	}
}

// --- Галерея характеров (этап 16): файлы рядом с твоим mira.json -----------

// PersonaInfo — запись галереи: файл и кто в нём.
type PersonaInfo struct {
	File     string `json:"file"`
	Name     string `json:"name"`
	Language string `json:"language"`
}

// ListDir — все *.json в каталоге характеров: имя и язык каждого.
// Битые файлы и файлы без имени пропускаются: галерея — витрина, а не аудит.
// Каталога нет — галерея пуста, и это честно (характеры встроены).
func ListDir(dir string) ([]PersonaInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []PersonaInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var head struct {
			Name     string `json:"name"`
			Language string `json:"language"`
		}
		if json.Unmarshal(raw, &head) != nil || head.Name == "" {
			continue
		}
		out = append(out, PersonaInfo{File: e.Name(), Name: head.Name, Language: head.Language})
	}
	return out, nil
}
