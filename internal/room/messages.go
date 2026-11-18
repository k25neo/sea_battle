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
	Type string `json:"type"` // room
	Code string `json:"code"`
	Side int    `json:"side"` // 0 — первый игрок, 1 — второй
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
