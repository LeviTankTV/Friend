package wake

import "testing"

// Русские случаи переехали из server.TestWakeMention один в один (этап 7),
// английские — новые (этап 10): регресс на живых находках whisper.

func TestMiraRu(t *testing.T) {
	m := New("Мира", "ru")
	cases := []struct {
		text string
		want bool
	}{
		{"Мира, приветы!", true},  // имя в начале
		{"Мира", true},            // только имя
		{"Привет, Мира!", true},   // имя после запятой
		{"мира", true},            // регистр не важен
		{"Мира, ну привет", true}, // запятая после имени
		{"Мир, приветы!", true},   // whisper съел гласную в начале
		{"мир приветы", true},     // то же без запятой
		{"картина мира", false},   // «мира» — родительный от «мир»
		{"нет мира.", false},      // тот же падеж в конце
		{"красота мира", false},   // «мира» в середине фразы
		{"мираж", false},          // за именем буква
		{"в мир", false},          // предлог + «мир» — не обращение
		{"о мир", false},          // то же
		{"про мир", false},        // другой падеж
		{"Mup, приветы!", true},   // латинские M/u/p вместо кириллицы
		{"Мiр, приверьте.", true}, // «і» (U+0456) вместо «и» — как у живой записи
		{"mup, мир", true},        // «m»=м, «u»=и, «p»=р — двойники
		{"картина mupы", false},   // двойники не делают обращение из падежа
		{"", false},               // пусто
	}
	for _, c := range cases {
		if got := m.Mention(c.text); got != c.want {
			t.Errorf("Mention(%q) = %v, ожидалось %v", c.text, got, c.want)
		}
	}
}

func TestMiraEn(t *testing.T) {
	m := New("Mira", "en")
	cases := []struct {
		text string
		want bool
	}{
		{"Mira, hello!", true},
		{"mira", true},
		{"Hey mira, you there?", true},
		{"Mira?", true},
		{"miracle", false},  // продолжение буквами — не имя
		{"mirage", false},   // то же
		{"Miranda", false},  // имя + буквы
		{"miracles happen", false},
		{"I saw a mirage today", false},
		{"", false},
	}
	for _, c := range cases {
		if got := m.Mention(c.text); got != c.want {
			t.Errorf("Mention(%q) = %v, ожидалось %v", c.text, got, c.want)
		}
	}
}

// Имя меняется конфигом — правило следует за ним, включая короткую форму
// без последнего гласного.
func TestCustomName(t *testing.T) {
	m := New("Яся", "ru")
	if !m.Mention("Яся, привет") || !m.Mention("яс, привет") {
		t.Fatal("имя из конфига должно узнаваться, включая форму без гласной")
	}
	if m.Mention("ясное небо") {
		t.Fatal("«ясное» — не имя: за короткой формой буква")
	}
	en := New("Aria", "en")
	if !en.Mention("aria, hi") || en.Mention("ariane") {
		t.Fatal("английское имя из конфига: границы слова")
	}
	// Пустое имя — слушать нечего.
	if New("", "ru").Mention("Мира") {
		t.Fatal("без имени матчер обязан молчать")
	}
}
