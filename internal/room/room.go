package room

import (
	"crypto/rand"
	"errors"
	"math/big"
	"sync"

	"seabattle/internal/game"
)

const (
	stateWaiting  = iota // ждём второго игрока
	statePlacing         // оба на связи, расстановка кораблей
	statePlaying         // бой
	stateFinished        // игра окончена
)

// player — участник комнаты.
type player struct {
	name  string
	board *game.Board
	ready bool
	c     *Client
}

// Room — комната на двух игроков с общим состоянием партии.
type Room struct {
	code    string
	mu      sync.Mutex
	state   int
	players [2]*player // nil, если место свободно
	turn    int        // чей ход (индекс в players)
}

// Code возвращает код комнаты для приглашения второго игрока.
func (r *Room) Code() string { return r.code }

func (r *Room) sideOf(p *player) int {
	if r.players[0] == p {
		return 0
	}
	return 1
}

// addPlayer занимает первое свободное место.
func (r *Room) addPlayer(c *Client, name string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.players {
		if p == nil {
			r.players[i] = &player{name: name, board: game.NewBoard(), c: c}
			c.room, c.side = r, i
			if r.players[1-i] != nil {
				r.state = statePlacing
			}
			return i, nil
		}
	}
	return 0, errors.New("комната заполнена")
}

// removePlayer удаляет игрока (обрыв связи) и рассылает уведомления.
func (r *Room) removePlayer(c *Client, m *Manager) {
	r.mu.Lock()
	slot := -1
	for i, p := range r.players {
		if p != nil && p.c == c {
			slot = i
		}
	}
	if slot == -1 {
		r.mu.Unlock()
		return
	}
	r.players[slot] = nil
	other := r.players[1-slot]
	playing := r.state == stateWaiting || r.state == statePlacing || r.state == statePlaying
	r.state = stateFinished
	if other != nil && playing {
		other.c.sendJSON(leftMsg{Type: "opponent_left"})
		other.c.close()
	}
	empty := r.players[0] == nil && r.players[1] == nil
	r.mu.Unlock()
	if empty || playing {
		m.remove(r.code)
	}
}

// handlePlace принимает расстановку кораблей.
func (r *Room) handlePlace(c *Client, msg placeMsg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != statePlacing {
		c.sendJSON(errorMsg{Type: "error", Message: "сейчас нельзя расставлять корабли"})
		return
	}
	p := r.players[c.side]
	if err := p.board.PlaceFleet(msg.Fleet); err != nil {
		c.sendJSON(errorMsg{Type: "error", Message: err.Error()})
		return
	}
	p.ready = true
	c.sendJSON(placedMsg{Type: "placed"})
	other := r.players[1-c.side]
	if other == nil || !other.ready {
		return
	}
	// Оба готовы — начинаем бой; первым ходит игрок 0.
	r.state = statePlaying
	r.turn = 0
	for i, pl := range r.players {
		pl.c.sendJSON(startMsg{Type: "start", YouTurn: r.turn == i})
	}
}

// handleFire обрабатывает выстрел.
func (r *Room) handleFire(c *Client, msg fireMsg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != statePlaying {
		return
	}
	if r.turn != c.side {
		c.sendJSON(errorMsg{Type: "error", Message: "сейчас не ваш ход"})
		return
	}
	shooter := r.players[c.side]
	defender := r.players[1-c.side]
	res, err := defender.board.Shoot(msg.X, msg.Y)
	if err != nil {
		c.sendJSON(errorMsg{Type: "error", Message: err.Error()})
		return
	}
	shot := shotMsg{Type: "shot", X: msg.X, Y: msg.Y, Hit: res.Hit, Sunk: res.Sunk, Cells: res.Cells, Area: res.Area}

	// Промах — ход переходит; попадание — стреляющий ходит снова.
	if !res.Hit {
		r.turn = 1 - c.side
	}

	// Стреляющий видит результат на поле противника.
	target := shot
	target.Target = "enemy"
	shooter.c.sendJSON(target)
	// Защитник видит выстрел на своём поле и чей теперь ход.
	target = shot
	target.Target = "own"
	defender.c.sendJSON(target)

	if defender.board.AllSunk() {
		r.state = stateFinished
		shooter.c.sendJSON(gameOverMsg{Type: "game_over", YouWon: true})
		defender.c.sendJSON(gameOverMsg{Type: "game_over", YouWon: false})
		return
	}
	// Явное сообщение о том, чей ход, обоим игрокам.
	for i, pl := range r.players {
		pl.c.sendJSON(startMsg{Type: "start", YouTurn: r.turn == i})
	}
}

// Manager — реестр комнат.
type Manager struct {
	mu    sync.Mutex
	rooms map[string]*Room
}

// NewManager создаёт пустой менеджер комнат.
func NewManager() *Manager {
	return &Manager{rooms: make(map[string]*Room)}
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newCode() string {
	out := make([]byte, 5)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		if err != nil {
			panic(err)
		}
		out[i] = codeAlphabet[n.Int64()]
	}
	return string(out)
}

// Create создаёт новую комнату и возвращает её код.
func (m *Manager) Create() *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		code := newCode()
		if _, exists := m.rooms[code]; exists {
			continue
		}
		r := &Room{code: code}
		m.rooms[code] = r
		return r
	}
}

// Join находит комнату с свободным местом.
func (m *Manager) Join(code string) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rooms[code]
	if !ok {
		return nil, errors.New("комната не найдена")
	}
	for _, p := range r.players {
		if p == nil {
			return r, nil
		}
	}
	return nil, errors.New("комната заполнена")
}

func (m *Manager) remove(code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rooms, code)
}
