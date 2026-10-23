// Package wake узнаёт её имя в расшифрованном речевом сегменте (этап 7,
// этап 10 — имя и язык из конфига, а не зашитая «Мира»).
//
// Распознанный whisper текст неточен, и неточности разные по языкам:
//   - русский: whisper иногда съедает последний гласный имени («Мира» →
//     «Мир»), а на деградированной записи сыпется латинскими литерами-двойниками
//     («Mup» вместо «Мир») и украинской «і». Ложные друзья — падежи слова
//     «мир» («картина мира») и «мир» после предлога;
//   - английский: имя — просто слово с границами («Mira, hello»), ложные
//     друзья — продолжения буквами («miracle», «mirage», «miranda»).
//
// Приватность прежняя (этап 7): сегмент расшифровывается локально, не имя —
// текст выбрасывается, сервер про него ничего не хранит.
package wake

import (
	"regexp"
	"strings"
)

// Matcher — имя, собранное в правила узнавания.
type Matcher struct {
	lang      string
	full      string          // имя в нижнем регистре (ё→е)
	re        *regexp.Regexp  // основное совпадение (начало фразы или после запятой)
	genitive  *regexp.Regexp  // ru: полное имя в конце фразы — падеж слова, не обращение
	commaFull *regexp.Regexp  // ru: «привет, Мира!» — обращение, падежом не считается
	shortWord *regexp.Regexp  // ru: предлог + короткая форма («в мир») — не обращение
	latin     *strings.Replacer // ru: латинские двойники → кириллица
	latinRun  *regexp.Regexp    // ru: есть ли латиница вообще
}

// ruVowels — гласные, которыми русские имена заканчиваются чаще всего;
// последняя из них whisper и съедает.
const ruVowels = "аеиоуыэюя"

// ruLatinRunes — латиница в «русском» тексте = деградированная запись.
var ruLatinRunes = regexp.MustCompile(`[abceHKoepcxyAKMEHTiіІ]`)

// ruLatin — карта визуальных двойников латиницы→кириллицы: переводится весь
// текст, частичная замена убивает имя («Miра» с латинской i остаётся
// не-именем). «u»→«и»: в битой кириллице u — это «и» («Mup» = «Мир», не
// «Мур»); «u» как «ю» whisper на русском пишет через «y».
var ruLatin = strings.NewReplacer(
	"A", "А", "a", "а", "B", "В", "b", "в", "C", "С", "c", "с", "E", "Е", "e", "е",
	"H", "Н", "h", "н", "I", "И", "i", "и", "J", "Й", "j", "й", "K", "К", "k", "к",
	"L", "Л", "l", "л", "M", "М", "m", "м", "N", "Н", "n", "н", "O", "О", "o", "о",
	"P", "Р", "p", "р", "Q", "Я", "q", "я", "R", "Г", "r", "г", "S", "С", "s", "с",
	"T", "Т", "t", "т", "U", "И", "u", "и", "X", "Х", "x", "х", "Y", "Ы", "y", "ы",
	"Z", "З", "z", "з",
	// «і» (украинская i) — whisper выдаёт её вместо «и» на битой записи.
	"і", "и", "І", "И",
)

// New собирает правило по имени и языку. Пустое имя —Matcher всегда «нет»
// (слушать имя без имени нечего).
func New(name, lang string) *Matcher {
	m := &Matcher{lang: strings.ToLower(strings.TrimSpace(lang))}
	m.full = normalize(m.lang, name)
	if m.full == "" {
		return m
	}
	if m.lang == "en" {
		m.re = regexp.MustCompile(`(?:^|[^a-z])` + regexp.QuoteMeta(m.full) + `(?:[^a-z]|$)`)
		return m
	}

	// Русский: whisper съедает ПОСЛЕДНИЙ ГЛАСНЫЙ — допускаем короткую форму
	// (имя без него). «Мира» → «мир», «Яся» → «яс».
	short, tail := m.full, ""
	if r := []rune(m.full); len(r) > 2 && strings.ContainsRune(ruVowels, r[len(r)-1]) {
		short, tail = string(r[:len(r)-1]), string(r[len(r)-1])
	}
	boundary := `(?:[^а-яё]|$)`
	m.re = regexp.MustCompile(`(?:^|,\s*)` + regexp.QuoteMeta(short) +
		`(?:` + regexp.QuoteMeta(tail) + `)?` + boundary)
	// Полное имя после слова — обычно падеж слова-двойника («картина мира»),
	// но «привет, Мира!» — обращение, и оно через запятую.
	m.genitive = regexp.MustCompile(`[\sа-яё]{2,}` + regexp.QuoteMeta(m.full) + boundary)
	m.commaFull = regexp.MustCompile(`,\s*` + regexp.QuoteMeta(m.full) + boundary)
	// «мир» после предлога («в мир», «о мир») — не обращение. Фильтр осмыслен
	// только когда короткая форма — само слово: проверяем это по словарику
	// слов-двойников, а не по всем именам сразу.
	if _, ok := homonymWorlds[short]; ok {
		m.shortWord = regexp.MustCompile(`(?:^|[^а-яё])(?:в|о|под|на|об|ко|за|меж)\s+` +
			regexp.QuoteMeta(short) + boundary)
	}
	m.latin = ruLatin
	m.latinRun = ruLatinRunes
	return m
}

// homonymWorlds — короткие формы имён, которые сами по себе русские слова:
// фильтр предлогов лечит ложные «в мир» для «Мира».
var homonymWorlds = map[string]bool{"мир": true}

// normalize — имя к каноническому виду: без пробелов, нижний регистр,
// русский — с «ё»→«е».
func normalize(lang, name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if lang != "en" {
		s = strings.ReplaceAll(s, "ё", "е")
	}
	return s
}

// Mention — прозвучало ли имя в тексте.
func (m *Matcher) Mention(text string) bool {
	if m == nil || m.full == "" || m.re == nil {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(text))
	if m.lang != "en" {
		t = strings.ReplaceAll(t, "ё", "е")
		if m.latinRun.MatchString(t) {
			// Латиница в русском тексте — битая кириллица, переводим весь.
			t = m.latin.Replace(t)
		}
	}
	if !m.re.MatchString(t) {
		return false
	}
	if m.genitive != nil && m.genitive.MatchString(t) && !m.commaFull.MatchString(t) {
		return false
	}
	if m.shortWord != nil && m.shortWord.MatchString(t) {
		return false
	}
	return true
}
