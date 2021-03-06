package main

import (
	"github.com/gorilla/websocket"
	"net/http"
	"strings"
	"time"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return strings.HasPrefix(r.RemoteAddr, "127.0.0.1")
	},
}

type SocketHub struct {
	clients map[*SocketClient]bool

	broadcast  chan []byte
	register   chan *SocketClient
	unregister chan *SocketClient
}

type SocketClient struct {
	conn *websocket.Conn
	send chan []byte
}

func NewSocketHub() *SocketHub {
	h := SocketHub{
		broadcast:  make(chan []byte),
		register:   make(chan *SocketClient),
		unregister: make(chan *SocketClient),
		clients:    make(map[*SocketClient]bool),
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		h.HandleSocket(w, r)
	})

	go func() {
		err := http.ListenAndServe("127.0.0.1:42000", nil)

		log.MustPanic(err)
	}()

	go func() {
		for {
			select {
			case client := <-h.register:
				h.clients[client] = true

				info.ActiveConnections = len(h.clients)

				log.DebugF("-> New client connected (%d total)\n", len(h.clients))
			case client := <-h.unregister:
				if _, ok := h.clients[client]; ok {
					delete(h.clients, client)
					close(client.send)

					_ = client.conn.Close()

					info.ActiveConnections = len(h.clients)

					log.DebugF("-> Client disconnected (%d total)\n", len(h.clients))
				}
			case message := <-h.broadcast:
				sent := false

				for client := range h.clients {
					select {
					case client.send <- message:
						sent = true
					default:
						info.FailedSend++

						h.unregister <- client
					}
				}

				if sent {
					info.SentCommands++
				}
			}
		}
	}()

	return &h
}

func (h *SocketHub) DisconnectAll() {
	for client := range h.clients {
		h.unregister <- client
	}
}

func (h *SocketHub) HandleSocket(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warning("Failed to upgrade socket: " + err.Error())

		info.LastError = err

		return
	}

	_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))

	conn := SocketClient{
		conn: ws,
		send: make(chan []byte),
	}

	h.DisconnectAll()

	h.register <- &conn

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer func() {
			ticker.Stop()
		}()

		for {
			select {
			case msg := <-conn.send:
				_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))

				if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
					h.unregister <- &conn

					info.LastError = err

					return
				}
			case <-ticker.C:
				_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
					h.unregister <- &conn

					info.LastError = err

					return
				}
			}
		}
	}()
}
