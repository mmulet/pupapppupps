package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mmulet/term.everything/wayland"
	"github.com/mmulet/term.everything/wayland/protocols"
)

// Args implements the HasDisplayName interface required by MakeSocketListener.
type Args struct {
	DisplayName string
}

func (a *Args) WaylandDisplayName() string {
	return a.DisplayName // empty string auto-generates a name
}

func main() {
	// Initialize arguments. Passing an empty string will let the library
	// automatically choose a display name (e.g., wayland-0, wayland-1).
	args := &Args{DisplayName: ""}

	// Create the socket listener.
	listener, err := wayland.MakeSocketListener(args)
	if err != nil {
		log.Fatalf("Failed to create socket listener: %v", err)
	}

	fmt.Printf("Wayland Compositor started.\n")
	fmt.Printf("Display: %s\n", listener.WaylandDisplayName)
	fmt.Printf("Socket Path: %s\n", listener.SocketPath)
	fmt.Printf("Set WAYLAND_DISPLAY=%s to connect clients.\n", listener.WaylandDisplayName)

	// Start the listener loop in a background goroutine.
	go func() {
		if err := listener.MainLoopThenClose(); err != nil {
			log.Printf("Listener loop error: %v", err)
		}
	}()

	// Track connected clients.
	var clients []*wayland.Client
	var mu sync.Mutex

	// Handle frame callbacks to know when clients want to redraw.
	handleFrameRequests := func(client *wayland.Client) {
		for callbackID := range client.FrameDrawRequests {
			// Acknowledge the frame callback with the current time in milliseconds.
			protocols.WlCallback_done(client, callbackID, uint32(time.Now().UnixMilli()))
			if client.Status != wayland.ClientStatus_Connected {
				break
			}
		}
	}

	// Accept new client connections.
	go func() {
		for conn := range listener.OnConnection {
			log.Printf("New client connection accepted.")
			client := wayland.MakeClient(conn)

			mu.Lock()
			clients = append(clients, client)
			mu.Unlock()

			// Start the client's main loop to process messages.
			go client.MainLoop()

			// Handle frame requests for this client.
			go handleFrameRequests(client)
		}
	}()

	// Create a desktop for compositing.
	// We use a fixed size of 800x600 for this example.
	desktop := wayland.MakeDesktop(
		wayland.Size{Width: 800, Height: 600},
		false,        // willShowAppRightAtStartup / useLinuxDMABuf
		createIcon(), // icon data
	)

	// Setup signal handling for graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Render loop ticker (approx 60 FPS).
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()

	log.Println("Starting render loop. Press Ctrl+C to exit.")

	frameCount := 0
	lastLog := time.Now()

	for {
		select {
		case <-sigChan:
			log.Println("Shutting down...")
			// Close the listener to stop accepting new connections.
			listener.Close()
			return
		case <-ticker.C:
			mu.Lock()

			// Filter out disconnected clients
			activeClients := clients[:0]
			for _, c := range clients {
				if c.Status == wayland.ClientStatus_Connected {
					activeClients = append(activeClients, c)
				}
			}
			clients = activeClients

			// Render the clients to the desktop buffer.
			desktop.DrawClients(clients)
			mu.Unlock()

			// Here you would typically display desktop.Buffer (RGBA data)
			// to the screen or save it.
			// desktop.Buffer contains the raw pixel data.
			// desktop.Stride is the row stride.

			frameCount++
			if time.Since(lastLog) >= 5*time.Second {
				log.Printf("Rendered %d frames. Active clients: %d", frameCount, len(clients))
				frameCount = 0
				lastLog = time.Now()
			}
		}
	}
}

func createIcon() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	// Fill with blue
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatalf("Failed to create icon: %v", err)
	}
	return buf.Bytes()
}
