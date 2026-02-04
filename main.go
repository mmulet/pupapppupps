package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/mmulet/term.everything/wayland"
	"github.com/mmulet/term.everything/wayland/protocols"
	"github.com/veandco/go-sdl2/sdl"
)

func init() {
	// This is needed to arrange that main() runs on the main thread.
	// OpenGL and SDL2 require this.
	runtime.LockOSThread()
}

// Args implements the HasDisplayName interface required by MakeSocketListener.
type Args struct {
	DisplayName string
}

func (a *Args) WaylandDisplayName() string {
	return a.DisplayName // empty string auto-generates a name
}

func main() {
	// Parse command line flags
	httpAddr := flag.String("http", ":8080", "HTTP server address")
	staticDir := flag.String("static", "./static", "Static files directory")
	glbFile := flag.String("model", "", "Path to .glb model file to display")
	flag.Parse()

	if *glbFile == "" {
		log.Fatal("Please specify a .glb model file with -model flag")
	}

	// Start HTTP server with WebSocket support
	httpServer := NewHTTPServer(*httpAddr, *staticDir)
	if err := httpServer.Start(); err != nil {
		log.Fatalf("Failed to start HTTP server: %v", err)
	}
	defer httpServer.Stop()

	// Initialize SDL2 with OpenGL
	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_EVENTS); err != nil {
		log.Fatalf("Failed to initialize SDL2: %v", err)
	}
	defer sdl.Quit()

	// Set OpenGL attributes
	sdl.GLSetAttribute(sdl.GL_CONTEXT_MAJOR_VERSION, 4)
	sdl.GLSetAttribute(sdl.GL_CONTEXT_MINOR_VERSION, 1)
	sdl.GLSetAttribute(sdl.GL_CONTEXT_PROFILE_MASK, sdl.GL_CONTEXT_PROFILE_CORE)
	sdl.GLSetAttribute(sdl.GL_DOUBLEBUFFER, 1)
	sdl.GLSetAttribute(sdl.GL_DEPTH_SIZE, 24)

	window, err := sdl.CreateWindow("Wayland Compositor - 3D View",
		sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		800, 600,
		sdl.WINDOW_SHOWN|sdl.WINDOW_OPENGL|sdl.WINDOW_RESIZABLE)
	if err != nil {
		log.Fatalf("Failed to create SDL2 window: %v", err)
	}
	defer window.Destroy()

	// Create OpenGL context
	glContext, err := window.GLCreateContext()
	if err != nil {
		log.Fatalf("Failed to create OpenGL context: %v", err)
	}
	defer sdl.GLDeleteContext(glContext)

	// Initialize OpenGL
	if err := gl.Init(); err != nil {
		log.Fatalf("Failed to initialize OpenGL: %v", err)
	}

	log.Printf("OpenGL Version: %s", gl.GoStr(gl.GetString(gl.VERSION)))
	log.Printf("GLSL Version: %s", gl.GoStr(gl.GetString(gl.SHADING_LANGUAGE_VERSION)))

	// Enable depth testing and other OpenGL settings
	gl.Enable(gl.DEPTH_TEST)
	gl.Enable(gl.CULL_FACE)
	gl.CullFace(gl.BACK)
	gl.ClearColor(0.1, 0.1, 0.1, 1.0)

	// Create GLB renderer
	glbRenderer, err := NewGLBRenderer()
	if err != nil {
		log.Fatalf("Failed to create GLB renderer: %v", err)
	}
	defer glbRenderer.Destroy()

	// Load the GLB model
	if err := glbRenderer.LoadGLB(*glbFile); err != nil {
		log.Fatalf("Failed to load GLB model: %v", err)
	}
	log.Printf("Loaded GLB model: %s (%d meshes)", *glbFile, len(glbRenderer.Meshes))

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

	// Launch Chrome with the Wayland display
	go func() {
		cmd := exec.Command("google-chrome")
		cmd.Env = append(os.Environ(), "WAYLAND_DISPLAY="+listener.WaylandDisplayName)
		if err := cmd.Start(); err != nil {
			log.Printf("Failed to launch Chrome: %v", err)
		}
	}()

	// Render loop ticker (approx 60 FPS).
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()

	log.Println("Starting render loop. Press Ctrl+C to exit.")

	frameCount := 0
	lastLog := time.Now()

	running := true
	for running {
		// SDL2 event loop - forward input to Wayland clients
		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			mu.Lock()
			activeClients := clients
			mu.Unlock()

			switch e := event.(type) {
			case *sdl.QuitEvent:
				log.Println("SDL2 quit event received...")
				running = false

			case *sdl.MouseMotionEvent:
				wayland.SendPointerMotion(activeClients, float32(e.X), float32(e.Y))

			case *sdl.MouseButtonEvent:
				// Map SDL button to Linux button codes
				var button uint32
				switch e.Button {
				case sdl.BUTTON_LEFT:
					button = 0x110 // BTN_LEFT
				case sdl.BUTTON_RIGHT:
					button = 0x111 // BTN_RIGHT
				case sdl.BUTTON_MIDDLE:
					button = 0x112 // BTN_MIDDLE
				default:
					button = 0x110
				}
				pressed := e.Type == sdl.MOUSEBUTTONDOWN
				wayland.SendPointerButton(activeClients, button, pressed)

			case *sdl.MouseWheelEvent:
				// Scroll amount (positive = up, negative = down)
				value := float32(e.Y) * -15.0 // Invert and scale
				wayland.SendPointerAxis(activeClients, protocols.WlPointerAxis_enum_vertical_scroll, value)

			case *sdl.KeyboardEvent:
				// Convert SDL scancode to Linux evdev keycode
				keycode := sdlScancodeToLinux(e.Keysym.Scancode)
				if keycode != 0 {
					pressed := e.Type == sdl.KEYDOWN
					wayland.SendKeyboardKey(activeClients, keycode, pressed)
				}
			}
		}

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

			// Broadcast desktop buffer to WebSocket clients
			if len(desktop.Buffer) > 0 {
				httpServer.BroadcastDesktopBuffer(
					desktop.Buffer,
					800, // Desktop width
					600, // Desktop height
					desktop.Stride,
				)
			}

			// Update texture with desktop buffer
			if len(desktop.Buffer) > 0 {
				glbRenderer.UpdateTexture(desktop.Buffer, 800, 600, int32(desktop.Stride))
			}

			// Rotate the model slowly
			glbRenderer.Rotation += 0.01

			// Get current window size for proper viewport
			winW, winH := window.GetSize()
			gl.Viewport(0, 0, winW, winH)

			// Clear and render
			gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
			glbRenderer.Render(winW, winH)
			window.GLSwap()

			frameCount++
			if time.Since(lastLog) >= 5*time.Second {
				log.Printf("Rendered %d frames. Wayland clients: %d, WebSocket clients: %d",
					frameCount, len(clients), httpServer.WebSocketClientCount())
				frameCount = 0
				lastLog = time.Now()
			}
		default:
			// Non-blocking: continue loop
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

// sdlScancodeToLinux converts SDL2 scancodes to Linux evdev keycodes
func sdlScancodeToLinux(scancode sdl.Scancode) uint32 {
	// SDL scancodes are based on USB HID usage codes
	// Linux evdev keycodes are different, this maps common keys
	switch scancode {
	case sdl.SCANCODE_ESCAPE:
		return 1
	case sdl.SCANCODE_1:
		return 2
	case sdl.SCANCODE_2:
		return 3
	case sdl.SCANCODE_3:
		return 4
	case sdl.SCANCODE_4:
		return 5
	case sdl.SCANCODE_5:
		return 6
	case sdl.SCANCODE_6:
		return 7
	case sdl.SCANCODE_7:
		return 8
	case sdl.SCANCODE_8:
		return 9
	case sdl.SCANCODE_9:
		return 10
	case sdl.SCANCODE_0:
		return 11
	case sdl.SCANCODE_MINUS:
		return 12
	case sdl.SCANCODE_EQUALS:
		return 13
	case sdl.SCANCODE_BACKSPACE:
		return 14
	case sdl.SCANCODE_TAB:
		return 15
	case sdl.SCANCODE_Q:
		return 16
	case sdl.SCANCODE_W:
		return 17
	case sdl.SCANCODE_E:
		return 18
	case sdl.SCANCODE_R:
		return 19
	case sdl.SCANCODE_T:
		return 20
	case sdl.SCANCODE_Y:
		return 21
	case sdl.SCANCODE_U:
		return 22
	case sdl.SCANCODE_I:
		return 23
	case sdl.SCANCODE_O:
		return 24
	case sdl.SCANCODE_P:
		return 25
	case sdl.SCANCODE_LEFTBRACKET:
		return 26
	case sdl.SCANCODE_RIGHTBRACKET:
		return 27
	case sdl.SCANCODE_RETURN:
		return 28
	case sdl.SCANCODE_LCTRL:
		return 29
	case sdl.SCANCODE_A:
		return 30
	case sdl.SCANCODE_S:
		return 31
	case sdl.SCANCODE_D:
		return 32
	case sdl.SCANCODE_F:
		return 33
	case sdl.SCANCODE_G:
		return 34
	case sdl.SCANCODE_H:
		return 35
	case sdl.SCANCODE_J:
		return 36
	case sdl.SCANCODE_K:
		return 37
	case sdl.SCANCODE_L:
		return 38
	case sdl.SCANCODE_SEMICOLON:
		return 39
	case sdl.SCANCODE_APOSTROPHE:
		return 40
	case sdl.SCANCODE_GRAVE:
		return 41
	case sdl.SCANCODE_LSHIFT:
		return 42
	case sdl.SCANCODE_BACKSLASH:
		return 43
	case sdl.SCANCODE_Z:
		return 44
	case sdl.SCANCODE_X:
		return 45
	case sdl.SCANCODE_C:
		return 46
	case sdl.SCANCODE_V:
		return 47
	case sdl.SCANCODE_B:
		return 48
	case sdl.SCANCODE_N:
		return 49
	case sdl.SCANCODE_M:
		return 50
	case sdl.SCANCODE_COMMA:
		return 51
	case sdl.SCANCODE_PERIOD:
		return 52
	case sdl.SCANCODE_SLASH:
		return 53
	case sdl.SCANCODE_RSHIFT:
		return 54
	case sdl.SCANCODE_LALT:
		return 56
	case sdl.SCANCODE_SPACE:
		return 57
	case sdl.SCANCODE_CAPSLOCK:
		return 58
	case sdl.SCANCODE_F1:
		return 59
	case sdl.SCANCODE_F2:
		return 60
	case sdl.SCANCODE_F3:
		return 61
	case sdl.SCANCODE_F4:
		return 62
	case sdl.SCANCODE_F5:
		return 63
	case sdl.SCANCODE_F6:
		return 64
	case sdl.SCANCODE_F7:
		return 65
	case sdl.SCANCODE_F8:
		return 66
	case sdl.SCANCODE_F9:
		return 67
	case sdl.SCANCODE_F10:
		return 68
	case sdl.SCANCODE_F11:
		return 87
	case sdl.SCANCODE_F12:
		return 88
	case sdl.SCANCODE_RCTRL:
		return 97
	case sdl.SCANCODE_RALT:
		return 100
	case sdl.SCANCODE_HOME:
		return 102
	case sdl.SCANCODE_UP:
		return 103
	case sdl.SCANCODE_PAGEUP:
		return 104
	case sdl.SCANCODE_LEFT:
		return 105
	case sdl.SCANCODE_RIGHT:
		return 106
	case sdl.SCANCODE_END:
		return 107
	case sdl.SCANCODE_DOWN:
		return 108
	case sdl.SCANCODE_PAGEDOWN:
		return 109
	case sdl.SCANCODE_INSERT:
		return 110
	case sdl.SCANCODE_DELETE:
		return 111
	default:
		return 0
	}
}
