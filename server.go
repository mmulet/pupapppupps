package main

import (
	"encoding/binary"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocketServer manages WebSocket connections for streaming the desktop buffer
type WebSocketServer struct {
	clients   map[*websocket.Conn]bool
	mu        sync.RWMutex
	upgrader  websocket.Upgrader
	broadcast chan []byte
}

// NewWebSocketServer creates a new WebSocket server instance
func NewWebSocketServer() *WebSocketServer {
	return &WebSocketServer{
		clients:   make(map[*websocket.Conn]bool),
		broadcast: make(chan []byte, 10),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024 * 1024, // Large buffer for image data
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
	}
}

// HandleWebSocket handles incoming WebSocket connections
func (s *WebSocketServer) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	s.mu.Lock()
	s.clients[conn] = true
	s.mu.Unlock()

	log.Printf("New WebSocket client connected. Total clients: %d", len(s.clients))

	// Keep connection alive and handle disconnects
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.clients, conn)
			s.mu.Unlock()
			conn.Close()
			log.Printf("WebSocket client disconnected. Total clients: %d", len(s.clients))
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}

// BroadcastDesktopBuffer sends the desktop buffer to all connected clients
// The buffer format is: [width:4bytes][height:4bytes][stride:4bytes][rgba_data]
func (s *WebSocketServer) BroadcastDesktopBuffer(buffer []byte, width, height, stride int) {
	if len(buffer) == 0 {
		return
	}

	// Create message with header: width, height, stride + buffer data
	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header[0:4], uint32(width))
	binary.LittleEndian.PutUint32(header[4:8], uint32(height))
	binary.LittleEndian.PutUint32(header[8:12], uint32(stride))

	message := append(header, buffer...)

	s.mu.RLock()
	clients := make([]*websocket.Conn, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.RUnlock()

	for _, client := range clients {
		err := client.WriteMessage(websocket.BinaryMessage, message)
		if err != nil {
			log.Printf("Error sending to client: %v", err)
			client.Close()
			s.mu.Lock()
			delete(s.clients, client)
			s.mu.Unlock()
		}
	}
}

// ClientCount returns the number of connected clients
func (s *WebSocketServer) ClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// HTTPServer wraps the HTTP server with static file serving and WebSocket
type HTTPServer struct {
	wsServer *WebSocketServer
	server   *http.Server
}

// NewHTTPServer creates a new HTTP server
func NewHTTPServer(addr string, staticDir string) *HTTPServer {
	wsServer := NewWebSocketServer()

	mux := http.NewServeMux()

	// Serve static files from the static directory
	fs := http.FileServer(http.Dir(staticDir))
	mux.Handle("/", fs)

	// WebSocket endpoint for desktop buffer streaming
	mux.HandleFunc("/ws", wsServer.HandleWebSocket)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return &HTTPServer{
		wsServer: wsServer,
		server:   server,
	}
}

// Start starts the HTTP server in a goroutine
func (h *HTTPServer) Start() error {
	log.Printf("Starting HTTP server on %s", h.server.Addr)
	log.Printf("Static files served from: ./static")
	log.Printf("WebSocket endpoint: ws://%s/ws", h.server.Addr)

	go func() {
		if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully stops the HTTP server
func (h *HTTPServer) Stop() error {
	return h.server.Close()
}

// BroadcastDesktopBuffer forwards the desktop buffer to all WebSocket clients
func (h *HTTPServer) BroadcastDesktopBuffer(buffer []byte, width, height, stride int) {
	h.wsServer.BroadcastDesktopBuffer(buffer, width, height, stride)
}

// WebSocketClientCount returns the number of connected WebSocket clients
func (h *HTTPServer) WebSocketClientCount() int {
	return h.wsServer.ClientCount()
}
