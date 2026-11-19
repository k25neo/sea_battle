//go:build ignore

// Съёмка реальных экранов игры для ВК-модерации (600×1200).
//
//	Хром должен быть запущен: google-chrome --headless=new --no-sandbox \
//	  --remote-debugging-port=9333 --user-data-dir=/tmp/.../cdp about:blank
//
// go run scripts/shots.go
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

const (
	baseURL = "https://printlab3d.ru/sea/"
	wsURL   = "wss://printlab3d.ru/sea/ws"
)

type cell struct {
	X int `json:"x"`
	Y int `json:"y"`
}

func testFleet() [][]cell {
	c := func(x, y int) cell { return cell{x, y} }
	return [][]cell{
		{c(0, 0), c(1, 0), c(2, 0), c(3, 0)},
		{c(0, 2), c(1, 2), c(2, 2)},
		{c(0, 4), c(1, 4), c(2, 4)},
		{c(5, 0), c(6, 0)},
		{c(5, 2), c(6, 2)},
		{c(5, 4), c(6, 4)},
		{c(0, 6)}, {c(0, 8)}, {c(2, 8)}, {c(4, 8)},
	}
}

// ---------- минимальный клиент CDP ----------

type cdp struct {
	c   *websocket.Conn
	id  int
	out chan map[string]any
}

func dialCDP() *cdp {
	resp, err := http.Get("http://127.0.0.1:9333/json")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var targets []struct {
		Type  string `json:"type"`
		WSURL string `json:"webSocketDebuggerUrl"`
		URL   string `json:"url"`
	}
	json.NewDecoder(resp.Body).Decode(&targets)
	for _, t := range targets {
		if t.Type == "page" {
			c, _, err := websocket.DefaultDialer.Dial(t.WSURL, nil)
			if err != nil {
				panic(err)
			}
			k := &cdp{c: c, out: make(chan map[string]any, 64)}
			go k.pump()
			return k
		}
	}
	panic("page target not found")
}

func (k *cdp) pump() {
	for {
		var m map[string]any
		if err := k.c.ReadJSON(&m); err != nil {
			return
		}
		if _, ok := m["id"]; ok {
			k.out <- m
		}
	}
}

func (k *cdp) call(method string, params map[string]any) map[string]any {
	k.id++
	req := map[string]any{"id": k.id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := k.c.WriteJSON(req); err != nil {
		panic(err)
	}
	for {
		m := <-k.out
		if m["id"].(float64) == float64(k.id) {
			return m
		}
	}
}

func (k *cdp) eval(expr string) any {
	m := k.call("Runtime.evaluate", map[string]any{
		"expression": expr, "returnByValue": true, "awaitPromise": true})
	res, _ := m["result"]
	if res == nil {
		return nil
	}
	inner, _ := res.(map[string]any)["result"]
	if inner == nil {
		return nil
	}
	return inner.(map[string]any)["value"]
}

func (k *cdp) shot(path string) {
	m := k.call("Page.captureScreenshot", map[string]any{
		"format": "png", "clip": map[string]any{
			"x": 0, "y": 0, "width": 600, "height": 1200, "scale": 1}})
	res, _ := m["result"]
	data, _ := base64.StdEncoding.DecodeString(res.(map[string]any)["data"].(string))
	os.WriteFile(path, data, 0o644)
	fmt.Println("снят:", path)
}

// ---------- бот-соперник ----------

type bot struct{ c *websocket.Conn }

func (b *bot) wait(typ string) map[string]any {
	for {
		b.c.SetReadDeadline(time.Now().Add(10 * time.Second))
		var m map[string]any
		if err := b.c.ReadJSON(&m); err != nil {
			panic(fmt.Sprintf("бот ждал %q: %v", typ, err))
		}
		fmt.Printf("бот ← %v\n", m["type"])
		if m["type"] == typ {
			return m
		}
	}
}

func main() {
	k := dialCDP()
	k.call("Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 600, "height": 1200, "deviceScaleFactor": 2, "mobile": true})

	// 1. Меню
	k.call("Page.navigate", map[string]any{"url": baseURL})
	time.Sleep(4 * time.Second)
	k.shot("/tmp/opencode/vk/shot_menu_raw.png")

	// 2. Комната (экран расстановки с кодом)
	k.eval(`document.getElementById('name-input').value = 'Алексей'`)
	k.eval(`document.getElementById('btn-create').click()`)
	time.Sleep(2 * time.Second)
	code, _ := k.eval(`document.getElementById('room-code').textContent`).(string)
	fmt.Println("комната:", code)
	k.shot("/tmp/opencode/vk/shot_placement_raw.png")

	// 3. Случайная расстановка + вход бота → бой
	k.eval(`document.getElementById('btn-random').click()`)
	time.Sleep(500 * time.Millisecond)
	k.eval(`document.getElementById('btn-ready').click()`) // отправить флот серверу
	time.Sleep(500 * time.Millisecond)
	fmt.Println("браузер:", k.eval(`JSON.stringify({phase: typeof phase !== 'undefined' && phase,
		fleetSent: typeof fleetSent !== 'undefined' && fleetSent,
		ships: typeof ships !== 'undefined' && ships.length,
		ws: ws && ws.readyState})`))

	bc, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		panic(err)
	}
	b := &bot{bc}
	b.c.WriteJSON(map[string]any{"type": "join", "code": code, "name": "Борис"})
	b.wait("room")
	b.c.WriteJSON(map[string]any{"type": "place", "fleet": testFleet()})
	b.wait("start")

	k.eval(`document.getElementById('chat-input').value = 'Салют! Готов к бою?'`)
	k.eval(`document.getElementById('btn-send').click()`)
	time.Sleep(700 * time.Millisecond)
	b.c.WriteJSON(map[string]any{"type": "chat", "text": "Привет! Начинай, твой ход 🚢"})

	click := func(x, y int) {
		k.eval(fmt.Sprintf(
			`document.querySelector('#enemy-board .cell[data-x="%d"][data-y="%d"]').click()`, x, y))
		time.Sleep(800 * time.Millisecond)
	}
	// Флот бота известен (testFleet): А1 и Б1 — гарантированные попадания,
	// (7,7) — промах, ход переходит к боту.
	click(0, 0)
	click(7, 7)
	time.Sleep(600 * time.Millisecond)
	b.fireTillMiss([]cell{{9, 0}, {9, 9}, {0, 9}, {3, 3}, {6, 6}, {8, 4}})
	time.Sleep(400 * time.Millisecond)
	// Ход снова у браузера: ещё попадание и промах.
	click(1, 0)
	click(6, 6)
	time.Sleep(600 * time.Millisecond)
	b.fireTillMiss([]cell{{8, 8}, {4, 6}, {6, 4}, {2, 6}, {8, 1}, {1, 8}})
	time.Sleep(1000 * time.Millisecond)

	// Полный кадр страницы — потом отрежем окно 600×1200.
	h := k.eval(`document.documentElement.scrollHeight`)
	hf, _ := h.(float64)
	m := k.call("Page.captureScreenshot", map[string]any{
		"format": "png", "captureBeyondViewport": true,
		"clip": map[string]any{"x": 0, "y": 0, "width": 600, "height": hf, "scale": 1}})
	res, _ := m["result"]
	data, _ := base64.StdEncoding.DecodeString(res.(map[string]any)["data"].(string))
	os.WriteFile("/tmp/opencode/vk/shot_battle_raw.png", data, 0o644)
	fmt.Printf("снят: бой (страница %.0f px)\n", hf)

	b.c.Close()
	io.Discard.Write(nil)
}

// fireTillMiss стреляет по списку, пока не промахнется (после попадания
// право хода остаётся — идём дальше по списку).
func (b *bot) fireTillMiss(cells []cell) {
	for _, xy := range cells {
		b.c.WriteJSON(map[string]any{"type": "fire", "x": xy.X, "y": xy.Y})
		for {
			b.c.SetReadDeadline(time.Now().Add(8 * time.Second))
			var m map[string]any
			if err := b.c.ReadJSON(&m); err != nil {
				fmt.Println("бот: связь потеряна:", err)
				return
			}
			if m["type"] != "shot" || m["target"] != "enemy" {
				continue
			}
			if m["hit"] != true {
				return
			}
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
}
