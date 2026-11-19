package room

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	sendBufferSize = 32
	maxMsgSize     = 64 << 10 // 64 КБ более чем достаточно
)

// Client — WebSocket-соединение одного игрока.
type Client struct {
	conn   *websocket.Conn
	send   chan []byte
	closed sync.Once

	room  *Room
	side  int
	token string // сессия игрока в комнате
}

func newClient(conn *websocket.Conn) *Client {
	return &Client{conn: conn, send: make(chan []byte, sendBufferSize)}
}

// Serve запускает чтение и запись, блокирует до закрытия соединения.
func (m *Manager) Serve(conn *websocket.Conn) {
	c := newClient(conn)
	go c.writePump()
	c.readPump(m)
}

func (c *Client) sendJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default: // буфер переполнен — клиент «завис», закрываем
		c.close()
	}
}

func (c *Client) close() {
	c.closed.Do(func() {
		close(c.send)
	})
}

func (c *Client) writePump() {
	for data := range c.send {
		c.conn.SetWriteDeadline(time.Now().Add(writeWait))
		if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			break
		}
	}
	c.conn.Close()
}

func (c *Client) readPump(m *Manager) {
	defer func() {
		if c.room != nil {
			c.room.detach(c)
		}
		c.close()
	}()
	c.conn.SetReadLimit(maxMsgSize)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		c.handle(m, data)
	}
}

// handle — маршрутизация входящих сообщений.
// create/join обрабатываются до присоединения к комнате (c.room == nil).
func (c *Client) handle(m *Manager, data []byte) {
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		c.sendJSON(errorMsg{Type: "error", Message: "некорректное сообщение"})
		return
	}

	switch env.Type {
	case "create":
		if c.room != nil {
			return
		}
		var msg createMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		r := m.Create()
		side, err := r.addPlayer(c, sanitizeName(msg.Name))
		if err != nil {
			m.remove(r.code)
			c.sendJSON(errorMsg{Type: "error", Message: err.Error()})
			return
		}
		c.sendJSON(roomMsg{Type: "room", Code: r.code, Side: side, Token: c.token})

	case "join":
		if c.room != nil {
			return
		}
		var msg joinMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		r, err := m.Join(sanitizeCode(msg.Code))
		if err != nil {
			c.sendJSON(errorMsg{Type: "error", Message: err.Error()})
			return
		}
		side, err := r.addPlayer(c, sanitizeName(msg.Name))
		if err != nil {
			c.sendJSON(errorMsg{Type: "error", Message: err.Error()})
			return
		}
		c.sendJSON(roomMsg{Type: "room", Code: r.code, Side: side, Token: c.token})
		r.announceOpponents()

	case "find":
		if c.room != nil {
			return
		}
		var msg createMsg // та же форма: {type, name}
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		r := m.Find(c, sanitizeName(msg.Name))
		if r == nil {
			c.sendJSON(errorMsg{Type: "error", Message: "не удалось создать комнату"})
			return
		}
		c.sendJSON(roomMsg{Type: "room", Code: r.code, Side: c.side, Token: c.token})
		c.sendJSON(openRoomsMsg{Type: "rooms", Open: m.OpenRooms(r.code)})
		r.announceOpponents()

	case "list":
		if c.room == nil {
			return
		}
		c.room.mu.Lock()
		exclude, playing := c.room.code, c.room.state >= statePlaying
		c.room.mu.Unlock()
		if playing {
			return // в бою список не нужен
		}
		c.sendJSON(openRoomsMsg{Type: "rooms", Open: m.OpenRooms(exclude)})

	case "rejoin":
		if c.room != nil {
			return
		}
		var msg rejoinMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		r := m.ByToken(msg.Token)
		if r == nil {
			c.sendJSON(errorMsg{Type: "error", Message: "игра не найдена, начните новую"})
			return
		}
		if _, err := r.rejoin(c, msg.Token); err != nil {
			c.sendJSON(errorMsg{Type: "error", Message: err.Error()})
			return
		}
		r.mu.Lock()
		p := r.players[c.side]
		c.sendJSON(roomMsg{Type: "room", Code: r.code, Side: c.side, Token: c.token})
		c.sendJSON(r.snapshot(p))
		if other := r.opponentOf(p); other != nil && other.c != nil {
			other.c.sendJSON(backMsg{Type: "opponent_back"})
		}
		r.mu.Unlock()

	case "place":
		if c.room == nil {
			return
		}
		var msg placeMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			c.sendJSON(errorMsg{Type: "error", Message: "некорректная расстановка"})
			return
		}
		c.room.handlePlace(c, msg)

	case "fire":
		if c.room == nil {
			return
		}
		var msg fireMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		c.room.handleFire(c, msg)

	default:
		c.sendJSON(errorMsg{Type: "error", Message: "неизвестный тип сообщения"})
	}
}

func sanitizeName(name string) string {
	out := make([]rune, 0, 16)
	for _, r := range name {
		if len(out) >= 16 {
			break
		}
		switch {
		case r >= 'а' && r <= 'я', r >= 'А' && r <= 'Я',
			r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "Игрок"
	}
	return string(out)
}

func sanitizeCode(code string) string {
	out := make([]byte, 0, 8)
	for i := 0; i < len(code); i++ {
		ch := code[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			out = append(out, ch)
		} else if ch >= 'a' && ch <= 'z' {
			out = append(out, ch-32)
		}
	}
	return string(out)
}
