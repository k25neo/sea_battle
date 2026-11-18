package room

import (
	"encoding/json"
	"testing"

	"seabattle/internal/game"
)

// testClient — клиент без реального соединения: сообщения копятся в буфере send.
func testClient() *Client {
	return &Client{send: make(chan []byte, 256)}
}

func waitFor(t *testing.T, c *Client, typ string) map[string]any {
	t.Helper()
	for {
		select {
		case data, ok := <-c.send:
			if !ok {
				t.Fatalf("канал закрыт, сообщение %s не получено", typ)
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			if m["type"] == typ {
				return m
			}
		default:
			t.Fatalf("сообщение %s не найдено в буфере", typ)
		}
	}
}

func cell(x, y int) game.Cell { return game.Cell{X: x, Y: y} }

func testFleet() [][]game.Cell {
	return [][]game.Cell{
		{cell(0, 0), cell(1, 0), cell(2, 0), cell(3, 0)},
		{cell(0, 2), cell(1, 2), cell(2, 2)},
		{cell(0, 4), cell(1, 4), cell(2, 4)},
		{cell(5, 0), cell(6, 0)},
		{cell(5, 2), cell(6, 2)},
		{cell(5, 4), cell(6, 4)},
		{cell(0, 6)},
		{cell(0, 8)},
		{cell(2, 8)},
		{cell(4, 8)},
	}
}

// shipCells — все 20 клеток тестового флота.
func shipCells() []game.Cell {
	var out []game.Cell
	for _, s := range testFleet() {
		out = append(out, s...)
	}
	return out
}

func TestJoinErrors(t *testing.T) {
	m := NewManager()
	if _, err := m.Join("ZZZZZ"); err == nil {
		t.Error("несуществующая комната")
	}
	r := m.Create()
	if _, err := r.addPlayer(testClient(), "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.addPlayer(testClient(), "B"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Join(r.code); err == nil {
		t.Error("в полную комнату нельзя войти")
	}
}

func TestFullGame(t *testing.T) {
	m := NewManager()
	r := m.Create()
	a, b := testClient(), testClient()
	if side, err := r.addPlayer(a, "A"); err != nil || side != 0 {
		t.Fatalf("игрок A: side=%d err=%v", side, err)
	}
	if side, err := r.addPlayer(b, "B"); err != nil || side != 1 {
		t.Fatalf("игрок B: side=%d err=%v", side, err)
	}

	// Расстановка: оба получают placed, после второго — start обоим.
	r.handlePlace(a, placeMsg{Fleet: testFleet()})
	waitFor(t, a, "placed")
	r.handlePlace(b, placeMsg{Fleet: testFleet()})
	waitFor(t, b, "placed")

	startA := waitFor(t, a, "start")
	startB := waitFor(t, b, "start")
	if startA["youTurn"] != true || startB["youTurn"] != false {
		t.Error("первым должен ходить игрок 0")
	}

	// B пытается стрелять не в свой ход.
	r.handleFire(b, fireMsg{X: 0, Y: 9})
	waitFor(t, b, "error")

	// Неверная расстановка отклоняется (игра уже идёт — тоже ошибка).
	r.handlePlace(a, placeMsg{Fleet: [][]game.Cell{{cell(9, 9)}}})

	// A стреляет по всем 20 клеткам флота B — все попадания, ход не переходит.
	cells := shipCells()
	for _, c := range cells[:19] {
		r.handleFire(a, fireMsg{X: c.X, Y: c.Y})
		shot := waitFor(t, a, "shot")
		if shot["target"] != "enemy" || shot["hit"] != true {
			t.Fatalf("выстрел в %v: %+v", c, shot)
		}
		waitFor(t, a, "start") // ход остался у A
		waitFor(t, b, "shot")
		waitFor(t, b, "start")
	}
	last := cells[19]
	r.handleFire(a, fireMsg{X: last.X, Y: last.Y})
	waitFor(t, a, "shot")
	over := waitFor(t, a, "game_over")
	if over["youWon"] != true {
		t.Error("A должен победить")
	}
	over = waitFor(t, b, "game_over")
	if over["youWon"] != false {
		t.Error("B должен проиграть")
	}
}

func TestMissSwitchesTurn(t *testing.T) {
	m := NewManager()
	r := m.Create()
	a, b := testClient(), testClient()
	r.addPlayer(a, "A")
	r.addPlayer(b, "B")
	r.handlePlace(a, placeMsg{Fleet: testFleet()})
	r.handlePlace(b, placeMsg{Fleet: testFleet()})
	waitFor(t, a, "start")
	waitFor(t, b, "start")

	r.handleFire(a, fireMsg{X: 9, Y: 9}) // мимо
	shot := waitFor(t, a, "shot")
	if shot["hit"] != false {
		t.Fatalf("ожидался промах: %+v", shot)
	}
	start := waitFor(t, a, "start")
	if start["youTurn"] != false {
		t.Error("после промага ход должен перейти B")
	}
	start = waitFor(t, b, "start")
	if start["youTurn"] != true {
		t.Error("B должен получить ход")
	}
	// Теперь стрелять может только B.
	r.handleFire(a, fireMsg{X: 8, Y: 8})
	waitFor(t, a, "error")
	r.handleFire(b, fireMsg{X: 8, Y: 8})
	waitFor(t, b, "shot")
}

func TestOpponentLeftRemovesRoom(t *testing.T) {
	m := NewManager()
	r := m.Create()
	a, b := testClient(), testClient()
	r.addPlayer(a, "A")
	r.addPlayer(b, "B")
	r.handlePlace(a, placeMsg{Fleet: testFleet()})
	r.handlePlace(b, placeMsg{Fleet: testFleet()})
	waitFor(t, a, "start")

	r.removePlayer(b, m)
	waitFor(t, a, "opponent_left")

	m.mu.Lock()
	_, exists := m.rooms[r.code]
	m.mu.Unlock()
	if exists {
		t.Error("комната должна быть удалена после ухода игрока")
	}
}
