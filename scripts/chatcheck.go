//go:build ignore

// Живая проверка чата: два искателя матчалятся и обмениваются сообщениями.
// go run scripts/chatcheck.go wss://printlab3d.ru/sea/ws
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func waitType(c *websocket.Conn, typ string) map[string]any {
	for {
		c.SetReadDeadline(time.Now().Add(6 * time.Second))
		var m map[string]any
		if err := c.ReadJSON(&m); err != nil {
			panic(err)
		}
		if m["type"] == typ {
			return m
		}
	}
}

func dial(u string) *websocket.Conn {
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		panic(err)
	}
	return c
}

func main() {
	u := os.Args[1]
	a := dial(u)
	a.WriteJSON(map[string]any{"type": "find", "name": "Чатист-А"})
	waitType(a, "room")

	b := dial(u)
	b.WriteJSON(map[string]any{"type": "find", "name": "Чатист-B"})
	waitType(b, "room")
	waitType(a, "opponent")

	a.WriteJSON(map[string]any{"type": "chat", "text": "Привет, готов к бою?"})
	gotB := waitType(b, "chat")
	fmt.Printf("B получил: %v: %v (own=%v)\n", gotB["name"], gotB["text"], gotB["own"])
	echo := waitType(a, "chat")
	fmt.Printf("A эхо: own=%v\n", echo["own"])

	b.WriteJSON(map[string]any{"type": "chat", "text": "Всегда! 🚢"})
	gotA := waitType(a, "chat")
	fmt.Printf("A получил: %v: %v\n", gotA["name"], gotA["text"])

	a.Close()
	b.Close()
	fmt.Println("OK")
}
