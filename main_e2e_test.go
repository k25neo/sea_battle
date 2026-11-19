package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"seabattle/internal/room"
)

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	if err := c.WriteJSON(v); err != nil {
		t.Fatal(err)
	}
}

// readUntil читает сообщения до нужного типа; ошибки до цели — сигнал бага.
func readUntil(t *testing.T, c *websocket.Conn, typ string) map[string]any {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var m map[string]any
		if err := c.ReadJSON(&m); err != nil {
			t.Fatalf("ждём %s: %v", typ, err)
		}
		if m["type"] == typ {
			return m
		}
		if m["type"] == "error" {
			t.Fatalf("ждём %s, получили ошибку: %v", typ, m["message"])
		}
	}
}

func TestE2EGameOverWebSocket(t *testing.T) {
	m := room.NewManager()
	srv := httptest.NewServer(newServer(m))
	defer srv.Close()

	a := dial(t, srv.URL)
	send(t, a, map[string]any{"type": "create", "name": "Анна"})
	roomMsg := readUntil(t, a, "room")
	if roomMsg["side"].(float64) != 0 {
		t.Fatal("создатель должен быть стороной 0")
	}
	code, err := m.Join(roomMsg["code"].(string))
	if err != nil || code.Code() != roomMsg["code"].(string) {
		t.Fatalf("код комнаты не найден: %v", err)
	}

	b := dial(t, srv.URL)
	send(t, b, map[string]any{"type": "join", "code": roomMsg["code"], "name": "Борис"})
	readUntil(t, b, "room")
	readUntil(t, a, "opponent")
	readUntil(t, b, "opponent")

	fleet := []any{
		[]any{c(0, 0), c(1, 0), c(2, 0), c(3, 0)},
		[]any{c(0, 2), c(1, 2), c(2, 2)},
		[]any{c(0, 4), c(1, 4), c(2, 4)},
		[]any{c(5, 0), c(6, 0)},
		[]any{c(5, 2), c(6, 2)},
		[]any{c(5, 4), c(6, 4)},
		[]any{c(0, 6)}, []any{c(0, 8)}, []any{c(2, 8)}, []any{c(4, 8)},
	}
	send(t, a, map[string]any{"type": "place", "fleet": fleet})
	readUntil(t, a, "placed")
	send(t, b, map[string]any{"type": "place", "fleet": fleet})
	readUntil(t, b, "placed")

	start := readUntil(t, a, "start")
	if start["youTurn"] != true {
		t.Error("первым должен ходить создающий комнату")
	}
	readUntil(t, b, "start")

	// Аня топит весь флот Бориса: 20 попаданий подряд, ход не переходит.
	targets := [][2]int{
		{0, 0}, {1, 0}, {2, 0}, {3, 0},
		{0, 2}, {1, 2}, {2, 2},
		{0, 4}, {1, 4}, {2, 4},
		{5, 0}, {6, 0},
		{5, 2}, {6, 2},
		{5, 4}, {6, 4},
		{0, 6}, {0, 8}, {2, 8}, {4, 8},
	}
	for i, xy := range targets {
		send(t, a, map[string]any{"type": "fire", "x": xy[0], "y": xy[1]})
		shot := readUntil(t, a, "shot")
		if shot["hit"] != true || shot["target"] != "enemy" {
			t.Fatalf("выстрел %d: %+v", i, shot)
		}
		readUntil(t, b, "shot")
		if i < len(targets)-1 {
			readUntil(t, a, "start")
			readUntil(t, b, "start")
		}
	}
	over := readUntil(t, a, "game_over")
	if over["youWon"] != true {
		t.Error("Анна должна победить")
	}
	readUntil(t, b, "game_over")
}

// Проверка работы за reverse-proxy с префиксом /sea/ — как на боевом сервере.
func TestE2ESubPathPrefix(t *testing.T) {
	m := room.NewManager()
	srv := httptest.NewServer(newServer(m))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/sea/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Морской бой") {
		t.Fatalf("страница /sea/ не отдаётся: %d", resp.StatusCode)
	}

	dial := func() *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/sea/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	a := dial()
	send(t, a, map[string]any{"type": "create", "name": "Тест"})
	msgs := make(chan map[string]any, 8)
	a.SetReadDeadline(time.Now().Add(3 * time.Second))
	go func() {
		for {
			var mm map[string]any
			if a.ReadJSON(&mm) != nil {
				return
			}
			msgs <- mm
		}
	}()
	for m := range msgs {
		if m["type"] == "room" {
			return
		}
	}
	t.Fatal("нет ответа на create через /sea/ws")
}

// Эндпоинт статистики показывает живых игроков и комнаты.
func TestE2EStatsEndpoint(t *testing.T) {
	m := room.NewManager()
	srv := httptest.NewServer(newServer(m))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	var empty map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&empty); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if empty["playersOnline"].(float64) != 0 {
		t.Errorf("на старте онлайн 0, получено %v", empty["playersOnline"])
	}

	a := dial(t, srv.URL)
	send(t, a, map[string]any{"type": "create", "name": "Стат"})
	readUntil(t, a, "room")

	resp, err = http.Get(srv.URL + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s["playersOnline"].(float64) != 1 || s["waiting"].(float64) != 1 {
		t.Errorf("ожидается 1 игрок и 1 комната ожидания: %v", s)
	}
}

func c(x, y int) map[string]any { return map[string]any{"x": x, "y": y} }
