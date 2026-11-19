//go:build ignore

// Быстрая проверка матчмейкинга на живом сервере:
// go run scripts/findcheck.go wss://printlab3d.ru/sea/ws
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func waitType(c *websocket.Conn, typ string) map[string]any {
	for {
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
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
	a.WriteJSON(map[string]any{"type": "find", "name": "Искатель-1"})
	ra := waitType(a, "room")
	waitType(a, "rooms")
	fmt.Println("первый ждёт в комнате", ra["code"])

	b := dial(u)
	b.WriteJSON(map[string]any{"type": "find", "name": "Искатель-2"})
	rb := waitType(b, "room")
	opp := waitType(a, "opponent")
	fmt.Println("второй привёдён:", rb["code"] == ra["code"], ", первый видит:", opp["name"])

	a.Close()
	b.Close()
	if rb["code"] != ra["code"] || opp["name"] != "Искатель-2" {
		os.Exit(1)
	}
	fmt.Println("OK")
}
