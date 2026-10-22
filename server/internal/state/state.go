// Package state хранит состояние компаньона между запусками.
//
// Этап 1: позиция окна на экране. На этапе 3 сюда же приедет настроение и
// энергия (этап 5) — поэтому структура разделена на «что показывать» и «когда
// обновлено», а запись атомарная.
//
// Почему JSON-файл, а не SQLite: на этапе 1 хранится одно число позиции, и
// тащить драйвер БД ради него — лишняя зависимость. Память разговоров
// (сообщения, факты) появится на этапе 3 отдельным пакетом на SQLite, а
// позиция окна так и останется мелочью в файле.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"friend/internal/mood"
)

// Position — позиция окна в пикселях экрана.
type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// State — сохраняемое состояние.
type State struct {
	Position  Position  `json:"position"`
	UpdatedAt time.Time `json:"updated_at"`
	// Mood — внутреннее состояние Миры (этап 5): настроение, энергия,
	// привязанность. Zero (нет поля в старом файле) — «свежесозданная»,
	// вызывающий код подставит Default.
	Mood mood.State `json:"mood,omitempty"`
	// Version нужен, чтобы будущие поля можно было добавить без «угадывания»
	// формата старого файла.
	Version int `json:"version"`
}

// Store — потокобезопасный доступ к состоянию с записью на диск.
type Store struct {
	mu   sync.Mutex
	path string
	st   State
	// saveCount — сколько раз реально писали файл (для тестов и логов).
	saveCount int
}

// Open читает состояние из path. Отсутствие файла — не ошибка: компаньон
// ставится в позицию по умолчанию (центр определяется оболочкой).
func Open(path string) (*Store, error) {
	s := &Store{path: path, st: State{Version: 1}}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &s.st); err != nil {
			// Битый файл не должен мешать запуску: жалуемся и стартуем с нуля.
			return s, fmt.Errorf("состояние %s не читается (%w) — начинаю с чистого", path, err)
		}
		if s.st.Version == 0 {
			s.st.Version = 1
		}
	case os.IsNotExist(err):
		// Первый запуск.
	default:
		return s, fmt.Errorf("состояние %s: %w", path, err)
	}
	return s, nil
}

// Path — где лежит файл состояния.
func (s *Store) Path() string { return s.path }

// Snapshot — текущее состояние.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// SetPosition сохраняет позицию окна. Запись атомарная: временный файл в том же
// каталоге + переименование, иначе падение в момент записи оставило бы обрезанный
// JSON и позиция «потерялась» бы вместе с ним.
func (s *Store) SetPosition(p Position) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Position = p
	s.st.UpdatedAt = time.Now().UTC()
	s.st.Version = 1
	if err := s.writeLocked(); err != nil {
		return s.st, err
	}
	s.saveCount++
	return s.st, nil
}

// SetMood сохраняет внутреннее состояние (этап 5). Вызывается циклом
// состояния только при заметном изменении — файл не должен переписываться
// каждые полминуты от шума округления.
func (s *Store) SetMood(m mood.State) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Mood = m
	s.st.UpdatedAt = time.Now().UTC()
	s.st.Version = 1
	if err := s.writeLocked(); err != nil {
		return s.st, err
	}
	s.saveCount++
	return s.st, nil
}

func (s *Store) writeLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("каталог состояния: %w", err)
	}
	raw, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("запись состояния: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		// Windows иногда не даёт переименовать поверх существующего файла —
		// тогда удаляем цель и пробуем снова (данные уже лежат в tmp).
		if err2 := os.Remove(s.path); err2 != nil && !os.IsNotExist(err2) {
			return fmt.Errorf("переименование состояния: %w", err)
		}
		if err2 := os.Rename(tmp, s.path); err2 != nil {
			return fmt.Errorf("переименование состояния: %w", err2)
		}
	}
	return nil
}
