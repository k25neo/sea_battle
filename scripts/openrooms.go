//go:build ignore

// Держит открытую комнату на живом сервере ~6 с, чтобы успеть
// curl https://printlab3d.ru/sea/api/rooms
// go run scripts/openrooms.go wss://printlab3d.ru/sea/ws
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	c, _, err := websocket.DefaultDialer.Dial(os.Args[1], nil)
	if err != nil {
		panic(err)
	}
	c.WriteJSON(map[string]any{"type": "find", "name": "Витринный"})
	for {
		c.SetReadDeadline(time.Now().Add(8 * time.Second))
		var m map[string]any
		if err := c.ReadJSON(&m); err != nil {
			break
		}
		if m["type"] == "room" {
			fmt.Println("комната открыта:", m["code"], "— проверяйте /api/rooms")
		}
	}
	c.Close()
}
