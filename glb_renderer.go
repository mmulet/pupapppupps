package main

import (
	"fmt"
	"unsafe"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/modeler"
)

// Mesh represents a loaded mesh with OpenGL buffers
type Mesh struct {
	VAO         uint32
	VBO         uint32
	EBO         uint32
	IndexCount  int32
	HasIndices  bool
	VertexCount int32
}

// GLBRenderer handles loading and rendering GLB models with dynamic textures
type GLBRenderer struct {
	Meshes        []Mesh
	ShaderProgram uint32
	TextureID     uint32
	TextureWidth  int32
	TextureHeight int32

	// Uniform locations
	modelLoc      int32
	viewLoc       int32
	projectionLoc int32
	textureLoc    int32

	// Transform
	Rotation float32
}

const vertexShaderSource = `
#version 410 core
layout (location = 0) in vec3 aPos;
layout (location = 1) in vec3 aNormal;
layout (location = 2) in vec2 aTexCoord;

out vec2 TexCoord;
out vec3 Normal;
out vec3 FragPos;

uniform mat4 model;
uniform mat4 view;
uniform mat4 projection;

void main() {
    FragPos = vec3(model * vec4(aPos, 1.0));
    Normal = mat3(transpose(inverse(model))) * aNormal;
    TexCoord = aTexCoord;
    gl_Position = projection * view * model * vec4(aPos, 1.0);
}
` + "\x00"

const fragmentShaderSource = `
#version 410 core
out vec4 FragColor;

in vec2 TexCoord;
in vec3 Normal;
in vec3 FragPos;

uniform sampler2D desktopTexture;

void main() {
    // Simple lighting
    vec3 lightDir = normalize(vec3(1.0, 1.0, 1.0));
    vec3 norm = normalize(Normal);
    float diff = max(dot(norm, lightDir), 0.0);
    float ambient = 0.3;
    float lighting = ambient + diff * 0.7;
    
    vec4 texColor = texture(desktopTexture, TexCoord);
    FragColor = vec4(texColor.rgb * lighting, texColor.a);
}
` + "\x00"

// NewGLBRenderer creates a new GLB renderer
func NewGLBRenderer() (*GLBRenderer, error) {
	r := &GLBRenderer{}

	// Compile shaders
	vertexShader, err := compileShader(vertexShaderSource, gl.VERTEX_SHADER)
	if err != nil {
		return nil, fmt.Errorf("vertex shader: %w", err)
	}

	fragmentShader, err := compileShader(fragmentShaderSource, gl.FRAGMENT_SHADER)
	if err != nil {
		return nil, fmt.Errorf("fragment shader: %w", err)
	}

	// Create shader program
	r.ShaderProgram = gl.CreateProgram()
	gl.AttachShader(r.ShaderProgram, vertexShader)
	gl.AttachShader(r.ShaderProgram, fragmentShader)
	gl.LinkProgram(r.ShaderProgram)

	var status int32
	gl.GetProgramiv(r.ShaderProgram, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetProgramiv(r.ShaderProgram, gl.INFO_LOG_LENGTH, &logLength)
		log := make([]byte, logLength)
		gl.GetProgramInfoLog(r.ShaderProgram, logLength, nil, &log[0])
		return nil, fmt.Errorf("program link: %s", string(log))
	}

	gl.DeleteShader(vertexShader)
	gl.DeleteShader(fragmentShader)

	// Get uniform locations
	r.modelLoc = gl.GetUniformLocation(r.ShaderProgram, gl.Str("model\x00"))
	r.viewLoc = gl.GetUniformLocation(r.ShaderProgram, gl.Str("view\x00"))
	r.projectionLoc = gl.GetUniformLocation(r.ShaderProgram, gl.Str("projection\x00"))
	r.textureLoc = gl.GetUniformLocation(r.ShaderProgram, gl.Str("desktopTexture\x00"))

	// Create texture for desktop buffer
	gl.GenTextures(1, &r.TextureID)
	gl.BindTexture(gl.TEXTURE_2D, r.TextureID)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)

	return r, nil
}

// LoadGLB loads a GLB file and creates OpenGL buffers
func (r *GLBRenderer) LoadGLB(filename string) error {
	doc, err := gltf.Open(filename)
	if err != nil {
		return fmt.Errorf("open glb: %w", err)
	}

	// Process each mesh in the document
	for _, mesh := range doc.Meshes {
		for _, prim := range mesh.Primitives {
			m, err := r.loadPrimitive(doc, prim)
			if err != nil {
				return fmt.Errorf("load primitive: %w", err)
			}
			r.Meshes = append(r.Meshes, m)
		}
	}

	if len(r.Meshes) == 0 {
		return fmt.Errorf("no meshes found in GLB file")
	}

	return nil
}

func (r *GLBRenderer) loadPrimitive(doc *gltf.Document, prim *gltf.Primitive) (Mesh, error) {
	var m Mesh

	// Get position data
	posAccessorIdx, ok := prim.Attributes[gltf.POSITION]
	if !ok {
		return m, fmt.Errorf("no POSITION attribute")
	}
	positions, err := modeler.ReadPosition(doc, doc.Accessors[posAccessorIdx], nil)
	if err != nil {
		return m, fmt.Errorf("read positions: %w", err)
	}

	// Get normal data (optional)
	var normals [][3]float32
	if normalIdx, ok := prim.Attributes[gltf.NORMAL]; ok {
		normals, err = modeler.ReadNormal(doc, doc.Accessors[normalIdx], nil)
		if err != nil {
			normals = nil
		}
	}

	// Get texture coordinates (optional)
	var texCoords [][2]float32
	if texIdx, ok := prim.Attributes[gltf.TEXCOORD_0]; ok {
		texCoords, err = modeler.ReadTextureCoord(doc, doc.Accessors[texIdx], nil)
		if err != nil {
			texCoords = nil
		}
	}

	// Build interleaved vertex data: position (3) + normal (3) + texcoord (2) = 8 floats per vertex
	vertexData := make([]float32, 0, len(positions)*8)
	for i, pos := range positions {
		// Position
		vertexData = append(vertexData, pos[0], pos[1], pos[2])

		// Normal
		if normals != nil && i < len(normals) {
			vertexData = append(vertexData, normals[i][0], normals[i][1], normals[i][2])
		} else {
			vertexData = append(vertexData, 0, 1, 0)
		}

		// Texture coordinates
		if texCoords != nil && i < len(texCoords) {
			vertexData = append(vertexData, texCoords[i][0], texCoords[i][1])
		} else {
			// Generate UV based on position if not available
			vertexData = append(vertexData, (pos[0]+1)/2, (pos[1]+1)/2)
		}
	}

	// Create VAO
	gl.GenVertexArrays(1, &m.VAO)
	gl.BindVertexArray(m.VAO)

	// Create VBO
	gl.GenBuffers(1, &m.VBO)
	gl.BindBuffer(gl.ARRAY_BUFFER, m.VBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(vertexData)*4, gl.Ptr(vertexData), gl.STATIC_DRAW)

	stride := int32(8 * 4) // 8 floats * 4 bytes

	// Position attribute
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(0)

	// Normal attribute
	gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(1)

	// Texture coordinate attribute
	gl.VertexAttribPointerWithOffset(2, 2, gl.FLOAT, false, stride, 6*4)
	gl.EnableVertexAttribArray(2)

	// Handle indices if present
	if prim.Indices != nil {
		indices, err := modeler.ReadIndices(doc, doc.Accessors[*prim.Indices], nil)
		if err == nil && len(indices) > 0 {
			gl.GenBuffers(1, &m.EBO)
			gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, m.EBO)
			gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(indices)*4, gl.Ptr(indices), gl.STATIC_DRAW)
			m.HasIndices = true
			m.IndexCount = int32(len(indices))
		}
	}

	if !m.HasIndices {
		m.VertexCount = int32(len(positions))
	}

	gl.BindVertexArray(0)
	return m, nil
}

// UpdateTexture updates the desktop texture with new buffer data
func (r *GLBRenderer) UpdateTexture(buffer []byte, width, height, stride int32) {
	if len(buffer) == 0 {
		return
	}

	gl.BindTexture(gl.TEXTURE_2D, r.TextureID)

	// Check if texture needs to be resized
	if r.TextureWidth != width || r.TextureHeight != height {
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, width, height, 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
		r.TextureWidth = width
		r.TextureHeight = height
	}

	// Update texture data
	gl.TexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, width, height, gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&buffer[0]))
}

// Render draws the loaded model with the current texture
func (r *GLBRenderer) Render(windowWidth, windowHeight int32) {
	gl.UseProgram(r.ShaderProgram)

	// Set up matrices
	aspect := float32(windowWidth) / float32(windowHeight)
	projection := mgl32.Perspective(mgl32.DegToRad(45.0), aspect, 0.1, 100.0)
	view := mgl32.LookAtV(mgl32.Vec3{0, 0, 3}, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})
	model := mgl32.HomogRotate3DY(r.Rotation)

	gl.UniformMatrix4fv(r.projectionLoc, 1, false, &projection[0])
	gl.UniformMatrix4fv(r.viewLoc, 1, false, &view[0])
	gl.UniformMatrix4fv(r.modelLoc, 1, false, &model[0])

	// Bind texture
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, r.TextureID)
	gl.Uniform1i(r.textureLoc, 0)

	// Draw all meshes
	for _, mesh := range r.Meshes {
		gl.BindVertexArray(mesh.VAO)
		if mesh.HasIndices {
			gl.DrawElements(gl.TRIANGLES, mesh.IndexCount, gl.UNSIGNED_INT, nil)
		} else {
			gl.DrawArrays(gl.TRIANGLES, 0, mesh.VertexCount)
		}
	}

	gl.BindVertexArray(0)
}

// Destroy cleans up OpenGL resources
func (r *GLBRenderer) Destroy() {
	for _, mesh := range r.Meshes {
		gl.DeleteVertexArrays(1, &mesh.VAO)
		gl.DeleteBuffers(1, &mesh.VBO)
		if mesh.HasIndices {
			gl.DeleteBuffers(1, &mesh.EBO)
		}
	}
	gl.DeleteTextures(1, &r.TextureID)
	gl.DeleteProgram(r.ShaderProgram)
}

func compileShader(source string, shaderType uint32) (uint32, error) {
	shader := gl.CreateShader(shaderType)
	csources, free := gl.Strs(source)
	gl.ShaderSource(shader, 1, csources, nil)
	free()
	gl.CompileShader(shader)

	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &logLength)
		log := make([]byte, logLength)
		gl.GetShaderInfoLog(shader, logLength, nil, &log[0])
		return 0, fmt.Errorf("compile: %s", string(log))
	}

	return shader, nil
}
