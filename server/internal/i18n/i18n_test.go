package i18n

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Два инварианта словарей: ни одного пустого поля (пустая строка в промпте —
// это потерянная инструкция) и одинаковый набор форматных глаголов в RU и EN
// (разный набор — падение fmt.Sprintf на живом вызове).

func TestDictsComplete(t *testing.T) {
	for name, d := range map[string]*Dict{"RU": &RU, "EN": &EN} {
		v := reflect.ValueOf(d).Elem()
		for i := 0; i < v.NumField(); i++ {
			if s := v.Field(i).String(); strings.TrimSpace(s) == "" {
				t.Errorf("%s.%s — пустая строка", name, v.Type().Field(i).Name)
			}
		}
	}
}

func TestFormatVerbsMatch(t *testing.T) {
	verbs := func(s string) []string {
		var out []string
		for i := 0; i < len(s); i++ {
			if s[i] == '%' && i+1 < len(s) {
				out = append(out, string(s[i+1]))
			}
		}
		sort.Strings(out)
		return out
	}
	ru := reflect.ValueOf(&RU).Elem()
	en := reflect.ValueOf(&EN).Elem()
	for i := 0; i < ru.NumField(); i++ {
		name := ru.Type().Field(i).Name
		a := verbs(ru.Field(i).String())
		b := verbs(en.Field(i).String())
		if strings.Join(a, ",") != strings.Join(b, ",") {
			t.Errorf("%s: глаголы расходятся (ru=%v en=%v)", name, a, b)
		}
	}
}

func TestFor(t *testing.T) {
	if For("en") != &EN || For("EN") != &EN || For(" en ") != &EN {
		t.Fatal("«en» в любом регистре обязан давать английский словарь")
	}
	if For("ru") != &RU || For("") != &RU || For("de") != &RU {
		t.Fatal("всё, кроме en, — русский: битый код языка не ломает личность")
	}
}

// Счёта словами: русская плюрализация — 1/2/5, 21; английская — 1/many.
func TestCounts(t *testing.T) {
	cases := []struct {
		lang string
		n    int
		want string
	}{
		{"ru", 1, "1 ночь"}, {"ru", 3, "3 ночи"}, {"ru", 5, "5 ночей"},
		{"ru", 21, "21 ночь"}, {"ru", 22, "22 ночи"}, {"ru", 11, "11 ночей"},
		{"en", 1, "1 night"}, {"en", 3, "3 nights"},
	}
	for _, c := range cases {
		if got := CountNights(c.lang, c.n); got != c.want {
			t.Errorf("CountNights(%q,%d) = %q, ожидалось %q", c.lang, c.n, got, c.want)
		}
	}
	if got := CountDays("ru", 2); got != "2 дня" {
		t.Errorf("CountDays: %q", got)
	}
	if got := CountDays("ru", 30); got != "30 дней" {
		t.Errorf("CountDays: %q", got)
	}
	if got := CountHours("ru", 4*time.Hour); got != "4 часа" {
		t.Errorf("CountHours: %q", got)
	}
	if got := DaysAgo("ru", 30); got != "30 дней назад" {
		t.Errorf("DaysAgo: %q", got)
	}
	if got := DaysAgo("en", 30); got != "30 days ago" {
		t.Errorf("DaysAgo en: %q", got)
	}
}
