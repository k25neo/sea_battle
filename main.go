package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"seabattle/internal/room"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// newServer собирает HTTP-обработчик: статика из web/ и WebSocket-эндпоинт /ws.
// Приложение работает и в корне, и за reverse-proxy с префиксом (например,
// nginx проксирует /sea/): префикс срезается на входе.
func newServer(m *room.Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		m.Serve(conn)
	})
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(m.Stats())
	})
	fileServer := http.FileServer(http.Dir("web"))
	mux.Handle("/", fileServer)

	stripped := http.StripPrefix("/sea", mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sea" || strings.HasPrefix(r.URL.Path, "/sea/") {
			stripped.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func main() {
	addr := flag.String("addr", ":8080", "адрес сервера")
	flag.Parse()

	log.Printf("Морской бой: http://localhost%s", *addr)
	if err := http.ListenAndServe(*addr, newServer(room.NewManager())); err != nil {
		log.Fatal(err)
	}
}
