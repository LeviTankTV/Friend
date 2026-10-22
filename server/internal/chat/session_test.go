package chat

import (
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		i++
		return t.Add(time.Duration(i) * time.Second)
	}
}

// История держит ровно max реплик: старые вытесняются, новые остаются.
func TestSessionKeepsLastMessages(t *testing.T) {
	s := New(4)
	for _, text := range []string{"1", "2", "3", "4", "5", "6"} {
		s.Add(RoleUser, text)
	}
	got := s.Messages()
	if len(got) != 4 {
		t.Fatalf("в истории %d реплик, ожидалось 4", len(got))
	}
	for i, want := range []string{"3", "4", "5", "6"} {
		if got[i].Text != want {
			t.Errorf("реплика %d = %q, ожидалась %q", i, got[i].Text, want)
		}
	}
}

// Порядок — от старой к новой: модель должна видеть разговор в ту же сторону,
// в какую его видит человек.
func TestOrderAndRoles(t *testing.T) {
	s := New(10)
	s.Add(RoleUser, "привет")
	s.Add(RoleAssistant, "привет-привет")

	got := s.Messages()
	if len(got) != 2 {
		t.Fatalf("в истории %d реплик, ожидалось 2", len(got))
	}
	if got[0].Role != RoleUser || got[0].Text != "привет" {
		t.Errorf("первая реплика %+v", got[0])
	}
	if got[1].Role != RoleAssistant || got[1].Text != "привет-привет" {
		t.Errorf("вторая реплика %+v", got[1])
	}
}

// Messages отдаёт КОПИЮ: иначе следующая Add сдвинула бы элементы под руками
// того, кто читает историю в другом потоке.
func TestMessagesIsCopy(t *testing.T) {
	s := New(3)
	s.Add(RoleUser, "первый")
	got := s.Messages()
	s.Add(RoleUser, "второй")
	s.Add(RoleUser, "третий")
	s.Add(RoleUser, "четвёртый") // первый вытеснен

	if len(got) != 1 || got[0].Text != "первый" {
		t.Errorf("копия изменилась: %+v", got)
	}
}

// Пустой лимит не должен означать «ничего не помним»: ноль — это признак
// незаполненного конфига, а не желание стереть память.
func TestZeroMaxFallsBackToDefault(t *testing.T) {
	s := New(0)
	if s.Max() != DefaultMax {
		t.Errorf("Max() = %d, ожидалось %d", s.Max(), DefaultMax)
	}
	s.Add(RoleUser, "реплика")
	if s.Len() != 1 {
		t.Errorf("Len() = %d, ожидалась 1", s.Len())
	}
}

func TestClockIsUsed(t *testing.T) {
	base := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s := NewWithClock(5, fixedClock(base))
	m := s.Add(RoleUser, "время")
	if !m.At.After(base) {
		t.Errorf("время реплики %v — часы не применились", m.At)
	}
}

func TestClear(t *testing.T) {
	s := New(5)
	s.Add(RoleUser, "забудь это")
	s.Clear()
	if s.Len() != 0 {
		t.Errorf("после Clear в истории %d реплик", s.Len())
	}
}
