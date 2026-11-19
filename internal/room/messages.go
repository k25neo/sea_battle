package room

import "seabattle/internal/game"

// Протокол сообщений поверх WebSocket. Все сообщения — JSON с полем type.

// --- Сообщения клиента ---

type createMsg struct {
	Type string `json:"type"` // create
	Name string `json:"name"`
}

type joinMsg struct {
	Type string `json:"type"` // join
	Code string `json:"code"`
	Name string `json:"name"`
}

type placeMsg struct {
	Type  string        `json:"type"` // place
	Fleet [][]game.Cell `json:"fleet"`
}

type fireMsg struct {
	Type string `json:"type"` // fire
	X    int    `json:"x"`
	Y    int    `json:"y"`
}

// --- Сообщения сервера ---

type roomMsg struct {
	Type  string `json:"type"` // room
	Code  string `json:"code"`
	Side  int    `json:"side"`  // 0 — первый игрок, 1 — второй
	Token string `json:"token"` // токен сессии для переподключения
}

type opponentMsg struct {
	Type string `json:"type"` // opponent
	Name string `json:"name"`
}

type startMsg struct {
	Type    string `json:"type"` // start
	YouTurn bool   `json:"youTurn"`
}

type placedMsg struct {
	Type string `json:"type"` // placed — сервер принял расстановку
}

type shotMsg struct {
	Type   string      `json:"type"`   // shot
	Target string      `json:"target"` // "enemy" — ваш выстрел, "own" — выстрел по вам
	X      int         `json:"x"`
	Y      int         `json:"y"`
	Hit    bool        `json:"hit"`
	Sunk   bool        `json:"sunk"`
	Cells  []game.Cell `json:"cells,omitempty"`
	Area   []game.Cell `json:"area,omitempty"`
}

type gameOverMsg struct {
	Type   string `json:"type"` // game_over
	YouWon bool   `json:"youWon"`
}

type errorMsg struct {
	Type    string `json:"type"` // error
	Message string `json:"message"`
}

type leftMsg struct {
	Type string `json:"type"` // opponent_left
}

type offMsg struct {
	Type string `json:"type"` // opponent_off — связь оборвалась, ждём возврата
}

type backMsg struct {
	Type string `json:"type"` // opponent_back — соперник вернулся
}

// markMsg — отметка о выстреле на поле (для snapshot).
type markMsg struct {
	X    int  `json:"x"`
	Y    int  `json:"y"`
	Hit  bool `json:"hit"`
	Sunk bool `json:"sunk"`
}

// snapshotMsg — полное состояние игры для переподключившегося игрока.
type snapshotMsg struct {
	Type      string        `json:"type"`  // snapshot
	Phase     string        `json:"phase"` // waiting|placing|playing|finished
	Opponent  string        `json:"opponent,omitempty"`
	YouTurn   bool          `json:"youTurn"`
	Ready     bool          `json:"ready"`               // вы уже поставили флот
	Fleet     [][]game.Cell `json:"fleet,omitempty"`     // ваши корабли
	Board     []markMsg     `json:"board,omitempty"`     // выстрелы по вам
	Enemy     []markMsg     `json:"enemy,omitempty"`     // ваши выстрелы
	Area      []game.Cell   `json:"area,omitempty"`      // ореолы на вашем поле
	EnemyArea []game.Cell   `json:"enemyArea,omitempty"` // ореолы на поле противника
}

// rejoinMsg — переподключение после обрыва по токену сессии.
type rejoinMsg struct {
	Type  string `json:"type"` // rejoin
	Token string `json:"token"`
}

// openRoomsMsg — список открытых комнат быстрого подбора.
type openRoomsMsg struct {
	Type string     `json:"type"` // rooms
	Open []OpenRoom `json:"open"`
}
