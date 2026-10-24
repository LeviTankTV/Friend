// Package memory — долгая память компаньона (этап 3).
//
// История разговора (internal/chat) умирает вместе с сервером. Память — нет:
// она в SQLite-файле в каталоге пользователя, и после перезапуска сервера Мира
// помнит вчерашний разговор и то, что человек ей доверил.
//
// Три вида данных, у каждого своё назначение:
//   - convo     — архив всех реплик (и его, и её). Из него восстанавливается
//     живая история при старте и собирается саммари за день;
//   - facts     — то, что человек явно велел запомнить («запомни: …»).
//     Факты ищутся по тексту текущей реплики и подставляются в системный
//     промпт — только подходящие, не весь архив;
//   - summaries — короткие саммари разговоров (модель сжимает день ночью).
//     Нужны для «что происходило в последнее время», когда фактов мало.
//
// Приватность: файл лежит локально, наружу не уходит никогда. В запрос к
// модели попадают только подходящие по смыслу факты и последние саммари.
//
// Движок — modernc.org/sqlite (чистый Go, без cgo): на этой машине C-компилятора
// нет, и сборка не должна от него зависеть.
package memory

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // драйвер SQLite под именем "sqlite"
)

// Fact — запомненный факт.
type Fact struct {
	ID      int64
	Text    string
	Created time.Time
}

// Summary — саммари разговора за день.
type Summary struct {
	ID      int64
	Text    string
	Day     string // «2006-01-02» локального дня: саммари одноразово на день
	Created time.Time
}

// ConvoMsg — реплика из архива разговора.
type ConvoMsg struct {
	Role string
	Text string
	At   time.Time
}

// Store — память в SQLite. Все методы безопасны для одновременного вызова
// (database/sql сериализует, соединение одно).
type Store struct {
	db *sql.DB
}

// Open открывает (при необходимости создаёт) файл памяти. Пустой путь —
// внутренняя база ":memory:": удобно тестам, приложению так жить нельзя.
func Open(path string) (*Store, error) {
	dsn := ":memory:"
	if path != "" {
		// file:///C:/... — канонический вид для Windows-пути; пробелы и
		// не-ASCII driver увидит как %XX.
		dsn = "file:///" + url.PathEscape(filepath.ToSlash(path)) +
			"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Одно соединение: записи идут из хаба, планировщика и старта, SQLITE_BUSY
	// при одном writer не возникает вовсе.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// migrate поднимает таблицы и FTS-индекс. IF NOT EXISTS — чтобы повторный
// запуск на живом файле ничего не трогал.
func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS convo (
  id   INTEGER PRIMARY KEY AUTOINCREMENT,
  role TEXT NOT NULL,
  text TEXT NOT NULL,
  at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS facts (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  text    TEXT NOT NULL,
  source  TEXT NOT NULL DEFAULT 'user',
  created TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS summaries (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  text    TEXT NOT NULL,
  day     TEXT NOT NULL UNIQUE,
  created TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(
  text, content='facts', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS facts_ai AFTER INSERT ON facts BEGIN
  INSERT INTO facts_fts(rowid, text) VALUES (new.id, new.text);
END;
CREATE TRIGGER IF NOT EXISTS facts_ad AFTER DELETE ON facts BEGIN
  INSERT INTO facts_fts(facts_fts, rowid, text) VALUES('delete', old.id, old.text);
END;
CREATE INDEX IF NOT EXISTS convo_at ON convo(at);
`
	_, err := s.db.Exec(schema)
	return err
}

// Close закрывает базу.
func (s *Store) Close() error { return s.db.Close() }

// MaxFactRunes — предел длины факта. «запомни:» на страницу текста — это
// уже документ, а не факт; он раздувает промпт и выдаёт мусорные совпадения.
const MaxFactRunes = 300

// Remember сохраняет факт. source — «user» (человек велел). Пустой факт —
// не факт.
func (s *Store) Remember(text, source string, at time.Time) (int64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	if n := len([]rune(text)); n > MaxFactRunes {
		text = string([]rune(text)[:MaxFactRunes])
	}
	if source == "" {
		source = "user"
	}
	res, err := s.db.Exec(`INSERT INTO facts(text, source, created) VALUES(?, ?, ?)`,
		text, source, at.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

// stopWords — частотные слова: в MATCH они не несут смысла и дали бы
// совпадения со всем на свете. Только слова длиннее двух букв (короткие отсекает
// tokenize).
var stopWords = map[string]bool{
	// русские
	"он": true, "она": true, "оно": true, "они": true, "мы": true, "ты": true,
	"вы": true, "не": true, "ни": true, "да": true, "нет": true, "что": true,
	"чем": true, "как": true, "кто": true, "мой": true, "моя": true,
	"мои": true, "моё": true, "его": true, "её": true, "это": true,
	"тут": true, "там": true, "про": true, "для": true, "над": true,
	"об": true, "от": true, "из": true, "за": true, "на": true, "но": true,
	"со": true, "же": true, "бы": true, "ли": true, "мне": true, "тебе": true,
	"себя": true, "всё": true, "так": true, "был": true, "есть": true,
	"было": true, "будет": true, "стал": true, "стала": true, "очень": true,
	"совсем": true, "меня": true, "тебя": true, "него": true, "неё": true,
	"себе": true, "тобой": true, "много": true, "мало": true, "потом": true,
	"сейчас": true, "тогда": true, "завтра": true, "сегодня": true,
	"вчера": true, "давно": true, "всегда": true, "никогда": true,
	"почему": true, "когда": true, "где": true, "куда": true, "какой": true,
	"какая": true, "какое": true, "какие": true, "который": true,
	"такой": true, "такая": true, "такое": true, "такие": true,
	"каждый": true, "каждая": true, "каждое": true, "самый": true,
	"сама": true, "сами": true, "этом": true, "этого": true, "этой": true,
	"эти": true, "этих": true, "этим": true, "тем": true, "тот": true,
	"та": true, "то": true, "те": true, "были": true, "буду": true,
	"будем": true, "нужен": true, "нужна": true, "нужно": true,
	"можно": true, "нельзя": true, "тоже": true, "уже": true, "ещё": true,
	"просто": true,
	// английские базовые
	"about": true, "with": true, "that": true, "this": true, "what": true,
	"have": true, "been": true, "from": true, "there": true, "their": true,
	"them": true, "then": true, "they": true, "will": true, "would": true,
	"could": true, "should": true, "your": true,
	// английские частотные (этап 10): запрос из них — «ответить нечем»,
	// а не подбор всех фактов подряд
	"the": true, "and": true, "was": true, "are": true, "his": true,
	"her": true, "its": true, "our": true, "you": true, "she": true,
	"him": true, "who": true, "why": true, "how": true, "all": true,
	"any": true, "for": true, "not": true, "but": true, "can": true,
	"did": true, "does": true, "had": true, "has": true, "also": true,
	"just": true, "when": true, "where": true, "which": true, "while": true,
	"because": true, "really": true, "very": true, "one": true, "two": true,
	"get": true, "got": true, "out": true, "too": true, "now": true,
}

// tokenize разбивает текст на слова, пригодные для FTS5-запроса: только
// буквы и цифры (символы FTS-синтаксиса — кавычки, звёзды, скобки —
// вычищаются, запрос не может стать инъекцией), длина ≥ 3 (короткие слова —
// предлоги и обращения), без стоп-слов. Дубли убираются.
func tokenize(text string, max int) []string {
	lowered := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= 'а' && r <= 'я', r >= 'А' && r <= 'Я',
			r == 'ё', r == 'Ё', r >= '0' && r <= '9':
			return r
		default:
			return ' '
		}
	}, text)
	seen := make(map[string]bool)
	var out []string
	for _, w := range strings.Fields(strings.ToLower(lowered)) {
		if len([]rune(w)) < 3 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// Recall ищет факты, относящиеся к тексту реплики. Совпадение — по началу
// слова («отц*» найдёт «отца», «отцом»): русский сгибается, полное совпадение
// целого слова ловит мало. Ранжирование — bm25 (частотность FTS5).
//
// Пустой запрос (или запрос из одних предлогов) — самые свежие факты: на
// «что ты помнишь?» отвечать нечем, если ничего не подставить.
func (s *Store) Recall(query string, limit int) ([]Fact, error) {
	if limit <= 0 {
		limit = 5
	}
	tokens := tokenize(query, 12)
	if len(tokens) == 0 {
		return s.recentFacts(limit)
	}
	// Каждому токену — префиксное совпадение со словом в памяти. Запрос —
	// начало слова без двух последних знаков («жаловался» → «жаловал*»
	// найдёт «жаловался», «жаловался бы»; «отец*» НЕ найдёт «отца» —
	// чередующаяся гласная, общий префикс там всего «от»). Префикс короче
	// двух знаков не делаем: на коротких словах это уже любые совпадения.
	// Скобки вокруг группы ставить нельзя: с группой запрос ломается
	// (замерено на живом движке).
	var parts []string
	for _, w := range tokens {
		r := []rune(w)
		n := len(r) - 2
		if n < 2 {
			n = 2
		}
		parts = append(parts, string(r[:n])+"*")
	}
	match := strings.Join(parts, " OR ")
	// rowid из FTS-индекса, сами факты — из таблицы: с JOIN этого же
	// запроса движок на префиксных OR иногда теряет строку (замерено),
	// IN-подзапрос работает стабильно.
	// rank (это же bm25) доступен только внутри FTS-таблицы, поэтому
	// ранжирование и лимит — в подзапросе; снаружи остаётся порядок id.
	rows, err := s.db.Query(`
SELECT id, text, created FROM facts
WHERE id IN (
  SELECT rowid FROM facts_fts
  WHERE facts_fts MATCH ?
  ORDER BY rank, rowid DESC
  LIMIT ?
)
ORDER BY id`, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFacts(rows)
}

// recentFacts — свежие факты без ранжирования.
func (s *Store) recentFacts(limit int) ([]Fact, error) {
	rows, err := s.db.Query(
		`SELECT id, text, created FROM facts ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFacts(rows)
}

// scanFacts — общий разбор строк фактов.
func scanFacts(rows *sql.Rows) ([]Fact, error) {
	var out []Fact
	for rows.Next() {
		var f Fact
		var created string
		if err := rows.Scan(&f.ID, &f.Text, &created); err != nil {
			return nil, err
		}
		f.Created, _ = time.Parse(time.RFC3339, created)
		out = append(out, f)
	}
	return out, rows.Err()
}

// FactCount — сколько фактов в памяти (для /healthz и логов).
func (s *Store) FactCount() int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM facts`).Scan(&n)
	return n
}

// getMeta/setMeta — ключ-значение в таблице meta (upsert).
func (s *Store) getMeta(key string) string {
	var v string
	s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	return v
}

func (s *Store) setMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// FollowupMinAge — младше этого возраста факт не «возврат», а новость.
const FollowupMinAge = 21 * 24 * time.Hour

// FollowupFact — старый факт для возврата к нему (этап 11б): создан раньше
// cutoff, не тот, что показывали в прошлый раз (meta followup_id), из тех,
// что старше — случайный, чтобы не ходить по списку по кругу. Пусто —
// старых фактов нет.
func (s *Store) FollowupFact(now time.Time) (Fact, bool, error) {
	var lastID int64
	if _, err := fmt.Sscanf(s.getMeta("followup_id"), "%d", &lastID); err != nil {
		lastID = 0
	}
	rows, err := s.db.Query(`
SELECT id, text, created FROM facts
WHERE created < ? AND id != ?
ORDER BY RANDOM() LIMIT 3`,
		now.Add(-FollowupMinAge).UTC().Format(time.RFC3339), lastID)
	if err != nil {
		return Fact{}, false, err
	}
	defer rows.Close()
	facts, err := scanFacts(rows)
	if err != nil {
		return Fact{}, false, err
	}
	if len(facts) == 0 {
		return Fact{}, false, nil
	}
	pick := facts[0]
	if err := s.setMeta("followup_id", strconv.FormatInt(pick.ID, 10)); err != nil {
		return Fact{}, false, err
	}
	return pick, true, nil
}

// ConvoCount — сколько реплик в архиве.
func (s *Store) ConvoCount() int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM convo`).Scan(&n)
	return n
}

// SummaryCount — сколько саммари.
func (s *Store) SummaryCount() int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM summaries`).Scan(&n)
	return n
}

// Archive пишет реплику в архив разговора. Роли — как в internal/chat
// (user/assistant), чтобы архив и живая история были одним и тем же словарём.
func (s *Store) Archive(role, text string, at time.Time) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO convo(role, text, at) VALUES(?, ?, ?)`,
		role, text, at.UTC().Format(time.RFC3339))
	return err
}

// RecentConvo — последние n реплик архива в хронологическом порядке.
// Нужен при старте: живая история разговора восстанавливается из файла.
func (s *Store) RecentConvo(n int) ([]ConvoMsg, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT role, text, at FROM convo ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConvoMsg
	for rows.Next() {
		var m ConvoMsg
		var at string
		if err := rows.Scan(&m.Role, &m.Text, &at); err != nil {
			return nil, err
		}
		m.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Запрос шёл от новых к старым — разворачиваем в хронологическом порядке.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ConvoSince — реплики позже момента (для саммари за день).
func (s *Store) ConvoSince(t time.Time) ([]ConvoMsg, error) {
	rows, err := s.db.Query(
		`SELECT role, text, at FROM convo WHERE at > ? ORDER BY id`,
		t.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConvoMsg
	for rows.Next() {
		var m ConvoMsg
		var at string
		if err := rows.Scan(&m.Role, &m.Text, &at); err != nil {
			return nil, err
		}
		m.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveSummary сохраняет саммари дня. День один — саммари одно: повторная
// ночная свка обновляет текст дня (ON CONFLICT), а не плодит второе саммари.
func (s *Store) SaveSummary(text string, at time.Time) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	day := at.Format("2006-01-02")
	_, err := s.db.Exec(`
INSERT INTO summaries(text, day, created) VALUES(?, ?, ?)
ON CONFLICT(day) DO UPDATE SET text = excluded.text, created = excluded.created`,
		text, day, at.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO meta(key, value) VALUES('last_summary', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, at.UTC().Format(time.RFC3339))
	return err
}

// Summaries — последние n саммари в хронологическом порядке.
func (s *Store) Summaries(n int) ([]Summary, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT id, text, day, created FROM summaries ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var su Summary
		var created string
		if err := rows.Scan(&su.ID, &su.Text, &su.Day, &created); err != nil {
			return nil, err
		}
		su.Created, _ = time.Parse(time.RFC3339, created)
		out = append(out, su)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// HasSummary — есть ли саммари за указанный день (проверка «не повторяться»).
func (s *Store) HasSummary(day time.Time) bool {
	var x int
	err := s.db.QueryRow(`SELECT 1 FROM summaries WHERE day = ? LIMIT 1`,
		day.Format("2006-01-02")).Scan(&x)
	return err == nil
}

// LastSummaryTime — когда последний раз было саммари (false — никогда).
func (s *Store) LastSummaryTime() (time.Time, bool) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'last_summary'`).Scan(&v)
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
