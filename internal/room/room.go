package room

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"seabattle/internal/game"
	"seabattle/internal/store"
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

	turnTime = 30 * time.Second // время на ход; 0 — отключить часы
)

// player — участник комнаты; переживает обрыв соединения.
type player struct {
	name     string
	pid      string // id профиля в сторе ("" — аноним без профиля)
	board    *game.Board
	ready    bool
	token    string
	c        *Client // nil, если связь оборвалась
	lastChat time.Time
}

// Room — комната на двух игроков с общим состоянием партии.
type Room struct {
	code string
	m    *Manager

	mu        sync.Mutex
	state     int
	players   [2]*player // nil, если место свободно
	turn      int        // чей ход (индекс в players)
	public    bool       // открыта для быстрого подбора
	createdAt time.Time

	grace *time.Timer // ждём возвращения отключившегося
	ttl   *time.Timer // чистим пустую/завершённую комнату

	turnTimer *time.Timer // ограничение на ход; nil, когда считать нечего

	chat []chatEntry // история чата (для восстановления при rejoin)
}

const (
	chatMaxLen    = 300
	chatHistory   = 50
	chatRateLimit = 500 * time.Millisecond
)

type chatEntry struct {
	from int
	name string
	text string
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
	if r.turnTimer != nil {
		r.turnTimer.Stop()
		r.turnTimer = nil
	}
}

// scheduleTurnTimer перезапускает песочные часы текущего хода.
// Вызывается с захваченным r.mu.
func (r *Room) scheduleTurnTimer() {
	if r.turnTimer != nil {
		r.turnTimer.Stop()
		r.turnTimer = nil
	}
	if r.state != statePlaying || turnTime <= 0 {
		return
	}
	r.turnTimer = time.AfterFunc(turnTime, r.onTurnTimeout)
}

// onTurnTimeout передаёт ход тому, кто не дождался соперника.
func (r *Room) onTurnTimeout() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != statePlaying {
		return
	}
	r.turnTimer = nil
	r.turn = 1 - r.turn
	for i, p := range r.players {
		if p != nil && p.c != nil {
			p.c.sendJSON(timeoutMsg{Type: "timeout", YouTurn: r.turn == i})
		}
	}
	r.scheduleTurnTimer()
}

// handleRematch начинает ту же пару заново: новые поля, расстановка.
func (r *Room) handleRematch(c *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stateFinished {
		return
	}
	r.stopTimers()
	r.state = statePlacing
	r.turn = 0
	r.chat = nil
	for _, p := range r.players {
		if p != nil {
			p.board = game.NewBoard()
			p.ready = false
		}
	}
	for _, p := range r.players {
		if p != nil && p.c != nil {
			p.c.sendJSON(rematchMsg{Type: "rematch"})
		}
	}
}

// addPlayer подключает соединение к комнате: занимает свободный слот или
// (в лобби) возвращает в слот отключившегося игрока при входе по коду.
func (r *Room) addPlayer(c *Client, name string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.players {
		if p == nil {
			r.fillSlot(i, c, name)
			if r.players[1-i] != nil {
				r.state = statePlacing
				r.stopTimers()
				r.tryStart()
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
				r.fillSlot(i, c, name)
				return i, nil
			}
		}
	}
	return 0, errors.New("комната заполнена")
}

// fillSlot ставит нового игрока в слот (вызывать под r.mu).
func (r *Room) fillSlot(i int, c *Client, name string) {
	r.players[i] = &player{name: name, pid: r.m.ensureProfile(c, name), board: game.NewBoard(), token: newToken(), c: c}
	r.attach(c, i)
}

// joinIfOpen пускает игрока быстрого подбора в открытую комнату, если та
// ещё ждёт соперника и создатель на связи.
func (r *Room) joinIfOpen(c *Client, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.public || r.state != stateWaiting {
		return false
	}
	if p := r.players[0]; p == nil || p.c == nil {
		return false // создатель offline — не сажаем новичка в «мёртвую» комнату
	}
	for i, p := range r.players {
		if p == nil {
			r.fillSlot(i, c, name)
			r.state = statePlacing
			r.tryStart()
			return true
		}
	}
	return false
}

// announceOpponents сообщает обоим игрокам имена друг друга.
func (r *Room) announceOpponents() {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, b := r.players[0], r.players[1]
	if a == nil || b == nil || a.c == nil || b.c == nil {
		return
	}
	a.c.sendJSON(opponentMsg{Type: "opponent", Name: b.name})
	b.c.sendJSON(opponentMsg{Type: "opponent", Name: a.name})
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
				// Прежнее соединение живёт в другой вкладке — честно
				// предупреждаем его, что слот перехвачен, и только потом рвём.
				p.c.sendJSON(replacedMsg{Type: "replaced"})
				p.c.close()
			}
			p.c = c
			r.attach(c, i)
			if r.state == statePlaying {
				r.scheduleTurnTimer() // продолжим бой — продолжим и часы
			}
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
	// Расставлять корабли можно и в ожидании второго игрока: создатель,
	// нажавший «В бой!» до прихода гостя, иначе повис бы в мнимой готовности.
	if r.state != stateWaiting && r.state != statePlacing {
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
	r.tryStart()
}

// tryStart начинает бой, если оба игрока на месте и их флоты готовы.
// Вызывается с захваченным r.mu.
func (r *Room) tryStart() {
	a, b := r.players[0], r.players[1]
	if a == nil || b == nil || !a.ready || !b.ready || r.state > statePlacing {
		return
	}
	r.state = statePlaying
	r.turn = 0
	r.m.gamesStarted.Add(1)
	for i, p := range r.players {
		if p.c != nil {
			p.c.sendJSON(startMsg{Type: "start", YouTurn: r.turn == i})
		}
	}
	r.scheduleTurnTimer()
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
		win := gameOverMsg{Type: "game_over", YouWon: true}
		lose := gameOverMsg{Type: "game_over", YouWon: false}
		// Рейтинг пишем синхронно: локальный SQLite — миллисекунда, зато
		// «+16» на экране гарантированно совпадёт с записанным профилем.
		if r.m.store != nil && shooter.pid != "" && defender.pid != "" {
			wElo, wDelta, lElo, lDelta, err := r.m.store.RecordGame(shooter.pid, defender.pid)
			if err == nil && wElo > 0 {
				win.Elo, win.EloDelta = wElo, wDelta
				lose.Elo, lose.EloDelta = lElo, lDelta
			}
		}
		shooter.c.sendJSON(win)
		if defender.c != nil {
			defender.c.sendJSON(lose)
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
	r.scheduleTurnTimer()
}

// handleChat принимает сообщение чата и рассылает его обоим игрокам.
func (r *Room) handleChat(c *Client, msg chatMsg) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != statePlacing && r.state != statePlaying {
		return
	}
	p := r.players[c.side]
	text := sanitizeChat(msg.Text)
	if text == "" || time.Since(p.lastChat) < chatRateLimit {
		return // пусто или антифлуд
	}
	p.lastChat = time.Now()
	r.chat = append(r.chat, chatEntry{from: c.side, name: p.name, text: text})
	if len(r.chat) > chatHistory {
		r.chat = r.chat[len(r.chat)-chatHistory:]
	}
	for _, pl := range r.players {
		if pl != nil && pl.c != nil {
			pl.c.sendJSON(chatMsg{Type: "chat", Name: p.name, Text: text, Own: pl.c == c})
		}
	}
}

// replayChat отправляет историю чата переподключившемуся игроку.
func (r *Room) replayChat(p *player) {
	if p.c == nil {
		return
	}
	side := r.sideOf(p)
	for _, e := range r.chat {
		p.c.sendJSON(chatMsg{Type: "chat", Name: e.name, Text: e.text, Own: e.from == side})
	}
}

func sanitizeChat(s string) string {
	out := make([]rune, 0, chatMaxLen)
	for _, ch := range s {
		if ch == '\n' || ch == '\r' || ch == '\t' {
			ch = ' '
		}
		if ch < 0x20 {
			continue
		}
		out = append(out, ch)
		if len(out) == chatMaxLen {
			break
		}
	}
	return strings.TrimSpace(string(out))
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

	store *store.Store // профили и ЭЛО; nil — рейтинга нет (тесты, лёгкий запуск)
}

// NewManager создаёт пустой менеджер комнат.
func NewManager() *Manager {
	return &Manager{rooms: make(map[string]*Room), startedAt: time.Now()}
}

// SetStore подключает базу профилей; без неё игра живёт как жила.
func (m *Manager) SetStore(s *store.Store) { m.store = s }

// ensureProfile регистрирует вошедшего игрока; ошибки хранилища не должны
// ломать игру — игрок просто останется без рейтинга.
func (m *Manager) ensureProfile(c *Client, name string) string {
	if m.store == nil || (c.vkID == 0 && c.guestID == "") {
		return ""
	}
	pid, err := m.store.EnsureProfile(c.vkID, c.guestID, name)
	if err != nil {
		return ""
	}
	return pid
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

// Create создаёт приватную комнату (вход по ссылке/коду).
func (m *Manager) Create() *Room { return m.create(false) }

// CreatePublic создаёт комнату для быстрого подбора соперника.
func (m *Manager) CreatePublic() *Room { return m.create(true) }

func (m *Manager) create(public bool) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		code := newCode()
		if _, exists := m.rooms[code]; exists {
			continue
		}
		r := &Room{code: code, m: m, public: public, createdAt: time.Now()}
		m.rooms[code] = r
		return r
	}
}

// Find ищет открытую комнату быстрого подбора (самую старую) и приводит
// туда игрока; если таковых нет — создаёт новую публичную комнату.
func (m *Manager) Find(c *Client, name string) *Room {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].createdAt.Before(rooms[j].createdAt) })
	for _, r := range rooms {
		if r.joinIfOpen(c, name) {
			return r
		}
	}
	r := m.CreatePublic()
	if _, err := r.addPlayer(c, name); err != nil {
		return nil
	}
	return r
}

// OpenRoom — витрина открытых комнат для кнопки «Найти бой».
type OpenRoom struct {
	Code string `json:"code"`
	Host string `json:"host"`
}

// OpenRooms перечисляет публичные комнаты, которые ещё ждут соперника
// (связь создателя жива), исключая комнату exclude.
func (m *Manager) OpenRooms(exclude string) []OpenRoom {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()
	out := []OpenRoom{}
	for _, r := range rooms {
		if r.code == exclude {
			continue
		}
		r.mu.Lock()
		ok := r.public && r.state == stateWaiting
		host := ""
		if ok {
			if p := r.players[0]; p != nil && p.c != nil {
				host = p.name
			} else {
				ok = false
			}
		}
		r.mu.Unlock()
		if ok {
			out = append(out, OpenRoom{Code: r.code, Host: host})
		}
	}
	return out
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
