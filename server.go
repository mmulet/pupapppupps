package main

import (
	"encoding/binary"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// KeyboardEventHandler is a callback for handling keyboard events from WebSocket clients
type KeyboardEventHandler func(keycode uint32, pressed bool)

// MouseEventType represents the type of mouse event
type MouseEventType uint8

const (
	MouseEventMotion MouseEventType = 0
	MouseEventButton MouseEventType = 1
	MouseEventScroll MouseEventType = 2
)

// MouseEventHandler is a callback for handling mouse events from WebSocket clients
type MouseEventHandler func(eventType MouseEventType, x, y float32, button uint32, pressed bool, scrollDelta float32)

// WebSocketServer manages WebSocket connections for streaming the desktop buffer
type WebSocketServer struct {
	clients         map[*websocket.Conn]bool
	mu              sync.RWMutex
	upgrader        websocket.Upgrader
	broadcast       chan []byte
	keyboardHandler KeyboardEventHandler
	mouseHandler    MouseEventHandler
}

// NewWebSocketServer creates a new WebSocket server instance
func NewWebSocketServer() *WebSocketServer {
	return &WebSocketServer{
		clients:         make(map[*websocket.Conn]bool),
		broadcast:       make(chan []byte, 10),
		keyboardHandler: nil,
		mouseHandler:    nil,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024 * 1024, // Large buffer for image data
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
	}
}

// SetKeyboardHandler sets the callback for keyboard events
func (s *WebSocketServer) SetKeyboardHandler(handler KeyboardEventHandler) {
	s.keyboardHandler = handler
}

// SetMouseHandler sets the callback for mouse events
func (s *WebSocketServer) SetMouseHandler(handler MouseEventHandler) {
	s.mouseHandler = handler
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

	// Keep connection alive and handle disconnects and incoming messages
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.clients, conn)
			s.mu.Unlock()
			conn.Close()
			log.Printf("WebSocket client disconnected. Total clients: %d", len(s.clients))
		}()

		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				break
			}

			// Handle input messages
			// Format for keyboard: [type:1byte][keycode:4bytes][pressed:1byte]
			// Format for mouse: [type:1byte][eventType:1byte][x:4bytes float][y:4bytes float][button:4bytes][pressed:1byte][scrollDelta:4bytes float]
			// type: 1 = keyboard, 2 = mouse
			if messageType == websocket.BinaryMessage && len(message) >= 6 {
				msgType := message[0]
				if msgType == 1 && s.keyboardHandler != nil { // Keyboard message
					keycode := binary.LittleEndian.Uint32(message[1:5])
					pressed := message[5] != 0
					s.keyboardHandler(keycode, pressed)
				} else if msgType == 2 && s.mouseHandler != nil && len(message) >= 19 { // Mouse message
					eventType := MouseEventType(message[1])
					x := math.Float32frombits(binary.LittleEndian.Uint32(message[2:6]))
					y := math.Float32frombits(binary.LittleEndian.Uint32(message[6:10]))
					button := binary.LittleEndian.Uint32(message[10:14])
					pressed := message[14] != 0
					scrollDelta := math.Float32frombits(binary.LittleEndian.Uint32(message[15:19]))
					s.mouseHandler(eventType, x, y, button, pressed, scrollDelta)
				}
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

// SetKeyboardHandler sets the callback for keyboard events received from WebSocket clients
func (h *HTTPServer) SetKeyboardHandler(handler KeyboardEventHandler) {
	h.wsServer.SetKeyboardHandler(handler)
}

// SetMouseHandler sets the callback for mouse events received from WebSocket clients
func (h *HTTPServer) SetMouseHandler(handler MouseEventHandler) {
	h.wsServer.SetMouseHandler(handler)
}
