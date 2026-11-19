package room

import (
	"encoding/json"
	"testing"
	"time"

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

func TestFindMatchmaking(t *testing.T) {
	m := NewManager()

	// Первый «Найти бой» — своей комнаты нет, игрок ждёт в новой публичной.
	a := testClient()
	data, _ := json.Marshal(map[string]any{"type": "find", "name": "А"})
	a.handle(m, data)
	roomA := waitFor(t, a, "room")
	waitFor(t, a, "rooms") // витрина открытых комнат

	// Второй находит первую комнату и присоединяется.
	b := testClient()
	data, _ = json.Marshal(map[string]any{"type": "find", "name": "Б"})
	b.handle(m, data)
	roomB := waitFor(t, b, "room")
	if roomA["code"] != roomB["code"] {
		t.Fatal("оба должны оказаться в одной комнате")
	}
	if waitFor(t, a, "opponent")["name"] != "Б" {
		t.Error("А должен увидеть имя Б")
	}
	if waitFor(t, b, "opponent")["name"] != "А" {
		t.Error("Б должен увидеть имя А")
	}

	// Игра идёт как обычно.
	r := m.rooms[roomA["code"].(string)]
	r.handlePlace(a, placeMsg{Fleet: testFleet()})
	r.handlePlace(b, placeMsg{Fleet: testFleet()})
	start := waitFor(t, a, "start")
	if start["youTurn"] != true {
		t.Error("первым должен ходить тот, кто ждал (сторона 0)")
	}
}

func TestFindSkipsPrivateAndDead(t *testing.T) {
	m := NewManager()

	// Приватная комната с полным составом дразнить не должна.
	priv := m.Create()
	pa, pb := testClient(), testClient()
	priv.addPlayer(pa, "X")
	priv.addPlayer(pb, "Y")

	// Публичная, но создатель offline — тоже мимо.
	dead := m.CreatePublic()
	d := testClient()
	dead.addPlayer(d, "D")
	dead.detach(d)

	a := testClient()
	data, _ := json.Marshal(map[string]any{"type": "find", "name": "А"})
	a.handle(m, data)
	roomA := waitFor(t, a, "room")
	if roomA["code"] == priv.code || roomA["code"] == dead.code {
		t.Error("нельзя попадать в приватную или «мёртвую» комнату")
	}
	// Создатель жив — комната появится в витрине у следующего искателя.
	b := testClient()
	data, _ = json.Marshal(map[string]any{"type": "find", "name": "Б"})
	b.handle(m, data)
	// Б нашёл комнату А — коды совпадут.
	roomB := waitFor(t, b, "room")
	if roomB["code"] != roomA["code"] {
		t.Error("живая публичная комната должна подбираться")
	}
}

func TestCreatorPlacesBeforeGuest(t *testing.T) {
	// Создатель жмёт «В бой!» до прихода гостя: сервер обязан принять
	// флот и начать бой сразу, как только гость тоже будет готов.
	m := NewManager()
	r := m.Create()
	a := testClient()
	r.addPlayer(a, "Хозяин")
	r.handlePlace(a, placeMsg{Fleet: testFleet()})
	waitFor(t, a, "placed")

	b := testClient()
	r.addPlayer(b, "Гость")
	r.handlePlace(b, placeMsg{Fleet: testFleet()})
	sa := waitFor(t, a, "start")
	sb := waitFor(t, b, "start")
	if sa["youTurn"] != true || sb["youTurn"] != false {
		t.Errorf("ход должен быть у хозяина: %v %v", sa, sb)
	}
}

func TestChat(t *testing.T) {
	m := NewManager()
	r := m.Create()
	a, b := testClient(), testClient()
	r.addPlayer(a, "А")
	r.addPlayer(b, "Б")
	r.handlePlace(a, placeMsg{Fleet: testFleet()})

	r.handleChat(a, chatMsg{Type: "chat", Text: "  Привет, играй честно  "})
	own := waitFor(t, a, "chat")
	if own["own"] != true || own["name"] != "А" || own["text"] != "Привет, играй честно" {
		t.Errorf("эхо отправителю: %v", own)
	}
	theirs := waitFor(t, b, "chat")
	if theirs["own"] != false || theirs["name"] != "А" {
		t.Errorf("сопернику: %v", theirs)
	}

	// Антифлуд: мгновенное повторное сообщение отклоняется.
	r.handleChat(a, chatMsg{Type: "chat", Text: "ещё-ещё"})
	if _, ok := drain(a, "chat"); ok {
		t.Error("второе сообщение в окне rate-limit не должно проходить")
	}
	if _, ok := drain(b, "chat"); ok {
		t.Error("итоговое сообщение не должно дойти сопернику")
	}

	// Обрыв и возврат: история чата восстановлена.
	r.detach(a)
	a2 := testClient()
	data, _ := json.Marshal(rejoinMsg{Type: "rejoin", Token: r.players[0].token})
	a2.handle(m, data)
	waitFor(t, a2, "room")
	waitFor(t, a2, "snapshot")
	replayed := waitFor(t, a2, "chat")
	if replayed["text"] != "Привет, играй честно" || replayed["own"] != true {
		t.Errorf("история чата не восстановилась: %v", replayed)
	}
}

// drain возвращает первое сообщение типа typ из буфера, если оно там есть.
func drain(c *Client, typ string) (map[string]any, bool) {
	for {
		select {
		case data, ok := <-c.send:
			if !ok {
				return nil, false
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			if m["type"] == typ {
				return m, true
			}
		default:
			return nil, false
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
	battleGrace = 50 * time.Millisecond
	defer func() { battleGrace = 5 * time.Minute }()

	m := NewManager()
	r := m.Create()
	a, b := testClient(), testClient()
	r.addPlayer(a, "A")
	r.addPlayer(b, "B")
	r.handlePlace(a, placeMsg{Fleet: testFleet()})
	r.handlePlace(b, placeMsg{Fleet: testFleet()})
	waitFor(t, a, "start")

	r.detach(b)
	waitFor(t, a, "opponent_off") // сразу — «временный обрыв»
	time.Sleep(150 * time.Millisecond)
	waitFor(t, a, "opponent_left") // grace истёк — уход окончателен

	m.mu.Lock()
	_, exists := m.rooms[r.code]
	m.mu.Unlock()
	if exists {
		t.Error("комната должна быть удалена после ухода игрока")
	}
}

func TestRejoinDuringBattle(t *testing.T) {
	battleGrace = 5 * time.Second // чтобы grace точно не истёк во время теста
	defer func() { battleGrace = 5 * time.Minute }()

	m := NewManager()
	r := m.Create()
	a, b := testClient(), testClient()
	r.addPlayer(a, "A")
	r.addPlayer(b, "B")
	tokenA := r.players[0].token
	r.handlePlace(a, placeMsg{Fleet: testFleet()})
	r.handlePlace(b, placeMsg{Fleet: testFleet()})
	waitFor(t, a, "start")

	// A выстрелил и «свернул браузер».
	c0 := shipCells()[0]
	r.handleFire(a, fireMsg{X: c0.X, Y: c0.Y})
	waitFor(t, a, "shot")
	r.detach(a)
	waitFor(t, b, "opponent_off")

	// A возвращается по токену через новое соединение — как это делает
	// браузер: сообщение rejoin через общий обработчик.
	a2 := testClient()
	data, err := json.Marshal(rejoinMsg{Type: "rejoin", Token: tokenA})
	if err != nil {
		t.Fatal(err)
	}
	a2.handle(m, data)
	waitFor(t, a2, "room")
	snap := waitFor(t, a2, "snapshot")
	if snap["phase"] != "playing" {
		t.Fatalf("ожидался бой, получили %v", snap["phase"])
	}
	if snap["youTurn"] != true {
		t.Error("ход должен остаться у A")
	}
	if snap["opponent"] != "B" {
		t.Errorf("имя соперника: %v", snap["opponent"])
	}
	fleet, ok := snap["fleet"].([]any)
	if !ok || len(fleet) != 10 {
		t.Errorf("флот в снимке не восстановлен: %v", snap["fleet"])
	}
	marks, _ := snap["board"].([]any) // на поле A выстрелов ещё нет
	if len(marks) != 0 {
		t.Errorf("на поле A не должно быть выстрелов: %v", marks)
	}
	enemy, _ := snap["enemy"].([]any) // один выстрел A по полю B
	if len(enemy) != 1 || enemy[0].(map[string]any)["hit"] != true {
		t.Errorf("выстрел A не восстановлен: %v", enemy)
	}
	waitFor(t, b, "opponent_back")

	// A может продолжать игру.
	c1 := shipCells()[1]
	r.handleFire(a2, fireMsg{X: c1.X, Y: c1.Y})
	shot := waitFor(t, a2, "shot")
	if shot["hit"] != true {
		t.Error("выстрел после rejoin не работает")
	}
}

// Гость потерял код-сессию (мессенджер открыл ссылку в новой вкладке) —
// вход по коду возвращает его на его же место.
func TestLobbyTakeoverByCode(t *testing.T) {
	m := NewManager()
	r := m.Create()
	a, b := testClient(), testClient()
	r.addPlayer(a, "A")
	r.addPlayer(b, "B") // теперь state = placing
	r.detach(b)         // связь с B оборвалась

	if _, err := m.Join(r.code); err != nil {
		t.Fatalf("войти по коду можно: %v", err)
	}
	b2 := testClient()
	if side, err := r.addPlayer(b2, "B"); err != nil || side != 1 {
		t.Fatalf("занять слот ушедшего: side=%d err=%v", side, err)
	}
	if r.players[1].c != b2 {
		t.Error("новое соединение не привязано к слоту B")
	}
}

func TestRejoinByCodeInLobby(t *testing.T) {
	m := NewManager()
	r := m.Create()
	a := testClient()
	r.addPlayer(a, "A")
	r.detach(a) // телефон порвал связь, комната живёт по TTL

	// Гость заходит по коду — должен получить место, а не «комната заполнена».
	guest := testClient()
	if _, err := m.Join(r.code); err != nil {
		t.Fatalf("в комнату с отключившимся создателем можно войти: %v", err)
	}
	if side, err := r.addPlayer(guest, "G"); err != nil || side != 1 {
		t.Fatalf("гость должен занять свободный слот: side=%d err=%v", side, err)
	}
}
