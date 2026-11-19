//go:build ignore

// Воспроизведение: есть приватная комната A и публичная B.
// Должен ли A увидеть B в витрине? Пишем все типы сообщений A 15 секунд.
// go run scripts/vitrina.go ws://localhost:8091/ws
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func dial(u string) *websocket.Conn {
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		panic(err)
	}
	return c
}

func main() {
	u := os.Args[1]

	a := dial(u) // приватная комната
	a.WriteJSON(map[string]any{"type": "create", "name": "Приват"})

	b := dial(u) // публичная комната ждёт соперника
	b.WriteJSON(map[string]any{"type": "find", "name": "Публичный"})

	// читаем всё, что приходит A, 13 секунд ( POLL интервала 10 с )
	done := make(chan struct{})
	go func() { // эмулируем браузерный опрос: list каждые ~2 с
		for {
			time.Sleep(2 * time.Second)
			a.WriteJSON(map[string]any{"type": "list"})
		}
	}()
	go func() {
		for {
			a.SetReadDeadline(time.Now().Add(14 * time.Second))
			var m map[string]any
			if err := a.ReadJSON(&m); err != nil {
				fmt.Println("A: чтение завершено:", err)
				close(done)
				return
			}
			fmt.Printf("A получил: %v %v\n", m["type"], m["open"])
			if m["type"] == "rooms" {
				close(done)
				return
			}
		}
	}()
	<-done
	a.Close()
	b.Close()
}
