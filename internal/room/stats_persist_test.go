package room

import (
	"path/filepath"
	"testing"
)

// Счётчики должны переживать перезапуск процесса: сохранили — новый
// Manager читает то же число.
func TestCountersSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")

	m1 := NewManager()
	m1.gamesStarted.Add(7)
	m1.gamesFinished.Add(3)
	m1.SaveCounters(path)

	m2 := NewManager()
	m2.LoadCounters(path)
	if got := m2.gamesStarted.Load(); got != 7 {
		t.Errorf("gamesStarted после загрузки = %d, ждали 7", got)
	}
	if got := m2.gamesFinished.Load(); got != 3 {
		t.Errorf("gamesFinished после загрузки = %d, ждали 3", got)
	}

	// Прибавились — пересохранили — снова загрузили: растёт, а не откатывается.
	m2.gamesFinished.Add(1)
	m2.SaveCounters(path)
	m3 := NewManager()
	m3.LoadCounters(path)
	if got := m3.gamesFinished.Load(); got != 4 {
		t.Errorf("gamesFinished = %d, ждали 4", got)
	}

	// Битый/отсутствующий файл — старт с нуля, без паники.
	m4 := NewManager()
	m4.LoadCounters(filepath.Join(t.TempDir(), "нет-такого.json"))
	if got := m4.gamesStarted.Load(); got != 0 {
		t.Errorf("при отсутствии файла счётчик = %d, ждали 0", got)
	}
}
