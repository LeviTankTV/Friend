package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"friend/internal/mood"
)

// TestOpenMissingFileStartsEmpty — первый запуск: файла нет, состояние пустое и
// это не ошибка.
func TestOpenMissingFileStartsEmpty(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := st.Snapshot().Position; got.X != 0 || got.Y != 0 {
		t.Errorf("позиция первого запуска = %+v, want 0,0", got)
	}
}

// TestSetPositionSurvivesRestart — главное требование этапа 1: позиция окна
// запоминается между запусками.
func TestSetPositionSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := st.SetPosition(Position{X: 1234, Y: -56})
	if err != nil {
		t.Fatalf("SetPosition: %v", err)
	}
	if saved.Position.X != 1234 || saved.Position.Y != -56 {
		t.Errorf("вернулась позиция %+v, want 1234,-56", saved.Position)
	}
	if saved.UpdatedAt.IsZero() {
		t.Error("UpdatedAt не заполнен — по нему видно, когда компаньон двигали")
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("повторный Open: %v", err)
	}
	if got := again.Snapshot().Position; got.X != 1234 || got.Y != -56 {
		t.Errorf("после перезапуска позиция %+v, want 1234,-56", got)
	}
	// Временный файл атомарной записи не должен оставаться на диске.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("остался временный файл %s.tmp (err=%v)", path, err)
	}
}

// TestSetMoodSurvivesRestart — главное требование этапа 5: внутреннее
// состояние переживает перезапуск. Старый файл без поля mood — не ошибка:
// состояние просто начинается с нуля.
func TestSetMoodSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)
	want := mood.State{Mood: 0.42, Energy: 0.31, Affection: 0.77, LastUser: now, At: now}
	if _, err := st.SetMood(want); err != nil {
		t.Fatalf("SetMood: %v", err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatalf("повторный Open: %v", err)
	}
	got := again.Snapshot().Mood
	if got.Mood != want.Mood || got.Energy != want.Energy || got.Affection != want.Affection {
		t.Errorf("после перезапуска состояние %+v, want %+v", got, want)
	}
	if !got.LastUser.Equal(now) {
		t.Errorf("LastUser не пережил перезапуск: %v", got.LastUser)
	}
}

// TestMoodFieldIsOptionalForOldFile — файл старого формата (без mood) читается
// как милый: Mood остаётся нулевым, вызывающий подставит Default.
func TestMoodFieldIsOptionalForOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{"position":{"x":10,"y":20},"updated_at":"2026-01-01T00:00:00Z","version":1}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatalf("старый файл не читается: %v", err)
	}
	if got := st.Snapshot().Mood; got.Mood != 0 || got.Energy != 0 || !got.At.IsZero() {
		t.Errorf("старый файл должен дать пустое состояние, получил %+v", got)
	}
}

// TestOpenBrokenFileReportsButWorks — битый файл состояния не мешает запуску:
// компаньон должен появиться на экране, а не упасть.
func TestOpenBrokenFileReportsButWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{обрыв"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err == nil {
		t.Error("ожидалось предупреждение о битом состоянии")
	}
	if st == nil {
		t.Fatal("Open вернул nil вместо хранилища")
	}
	if _, err := st.SetPosition(Position{X: 5, Y: 5}); err != nil {
		t.Errorf("после битого файла запись не работает: %v", err)
	}
}

// TestConcurrentSetPosition — окно тащат мышью, а сервер в это время может
// читать состояние: гонка ловится запуском с -race.
func TestConcurrentSetPosition(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := st.SetPosition(Position{X: n*100 + j, Y: j}); err != nil {
					t.Errorf("SetPosition: %v", err)
					return
				}
				_ = st.Snapshot()
			}
		}(i)
	}
	wg.Wait()
	// Файл обязан быть валидным JSON после всех параллельных записей.
	reopened, err := Open(st.Path())
	if err != nil {
		t.Fatalf("после параллельных записей состояние не читается: %v", err)
	}
	if reopened.Snapshot().Version == 0 {
		t.Error("версия состояния не проставлена")
	}
}
