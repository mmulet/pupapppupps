# Wayland Compositor with 3D Model Texture

A Wayland compositor that renders client applications onto a 3D model loaded from a GLB file.

## Features

- Wayland compositor that captures client application output
- 3D model loading from GLB/glTF files
- Desktop buffer applied as a texture to the loaded 3D model
- SDL2 + OpenGL 4.1 rendering
- WebSocket streaming for remote viewing
- Input forwarding (mouse, keyboard) to Wayland clients

## Requirements

- Go 1.21+
- SDL2 development libraries
- OpenGL 4.1+ capable GPU
- Linux with Wayland support

## Installation

```bash
# Install SDL2 development libraries (Ubuntu/Debian)
sudo apt-get install libsdl2-dev

# Build
go build -o wayland-compositor
```

## Usage

```bash
# Run with a GLB model file
./wayland-compositor -model path/to/model.glb

# With custom HTTP port
./wayland-compositor -model model.glb -http :9090

# With custom static files directory
./wayland-compositor -model model.glb -static ./public
```

### Command Line Options

- `-model` - Path to a .glb model file (required)
- `-http` - HTTP server address (default: `:8080`)
- `-static` - Static files directory (default: `./static`)

## How it Works

1. Creates a Wayland socket for client applications to connect
2. Launches Chrome (or other Wayland clients) with the compositor
3. Captures the desktop buffer from connected clients
4. Loads a 3D model from the specified GLB file
5. Applies the desktop buffer as a texture to the model
6. Renders the textured model with simple lighting and rotation

## Getting GLB Files

You can download free GLB models from:
- [Sketchfab](https://sketchfab.com) (filter by downloadable)
- [Khronos glTF Sample Models](https://github.com/KhronosGroup/glTF-Sample-Models)
- [Google Poly](https://poly.pizza/)

Example: Download a simple cube or box model with UV coordinates for best results.
