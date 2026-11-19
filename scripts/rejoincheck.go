//go:build ignore

// Быстрая проверка reconnect-сценария на живом сервере:
// go run scripts/rejoincheck.go https://printlab3d.ru/sea/ws
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func waitType(c *websocket.Conn, typ string) map[string]any {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		var m map[string]any
		if err := c.ReadJSON(&m); err != nil {
			panic(err)
		}
		if m["type"] == typ {
			return m
		}
	}
	panic("timeout waiting " + typ)
}

func dial(wsURL string) *websocket.Conn {
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		panic(err)
	}
	return c
}

func main() {
	wsURL := os.Args[1]
	c := dial(wsURL)
	c.WriteJSON(map[string]any{"type": "create", "name": "Проверка"})
	room := waitType(c, "room")
	token, _ := room["token"].(string)
	fmt.Println("создан; токен:", token != "", "код:", room["code"])

	c.Close() // «телефон свернули»
	time.Sleep(500 * time.Millisecond)

	c2 := dial(wsURL) // «вернулись»
	c2.WriteJSON(map[string]any{"type": "rejoin", "token": token})
	room2 := waitType(c2, "room")
	snap := waitType(c2, "snapshot")
	same := room2["code"] == room["code"]
	fmt.Printf("rejoin: фаза=%v, комната та же=%v, соперник=%v\n",
		snap["phase"], same, snap["opponent"])
	if token == "" || !same || snap["phase"] != "waiting" {
		os.Exit(1)
	}
	fmt.Println("OK")
	c2.Close()
}
