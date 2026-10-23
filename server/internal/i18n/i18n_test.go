package i18n

import (
	"reflect"
	"sort"
	"strings"
	"testing"
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
