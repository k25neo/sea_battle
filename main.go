package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"seabattle/internal/room"
	"seabattle/internal/store"
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
	mux.HandleFunc("/api/rooms", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(m.OpenRooms(""))
	})
	mux.HandleFunc("/api/leaders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		limit := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		json.NewEncoder(w).Encode(m.Leaders(limit, r.URL.Query().Get("me")))
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
	statsPath := flag.String("stats", "stats.json", "файл счётчиков партий (пусто — не сохранять)")
	dbPath := flag.String("db", "seabattle.db", "база профилей и рейтинга (пусто — без рейтинга)")
	flag.Parse()

	m := room.NewManager()
	if *dbPath != "" {
		st, err := store.Open(*dbPath)
		if err != nil {
			log.Fatalf("не открыл базу %s: %v", *dbPath, err)
		}
		m.SetStore(st)
		defer st.Close()
	}
	if *statsPath != "" {
		m.LoadCounters(*statsPath)
		done := make(chan struct{})
		go m.RunCountersKeeper(*statsPath, 30*time.Second, done)
		// systemd шлёт SIGTERM при restart: сохраним последние счётчики,
		// рестарт не съест сыгранные партии.
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			s := <-sig
			log.Printf("сигнал %s, сохраняю счётчики и выхожу", s)
			close(done)
			os.Exit(0)
		}()
	}

	log.Printf("Морской бой: http://localhost%s", *addr)
	if err := http.ListenAndServe(*addr, newServer(m)); err != nil {
		log.Fatal(err)
	}
}
