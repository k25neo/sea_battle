package room

import (
	"encoding/json"
	"os"
	"time"
)

// persistedStats — то, что переживает рестарт процесса. Счётчики партий
// в памяти обнулялись при каждом деплое, а это главная метрика игры;
// файл sidecar проще любой БД и честнее: пишем редко, читаем раз.
type persistedStats struct {
	GamesStarted  int64 `json:"gamesStarted"`
	GamesFinished int64 `json:"gamesFinished"`
}

// LoadCounters поднимает счётчики из файла. Отсутствующий или битый файл —
// не беда: считаем с нуля (первый запуск, руками почищенная статистика).
func (m *Manager) LoadCounters(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var p persistedStats
	if json.Unmarshal(data, &p) != nil {
		return
	}
	m.gamesStarted.Store(p.GamesStarted)
	m.gamesFinished.Store(p.GamesFinished)
}

// SaveCounters пишет счётчики атомарно: во временный файл и переименованием.
// Полупустой файл после удара об kill -9 таким образом невозможен.
func (m *Manager) SaveCounters(path string) {
	data, err := json.Marshal(persistedStats{
		GamesStarted:  m.gamesStarted.Load(),
		GamesFinished: m.gamesFinished.Load(),
	})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	if os.Rename(tmp, path) != nil {
		os.Remove(tmp)
	}
}

// RunCountersKeeper periodically writes counters to disk until done closes,
// making a final save on exit. Close done (and wait) before exiting the
// process, so that the last counter is visible immediately after restart.
func (m *Manager) RunCountersKeeper(path string, interval time.Duration, done <-chan struct{}) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			m.SaveCounters(path)
		case <-done:
			m.SaveCounters(path)
			return
		}
	}
}
