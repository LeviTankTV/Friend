package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open("") // :memory:
	if err != nil {
		t.Fatalf("открыть память: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var at = time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)

func TestRememberRecallPrefix(t *testing.T) {
	s := open(t)
	// «отца» в запросе — факт написан «отцом»: полное совпадение слова бы
	// промахнулось, префиксное («отц*») обязано найти.
	if _, err := s.Remember("Тим вчера вечером жаловался на отца, тот болел", "user", at); err != nil {
		t.Fatalf("запомнить: %v", err)
	}
	if _, err := s.Remember("Кота зовут Барсик", "user", at.Add(time.Hour)); err != nil {
		t.Fatalf("запомнить: %v", err)
	}
	facts, err := s.Recall("как там отец?", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(facts) != 1 || !strings.Contains(facts[0].Text, "жаловался") {
		t.Fatalf("по запросу про отца должен найтись факт про отца: %+v", facts)
	}
	facts, err = s.Recall("барсик", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(facts) != 1 || !strings.Contains(facts[0].Text, "Барсик") {
		t.Fatalf("по запросу про кота — факт про кота: %+v", facts)
	}
	facts, err = s.Recall("сегодня совсем не спал", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("несвязанный запрос не должен тащить всё подряд: %+v", facts)
	}
}

func TestRecallEmptyQueryReturnsRecent(t *testing.T) {
	s := open(t)
	if _, err := s.Remember("первый факт", "user", at); err != nil {
		t.Fatalf("запомнить: %v", err)
	}
	if _, err := s.Remember("второй факт", "user", at.Add(time.Hour)); err != nil {
		t.Fatalf("запомнить: %v", err)
	}
	facts, err := s.Recall("как она?", 5) // из одних стоп-слов
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("запрос из одних предлогов — свежие факты: %+v", facts)
	}
}

func TestRememberCapAndEmpty(t *testing.T) {
	s := open(t)
	if id, err := s.Remember("   ", "user", at); id != 0 || err != nil {
		t.Fatalf("пустой факт не факт: id=%d err=%v", id, err)
	}
	long := strings.Repeat("а", MaxFactRunes+50)
	if _, err := s.Remember(long, "user", at); err != nil {
		t.Fatalf("запомнить длинный: %v", err)
	}
	facts, err := s.Recall("а", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(facts) != 1 || len([]rune(facts[0].Text)) != MaxFactRunes {
		t.Fatalf("длинный факт обязан обрезаться до лимита: %d знаков", len([]rune(facts[0].Text)))
	}
}

func TestRecallQuotedQueryIsNotInjection(t *testing.T) {
	s := open(t)
	if _, err := s.Remember("Тим кодит Шейпер", "user", at); err != nil {
		t.Fatalf("запомнить: %v", err)
	}
	// Кавычки, скобки и звёзды — синтаксис FTS5; tokenize их вычищает,
	// запрос обязан отработать, а не сломаться и не стать чем-то иным.
	for _, q := range []string{
		`Шейпер" OR id IN (SELECT id FROM facts) --`,
		`"*"`, `(текст NEAR код)`, `NOT код`,
	} {
		facts, err := s.Recall(q, 5)
		if err != nil {
			t.Fatalf("запрос %q: %v", q, err)
		}
		_ = facts
	}
}

func TestArchiveRecentConvoOrder(t *testing.T) {
	s := open(t)
	msgs := []struct{ role, text string }{
		{"user", "первая"},
		{"assistant", "вторая"},
		{"user", "третья"},
		{"assistant", "четвёртая"},
	}
	for i, m := range msgs {
		if err := s.Archive(m.role, m.text, at.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("archive: %v", err)
		}
	}
	list, err := s.RecentConvo(3)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(list) != 3 || list[0].Text != "вторая" || list[2].Text != "четвёртая" {
		t.Fatalf("последние 3 в хронологическом порядке: %+v", list)
	}
	if s.ConvoCount() != 4 {
		t.Fatalf("архив из 4 реплик: %d", s.ConvoCount())
	}
}

func TestSummariesOnePerDay(t *testing.T) {
	s := open(t)
	day := time.Date(2026, 10, 22, 23, 30, 0, 0, time.UTC)
	if err := s.SaveSummary("первое саммари", day); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !s.HasSummary(day) {
		t.Fatal("саммари за день обязано найтись")
	}
	// Повторная ночная свка того же дня — обновление, не второе саммари.
	if err := s.SaveSummary("уточнённое саммари", day.Add(20*time.Minute)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if s.SummaryCount() != 1 {
		t.Fatalf("одно саммари на день: %d", s.SummaryCount())
	}
	list, err := s.Summaries(5)
	if err != nil {
		t.Fatalf("summaries: %v", err)
	}
	if len(list) != 1 || list[0].Text != "уточнённое саммари" {
		t.Fatalf("саммари обновилось: %+v", list)
	}
	if _, ok := s.LastSummaryTime(); !ok {
		t.Fatal("время последнего саммари должно запомниться")
	}
	if s.HasSummary(day.AddDate(0, 0, 1)) {
		t.Fatal("за другой день саммари быть не должно")
	}
}

func TestConvoSince(t *testing.T) {
	s := open(t)
	base := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	_ = s.Archive("user", "утренняя", base)
	_ = s.Archive("user", "вечерняя", base.Add(8*time.Hour))
	list, err := s.ConvoSince(base.Add(time.Hour))
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(list) != 1 || list[0].Text != "вечерняя" {
		t.Fatalf("после полудня — только вечерняя: %+v", list)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("открыть: %v", err)
	}
	if _, err := s.Remember("кот Барсик", "user", at); err != nil {
		t.Fatalf("запомнить: %v", err)
	}
	if err := s.Archive("user", "поговорим потом", at); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// «Перезапуск сервера»: то же место, тот же файл.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("переоткрыть: %v", err)
	}
	defer s2.Close()
	facts, err := s2.Recall("барсик", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(facts) != 1 || !strings.Contains(facts[0].Text, "Барсик") {
		t.Fatalf("после перезапуска факт обязан быть на месте: %+v", facts)
	}
	if s2.ConvoCount() != 1 {
		t.Fatalf("архив обязан пережить перезапуск: %d", s2.ConvoCount())
	}
}

func TestTokenizeFilters(t *testing.T) {
	toks := tokenize("Тим ЧИТАЕТ «ТАСС» и спит 123 'кот' [пёс]", 0)
	// Ожидание по слову: нижний регистр, предлог «и» отфильтрован,
	// кавычки и скобки вычищены, цифры — токен.
	want := strings.Join([]string{"тим", "читает", "тасс", "спит", "123", "кот", "пёс"}, " ")
	if got := strings.Join(toks, " "); got != want {
		t.Fatalf("токены:\nполучили %q\nожидали  %q", got, want)
	}
	// Стоп-слово не токен даже в компании с нормальным словом.
	if got := tokenize("это кот", 0); len(got) != 1 || got[0] != "кот" {
		t.Fatalf("стоп-слово обязано отфильтроваться: %q", strings.Join(got, " "))
	}
	// Дубли убираются.
	if got := tokenize("кот кот кот", 0); len(got) != 1 {
		t.Fatalf("дубли: %q", strings.Join(got, " "))
	}
	// Лимит слов в запросе.
	if got := tokenize("кот пёс тигр слон лось жираф носорог", 3); len(got) != 3 {
		t.Fatalf("лимит токенов: %q", strings.Join(got, " "))
	}
}

// Этап 10: факты и запросы по-английски находятся тем же префиксным поиском,
// а запрос из английских стоп-слов — «не о чём», свежие факты (как русский
// запрос из предлогов).
func TestRecallEnglish(t *testing.T) {
	s := open(t)
	if _, err := s.Remember("my sister's birthday is on March 3", "user", at); err != nil {
		t.Fatalf("запомнить: %v", err)
	}
	if _, err := s.Remember("the office is cold in the morning", "user", at.Add(time.Hour)); err != nil {
		t.Fatalf("запомнить: %v", err)
	}
	facts, err := s.Recall("when is my sister birthday?", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(facts) != 1 || !strings.Contains(facts[0].Text, "birthday") {
		t.Fatalf("английский запрос должен находить факт про сестру: %+v", facts)
	}
	facts, err = s.Recall("the and was are", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("запрос из стоп-слов — свежие факты, а не пустота: %+v", facts)
	}
}

func TestListFactsPagination(t *testing.T) {
	s := open(t)
	for i, txt := range []string{"первый", "второй", "третий", "четвёртый"} {
		if _, err := s.Remember(txt, "user", at.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := s.ListFacts(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(page) != 2 {
		t.Fatalf("total=%d len=%d, ожидал 4/2", total, len(page))
	}
	// свежие сверху: id DESC — «четвёртый», «третий», «второй», «первый»;
	// offset=1 limit=2 → «третий», «второй»
	if page[0].Text != "третий" || page[1].Text != "второй" {
		t.Fatalf("страница не по свежести: %+v", page)
	}
	if !page[0].Created.Equal(at.Add(2 * time.Hour)) {
		t.Fatalf("created не распарсился: %+v", page[0])
	}
}

func TestForgetKeepsFTSClean(t *testing.T) {
	s := open(t)
	id, err := s.Remember("Кота зовут Барсик", "user", at)
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := s.Remember("Любит джаз", "user", at.Add(time.Hour))
	gone, err := s.Forget(id)
	if err != nil {
		t.Fatal(err)
	}
	if !gone {
		t.Fatal("Forget вернул false у существующего факта")
	}
	found, err := s.Recall("Барсик", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("после удаления FTS находит удалённый: %+v", found)
	}
	if got := s.FactCount(); got != 1 {
		t.Fatalf("FactCount=%d, ожидал 1", got)
	}
	// живой факт находится по-прежнему
	found2, _ := s.Recall("джаз", 5)
	if len(found2) != 1 || found2[0].ID != id2 {
		t.Fatalf("выживший факт потерялся: %+v", found2)
	}
	// повторное удаление — false, не ошибка
	gone2, err := s.Forget(id)
	if err != nil {
		t.Fatal(err)
	}
	if gone2 {
		t.Fatal("повторный Forget вернул true у удалённого факта")
	}
}
