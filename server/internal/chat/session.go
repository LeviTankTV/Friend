// Package chat — история разговора в памяти.
//
// Задача одна: держать последние N реплик, чтобы модель видела контекст, и не
// расти бесконечно. Долгая память (факты, вчерашние разговоры) — это этап 3 и
// SQLite; здесь только «что было сказано только что».
//
// Роли — строки OpenAI-совместимого API (user/assistant), а не свои имена:
// история уезжает в запрос к модели как есть, без перевода из одного словаря в
// другой.
package chat

import (
	"sync"
	"time"
)

// Роли реплик.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// DefaultMax — сколько реплик помним, если в конфиге не сказано иное.
// Двадцать реплик — это около десяти обменов: хватает, чтобы «о чём мы
// говорили», и мало, чтобы каждый запрос стоил копейки.
const DefaultMax = 20

// Message — одна реплика разговора.
type Message struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Session — история разговора. Безопасна для одновременного использования.
type Session struct {
	mu   sync.Mutex
	max  int
	now  func() time.Time
	msgs []Message
}

// New создаёт сессию на max реплик (max <= 0 — DefaultMax).
func New(max int) *Session { return NewWithClock(max, time.Now) }

// NewWithClock — то же, но с подменяемыми часами: время реплики попадает в
// историю, и в тестах оно должно быть предсказуемым.
func NewWithClock(max int, now func() time.Time) *Session {
	if max <= 0 {
		max = DefaultMax
	}
	if now == nil {
		now = time.Now
	}
	return &Session{max: max, now: now}
}

// Max — сколько реплик помещается.
func (s *Session) Max() int { return s.max }

// Add добавляет реплику и возвращает её. Самые старые реплики вытесняются:
// помнить всё в памяти незачем, а обрезать нужно пачкой, чтобы срез не рос.
func (s *Session) Add(role, text string) Message {
	m := Message{Role: role, Text: text, At: s.now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, m)
	if drop := len(s.msgs) - s.max; drop > 0 {
		// Копирование внутри одного массива: append с перекрывающимися
		// срезами безопасен, а memory не перевыделяется.
		s.msgs = append(s.msgs[:0], s.msgs[drop:]...)
	}
	return m
}

// Messages — копия истории от старой реплики к новой.
//
// Именно копия: вызывающая сторона уходит с этими данными в другой поток, и
// разделять с ней один срез нельзя — следующая Add сдвинула бы элементы под
// руками того, кто историю читает.
func (s *Session) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.msgs))
	copy(out, s.msgs)
	return out
}

// Len — сколько реплик сейчас в истории.
func (s *Session) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

// Clear очищает историю (нужно для «забыть разговор» и для тестов).
func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = nil
}
