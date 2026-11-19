package room

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"seabattle/internal/game"
)

const (
	stateWaiting  = iota // ждём второго игрока
	statePlacing         // оба на связи, расстановка кораблей
	statePlaying         // бой
	stateFinished        // игра окончена
)

// Таймауты жизни комнаты после обрыва связи. Мобильный браузер рвёт
// WebSocket при сворачивании, поэтому игрок может отсутствовать минуты.
var (
	battleGrace = 5 * time.Minute // игрок пропал во время боя
	lobbyTTL    = 15 * time.Minute
	endCleanup  = 2 * time.Minute
)

// player — участник комнаты; переживает обрыв соединения.
type player struct {
	name  string
	board *game.Board
	ready bool
	token string
	c     *Client // nil, если связь оборвалась
}

// Room — комната на двух игроков с общим состоянием партии.
type Room struct {
	code string
	m    *Manager

	mu      sync.Mutex
	state   int
	players [2]*player // nil, если место свободно
	turn    int        // чей ход (индекс в players)

	grace *time.Timer // ждём возвращения отключившегося
	ttl   *time.Timer // чистим пустую/завершённую комнату
}

// Code возвращает код комнаты для приглашения второго игрока.
func (r *Room) Code() string { return r.code }

func newToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func (r *Room) sideOf(p *player) int {
	if r.players[0] == p {
		return 0
	}
	return 1
}

func (r *Room) opponentOf(p *player) *player {
	return r.players[1-r.sideOf(p)]
}

func (r *Room) stopTimers() {
	if r.grace != nil {
		r.grace.Stop()
		r.grace = nil
	}
	if r.ttl != nil {
		r.ttl.Stop()
		r.ttl = nil
	}
}

// addPlayer подключает соединение к комнате: занимает свободный слот или
// (в лобби) возвращает в слот отключившегося игрока при входе по коду.
func (r *Room) addPlayer(c *Client, name string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.players {
		if p == nil {
			r.players[i] = &player{name: name, board: game.NewBoard(), token: newToken(), c: c}
			r.attach(c, i)
			if r.players[1-i] != nil {
				r.state = statePlacing
				r.stopTimers()
			}
			return i, nil
		}
	}
	// Места заняты: в лобби можно «занять» отключившегося игрока — иначе
	// создатель, у которого телефон порвал соединение, выбросил бы гостя
	// из комнаты, хотя сам ещё числится в ней.
	if r.state <= statePlacing {
		for i, p := range r.players {
			if p.c == nil {
				r.players[i] = &player{name: name, board: game.NewBoard(), token: newToken(), c: c}
				r.attach(c, i)
				return i, nil
			}
		}
	}
	return 0, errors.New("комната заполнена")
}

func (r *Room) attach(c *Client, side int) {
	c.room, c.side = r, side
	c.token = r.players[side].token
	r.stopTimers()
}

// rejoin возвращает отключившегося игрока в его слот по токену сессии.
func (r *Room) rejoin(c *Client, token string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.players {
		if p != nil && p.token == token {
			if p.c != nil && p.c != c {
				p.c.close() // прежнее соединение живёт в другой вкладке — закрываем
			}
			p.c = c
			r.attach(c, i)
			return i, nil
		}
	}
	return 0, errors.New("сессия не найдена, начните новую игру")
}

// detach обрабатывает обрыв соединения клиента.
func (r *Room) detach(c *Client) {
	r.mu.Lock()
	p := r.players[c.side]
	if p == nil || p.c != c { // слот уже занят другим соединением
		r.mu.Unlock()
		return
	}
	p.c = nil
	other := r.opponentOf(p)
	if r.state == stateFinished {
		if other == nil || other.c == nil {
			r.mu.Unlock()
			r.m.remove(r.code)
			return
		}
		r.mu.Unlock()
		return
	}
	if other != nil && other.c != nil {
		other.c.sendJSON(offMsg{Type: "opponent_off"})
	}
	grace := lobbyTTL
	if r.state == statePlaying {
		grace = battleGrace
	}
	// Если вернутся некому (комната пуста) — просто чистим по TTL.
	r.grace = time.AfterFunc(grace, func() { r.expire(c) })
	r.mu.Unlock()
}

// expire срабатывает, если отключившийся игрок не вернулся за grace-период.
func (r *Room) expire(c *Client) {
	r.mu.Lock()
	p := r.players[c.side]
	if p != nil && p.c != nil { // вернулся — ничего не делаем
		r.mu.Unlock()
		return
	}
	other := r.opponentOf(p)
	r.state = stateFinished
	r.stopTimers()
	if other != nil && other.c != nil {
		other.c.sendJSON(leftMsg{Type: "opponent_left"})
		other.c.close()
		r.mu.Unlock()
		r.m.remove(r.code)
		return
	}
	r.mu.Unlock()
	r.m.remove(r.code)
}

// scheduleCleanup ставит удаление комнаты после завершения игры.
func (r *Room) scheduleCleanup() {
	r.stopTimers()
	r.ttl = time.AfterFunc(endCleanup, func() { r.m.remove(r.code) })
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
	other := r.opponentOf(p)
	if other == nil || !other.ready {
		return
	}
	// Оба готовы — начинаем бой; первым ходит игрок 0.
	r.state = statePlaying
	r.turn = 0
	r.m.gamesStarted.Add(1)
	for i, pl := range r.players {
		if pl.c != nil {
			pl.c.sendJSON(startMsg{Type: "start", YouTurn: r.turn == i})
		}
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
	defender := r.opponentOf(shooter)
	if defender == nil {
		return
	}
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
	if defender.c != nil {
		target = shot
		target.Target = "own"
		defender.c.sendJSON(target)
	}

	if defender.board.AllSunk() {
		r.state = stateFinished
		r.m.gamesFinished.Add(1)
		shooter.c.sendJSON(gameOverMsg{Type: "game_over", YouWon: true})
		if defender.c != nil {
			defender.c.sendJSON(gameOverMsg{Type: "game_over", YouWon: false})
		}
		r.scheduleCleanup()
		return
	}
	// Явное сообщение о том, чей ход, обоим игрокам.
	for i, pl := range r.players {
		if pl.c != nil {
			pl.c.sendJSON(startMsg{Type: "start", YouTurn: r.turn == i})
		}
	}
}

// snapshot собирает полную картину для переподключившегося игрока.
func (r *Room) snapshot(p *player) snapshotMsg {
	s := snapshotMsg{Type: "snapshot"}
	switch r.state {
	case stateWaiting:
		s.Phase = "waiting"
	case statePlacing:
		s.Phase = "placing"
		s.Ready = p.ready
		s.Fleet, _, _ = p.board.Report()
	case statePlaying:
		s.Phase = "playing"
		s.YouTurn = r.turn == r.sideOf(p)
		other := r.opponentOf(p)
		if other != nil {
			s.Opponent = other.name
			var bm, em []game.Mark
			s.Fleet, bm, s.Area = p.board.Report()
			_, em, s.EnemyArea = other.board.Report()
			s.Board = toMarks(bm)
			s.Enemy = toMarks(em)
		}
	case stateFinished:
		s.Phase = "finished"
	}
	if other := r.opponentOf(p); other != nil {
		s.Opponent = other.name
	}
	return s
}

func toMarks(ms []game.Mark) []markMsg {
	out := make([]markMsg, len(ms))
	for i, m := range ms {
		out[i] = markMsg{X: m.X, Y: m.Y, Hit: m.Hit, Sunk: m.Sunk}
	}
	return out
}

// Manager — реестр комнат.
type Manager struct {
	mu    sync.Mutex
	rooms map[string]*Room

	startedAt     time.Time
	gamesStarted  atomic.Int64 // партии, в которых оба были готовы
	gamesFinished atomic.Int64 // партии, доигранные до победы
}

// NewManager создаёт пустой менеджер комнат.
func NewManager() *Manager {
	return &Manager{rooms: make(map[string]*Room), startedAt: time.Now()}
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
		r := &Room{code: code, m: m}
		m.rooms[code] = r
		return r
	}
}

// Join находит комнату с свободным местом (или отключившимся игроком в лобби).
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
	if r.state <= statePlacing {
		for _, p := range r.players {
			if p.c == nil {
				return r, nil
			}
		}
	}
	return nil, errors.New("комната заполнена")
}

// ByToken ищет комнату по токену сессии для переподключения.
func (m *Manager) ByToken(token string) *Room {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()
	for _, r := range rooms {
		r.mu.Lock()
		found := false
		for _, p := range r.players {
			if p != nil && p.token == token {
				found = true
			}
		}
		r.mu.Unlock()
		if found {
			return r
		}
	}
	return nil
}

func (m *Manager) remove(code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rooms, code)
}

// Stats — живая сводка для страницы и админения.
type Stats struct {
	Waiting       int   `json:"waiting"`       // комнат ждут второго игрока
	Placing       int   `json:"placing"`       // комнат на расстановке
	Playing       int   `json:"playing"`       // активных боёв
	PlayersOnline int   `json:"playersOnline"` // подключённых игроков
	GamesStarted  int64 `json:"gamesStarted"`  // партий начато с запуска сервера
	GamesFinished int64 `json:"gamesFinished"` // партий доиграно до победы
	UptimeSec     int64 `json:"uptimeSec"`
}

// Stats собирает текущее состояние реестра комнат.
func (m *Manager) Stats() Stats {
	s := Stats{
		GamesStarted:  m.gamesStarted.Load(),
		GamesFinished: m.gamesFinished.Load(),
		UptimeSec:     int64(time.Since(m.startedAt).Seconds()),
	}
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()
	for _, r := range rooms {
		r.mu.Lock()
		switch r.state {
		case stateWaiting:
			s.Waiting++
		case statePlacing:
			s.Placing++
		case statePlaying:
			s.Playing++
		}
		for _, p := range r.players {
			if p != nil && p.c != nil {
				s.PlayersOnline++
			}
		}
		r.mu.Unlock()
	}
	return s
}
