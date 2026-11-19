// Package store —Persistent-слой игры: профили, рейтинг ЭЛО, история партий.
// SQLite одним файлом на сервере: без демонов, с транзакциями; писать
// одновременно может только один процесс — у нас он и есть один.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// Open открывает (при необходимости создаёт) базу по пути.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // WAL + одна write-конка = нет SQLITE_BUSY вообще
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS players (
	id         TEXT PRIMARY KEY,   -- "vk:123…" или "g:<uuid>"
	name       TEXT NOT NULL,
	vk_id      INTEGER NOT NULL DEFAULT 0,
	elo        INTEGER NOT NULL DEFAULT 1500,
	games      INTEGER NOT NULL DEFAULT 0,
	wins       INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS games (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	winner      TEXT NOT NULL,
	loser       TEXT NOT NULL,
	finished_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_players_elo ON players(elo DESC, games DESC);
`

// Player — витрина профиля.
type Player struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Elo   int    `json:"elo"`
	Games int    `json:"games"`
	Wins  int    `json:"wins"`
}

var guestIDRx = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// EnsureProfile регистрирует/обновляет игрока и возвращает id его профиля.
// Имя обновляется при каждом входе (в ВК его могут сменить). Если гостевой
// профиль с партиями заходит под VK ID — статистика переезжает на VK-профиль
// («привязка»), пока тот пуст.
func (s *Store) EnsureProfile(vkID int64, guestID, name string) (string, error) {
	if vkID == 0 && !guestIDRx.MatchString(guestID) {
		return "", nil // аноним без обоих идентификаторов — не запоминаем
	}
	target := fmt.Sprintf("g:%s", guestID)
	if vkID != 0 {
		target = fmt.Sprintf("vk:%d", vkID)
	}
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO players (id, name, vk_id, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, vk_id=excluded.vk_id, updated_at=excluded.updated_at`,
		target, name, vkID, now)
	if err != nil {
		return "", err
	}

	if vkID != 0 && guestIDRx.MatchString(guestID) {
		guest := "g:" + guestID
		var ggames int
		err := tx.QueryRow(`SELECT games FROM players WHERE id=?`, guest).Scan(&ggames)
		if err == nil && ggames > 0 {
			var tgames int
			err := tx.QueryRow(`SELECT games FROM players WHERE id=?`, target).Scan(&tgames)
			if err == sql.ErrNoRows || err == nil && tgames == 0 {
				if _, err := tx.Exec(`DELETE FROM players WHERE id=?`, target); err != nil {
					return "", err
				}
				if _, err := tx.Exec(`UPDATE players SET id=?, name=?, vk_id=?, updated_at=? WHERE id=?`,
					target, name, vkID, now, guest); err != nil {
					return "", err
				}
			}
		}
	}
	return target, tx.Commit()
}

// kFactor: первые партии — крутая лестница (быстрая сходимость), дальше классика.
func kFactor(games int) float64 {
	if games < 10 {
		return 40
	}
	return 32
}

func expected(a, b float64) float64 { return 1 / (1 + math.Pow(10, (b-a)/400)) }

// RecordGame фиксирует итог партии и пересчитывает ЭЛО обоим.
// Пустой id участника (аноним) — партия в рейтинг не входит.
func (s *Store) RecordGame(winnerID, loserID string) (wElo, wDelta, lElo, lDelta int, err error) {
	if winnerID == "" || loserID == "" || winnerID == loserID {
		return 0, 0, 0, 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	var w, l Player
	errW := tx.QueryRow(`SELECT id, name, elo, games, wins FROM players WHERE id=?`, winnerID).Scan(&w.ID, &w.Name, &w.Elo, &w.Games, &w.Wins)
	errL := tx.QueryRow(`SELECT id, name, elo, games, wins FROM players WHERE id=?`, loserID).Scan(&l.ID, &l.Name, &l.Elo, &l.Games, &l.Wins)
	if errW == sql.ErrNoRows || errL == sql.ErrNoRows {
		return 0, 0, 0, 0, nil // профиль пропал — партия мимо рейтинга
	}
	if errW != nil || errL != nil {
		return 0, 0, 0, 0, errW
	}

	expW := expected(float64(w.Elo), float64(l.Elo))
	// Новый ЭЛО округляем и дельту считаем от округлённых — иначе показанное
	// игроку «+16» разошлось бы с записанным рейтингом.
	newW := int(math.Round(float64(w.Elo) + kFactor(w.Games)*(1-expW)))
	newL := int(math.Round(float64(l.Elo) + kFactor(l.Games)*(0-(1-expW))))

	if _, err = tx.Exec(`UPDATE players SET elo=?, games=games+1, wins=wins+1, updated_at=? WHERE id=?`,
		newW, time.Now().Unix(), winnerID); err != nil {
		return
	}
	if _, err = tx.Exec(`UPDATE players SET elo=?, games=games+1, updated_at=? WHERE id=?`,
		newL, time.Now().Unix(), loserID); err != nil {
		return
	}
	if _, err = tx.Exec(`INSERT INTO games (winner, loser, finished_at) VALUES (?, ?, ?)`,
		winnerID, loserID, time.Now().Unix()); err != nil {
		return
	}
	return newW, newW - w.Elo, newL, newL - l.Elo, tx.Commit()
}

// Leaders — топ по рейтингу: только сыгравшие минимум minGames (защита от
// однодневных ботов с 1500 из воздуха). meId возвращает позицию этого
// игрока, даже если он не в топе.
func (s *Store) Leaders(limit int, meID string, minGames int) (leaders []Player, me *Player, meRank int, err error) {
	rows, err := s.db.Query(`SELECT id, name, elo, games, wins FROM players
		WHERE games >= ? ORDER BY elo DESC, games DESC LIMIT ?`, minGames, limit)
	if err != nil {
		return nil, nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Player
		if err := rows.Scan(&p.ID, &p.Name, &p.Elo, &p.Games, &p.Wins); err != nil {
			return nil, nil, 0, err
		}
		leaders = append(leaders, p)
	}
	if meID == "" {
		return leaders, nil, 0, nil
	}
	me = &Player{ID: meID}
	err = s.db.QueryRow(`SELECT name, elo, games, wins FROM players WHERE id=?`, meID).
		Scan(&me.Name, &me.Elo, &me.Games, &me.Wins)
	if errors.Is(err, sql.ErrNoRows) {
		return leaders, nil, 0, nil
	}
	if err != nil {
		return nil, nil, 0, err
	}
	err = s.db.QueryRow(`SELECT COUNT(*)+1 FROM players WHERE games >= ? AND (elo > ? OR (elo = ? AND games > ?))`,
		minGames, me.Elo, me.Elo, me.Games).Scan(&meRank)
	if err != nil {
		return leaders, me, 0, err
	}
	return leaders, me, meRank, nil
}
