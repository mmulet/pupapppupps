package main

import (
	"fmt"
	"log"
	"math"
	"sort"
	"time"
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
	NodeIndex   int // Index of the node this mesh belongs to
	SkinIndex   int // Index of the skin for this mesh (-1 if not skinned)
}

// Skin represents a glTF skin with joint matrices
type Skin struct {
	Joints              []int // Node indices of the joints
	InverseBindMatrices []mgl32.Mat4
}

// AnimationChannel represents a single animation channel (target + sampler)
type AnimationChannel struct {
	NodeIndex  int
	Path       string // "translation", "rotation", "scale"
	Timestamps []float32
	Values     []float32 // Flat array of values
}

// Animation represents a glTF animation
type Animation struct {
	Name     string
	Channels []AnimationChannel
	Duration float32
}

// NodeTransform holds the current transform for a node
type NodeTransform struct {
	Translation mgl32.Vec3
	Rotation    mgl32.Quat
	Scale       mgl32.Vec3
}

// GLBRenderer handles loading and rendering GLB models with dynamic textures
type GLBRenderer struct {
	Meshes        []Mesh
	ShaderProgram uint32
	TextureID     uint32
	TextureWidth  int32
	TextureHeight int32

	// Uniform locations
	modelLoc        int32
	viewLoc         int32
	projectionLoc   int32
	textureLoc      int32
	boneMatricesLoc int32

	// Transform
	Rotation float32

	// Animation support
	Animations     map[string]*Animation
	NodeTransforms []NodeTransform
	BaseTransforms []NodeTransform // Original transforms from the file
	CurrentAnim    *Animation
	AnimStartTime  time.Time
	AnimLoop       bool
	Document       *gltf.Document // Keep reference to the document

	// Skinning support
	Skins        []Skin
	NodeParents  []int        // Parent index for each node (-1 for root)
	BoneMatrices []mgl32.Mat4 // Computed bone matrices for current frame
}

const vertexShaderSource = `
#version 410 core
layout (location = 0) in vec3 aPos;
layout (location = 1) in vec3 aNormal;
layout (location = 2) in vec2 aTexCoord;
layout (location = 3) in vec4 aJoints;
layout (location = 4) in vec4 aWeights;

out vec2 TexCoord;
out vec3 Normal;
out vec3 FragPos;

uniform mat4 model;
uniform mat4 view;
uniform mat4 projection;
uniform mat4 boneMatrices[128];

void main() {
    // Compute skinned position and normal
    mat4 skinMatrix = mat4(0.0);
    float totalWeight = aWeights.x + aWeights.y + aWeights.z + aWeights.w;
    
    if (totalWeight > 0.0) {
        skinMatrix += boneMatrices[int(aJoints.x)] * aWeights.x;
        skinMatrix += boneMatrices[int(aJoints.y)] * aWeights.y;
        skinMatrix += boneMatrices[int(aJoints.z)] * aWeights.z;
        skinMatrix += boneMatrices[int(aJoints.w)] * aWeights.w;
    } else {
        skinMatrix = mat4(1.0);
    }
    
    vec4 skinnedPos = skinMatrix * vec4(aPos, 1.0);
    vec3 skinnedNormal = mat3(skinMatrix) * aNormal;
    
    FragPos = vec3(model * skinnedPos);
    Normal = mat3(transpose(inverse(model))) * skinnedNormal;
    TexCoord = aTexCoord;
    gl_Position = projection * view * model * skinnedPos;
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
	r := &GLBRenderer{
		Animations: make(map[string]*Animation),
	}

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
	r.boneMatricesLoc = gl.GetUniformLocation(r.ShaderProgram, gl.Str("boneMatrices\x00"))

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

	r.Document = doc

	// Build node parent hierarchy
	r.NodeParents = make([]int, len(doc.Nodes))
	for i := range r.NodeParents {
		r.NodeParents[i] = -1 // Default to no parent
	}
	for parentIdx, node := range doc.Nodes {
		for _, childIdx := range node.Children {
			r.NodeParents[childIdx] = parentIdx
		}
	}

	// Initialize node transforms
	r.NodeTransforms = make([]NodeTransform, len(doc.Nodes))
	r.BaseTransforms = make([]NodeTransform, len(doc.Nodes))
	for i, node := range doc.Nodes {
		// Set default values
		r.NodeTransforms[i] = NodeTransform{
			Translation: mgl32.Vec3{0, 0, 0},
			Rotation:    mgl32.QuatIdent(),
			Scale:       mgl32.Vec3{1, 1, 1},
		}
		// Load from node if present
		if node.Translation != [3]float64{0, 0, 0} {
			r.NodeTransforms[i].Translation = mgl32.Vec3{
				float32(node.Translation[0]),
				float32(node.Translation[1]),
				float32(node.Translation[2]),
			}
		}
		if node.Rotation != [4]float64{0, 0, 0, 1} {
			r.NodeTransforms[i].Rotation = mgl32.Quat{
				W: float32(node.Rotation[3]),
				V: mgl32.Vec3{
					float32(node.Rotation[0]),
					float32(node.Rotation[1]),
					float32(node.Rotation[2]),
				},
			}
		}
		if node.Scale != [3]float64{1, 1, 1} && node.Scale != [3]float64{0, 0, 0} {
			r.NodeTransforms[i].Scale = mgl32.Vec3{
				float32(node.Scale[0]),
				float32(node.Scale[1]),
				float32(node.Scale[2]),
			}
		}
		r.BaseTransforms[i] = r.NodeTransforms[i]
	}

	// Load skins
	for _, skin := range doc.Skins {
		s := Skin{
			Joints: make([]int, len(skin.Joints)),
		}
		for i, jointIdx := range skin.Joints {
			s.Joints[i] = int(jointIdx)
		}

		// Load inverse bind matrices
		if skin.InverseBindMatrices != nil {
			matrices, err := r.readAccessorFloats(doc, int(*skin.InverseBindMatrices))
			if err == nil {
				s.InverseBindMatrices = make([]mgl32.Mat4, len(s.Joints))
				for i := 0; i < len(s.Joints) && i*16+16 <= len(matrices); i++ {
					// glTF stores matrices in column-major order, same as mgl32.Mat4
					for j := 0; j < 16; j++ {
						s.InverseBindMatrices[i][j] = matrices[i*16+j]
					}
				}
			}
		} else {
			// Default to identity matrices
			s.InverseBindMatrices = make([]mgl32.Mat4, len(s.Joints))
			for i := range s.InverseBindMatrices {
				s.InverseBindMatrices[i] = mgl32.Ident4()
			}
		}

		r.Skins = append(r.Skins, s)
	}

	// Initialize bone matrices
	if len(r.Skins) > 0 {
		maxJoints := 0
		for _, skin := range r.Skins {
			if len(skin.Joints) > maxJoints {
				maxJoints = len(skin.Joints)
			}
		}
		r.BoneMatrices = make([]mgl32.Mat4, maxJoints)
		for i := range r.BoneMatrices {
			r.BoneMatrices[i] = mgl32.Ident4()
		}
	}

	// Process each node to find meshes
	for nodeIdx, node := range doc.Nodes {
		if node.Mesh != nil {
			mesh := doc.Meshes[*node.Mesh]
			for _, prim := range mesh.Primitives {
				m, err := r.loadPrimitive(doc, prim)
				if err != nil {
					return fmt.Errorf("load primitive: %w", err)
				}
				m.NodeIndex = nodeIdx
				// Check if this node has a skin
				if node.Skin != nil {
					m.SkinIndex = int(*node.Skin)
				} else {
					m.SkinIndex = -1
				}
				r.Meshes = append(r.Meshes, m)
			}
		}
	}

	if len(r.Meshes) == 0 {
		return fmt.Errorf("no meshes found in GLB file")
	}

	log.Printf("Loaded %d skins, %d nodes", len(r.Skins), len(doc.Nodes))

	// Load animations
	for _, anim := range doc.Animations {
		name := anim.Name
		if name == "" {
			name = fmt.Sprintf("animation_%d", len(r.Animations))
		}

		a := &Animation{
			Name:     name,
			Channels: make([]AnimationChannel, 0),
		}

		for _, channel := range anim.Channels {
			if channel.Target.Node == nil {
				continue
			}

			sampler := anim.Samplers[channel.Sampler]

			// Read timestamps
			timestamps, err := r.readAccessorFloats(doc, int(sampler.Input))
			if err != nil {
				log.Printf("Failed to read animation timestamps: %v", err)
				continue
			}

			// Read values
			values, err := r.readAccessorFloats(doc, int(sampler.Output))
			if err != nil {
				log.Printf("Failed to read animation values: %v", err)
				continue
			}

			// Track maximum duration
			if len(timestamps) > 0 && timestamps[len(timestamps)-1] > a.Duration {
				a.Duration = timestamps[len(timestamps)-1]
			}

			ac := AnimationChannel{
				NodeIndex:  int(*channel.Target.Node),
				Path:       string(channel.Target.Path),
				Timestamps: timestamps,
				Values:     values,
			}
			a.Channels = append(a.Channels, ac)
		}

		if len(a.Channels) > 0 {
			r.Animations[name] = a
			log.Printf("Loaded animation: %s (duration: %.2fs, channels: %d)", name, a.Duration, len(a.Channels))
		}
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

	// Get joint indices (for skinning)
	var joints [][4]uint16
	if jointIdx, ok := prim.Attributes[gltf.JOINTS_0]; ok {
		joints, err = modeler.ReadJoints(doc, doc.Accessors[jointIdx], nil)
		if err != nil {
			log.Printf("Failed to read joints: %v", err)
			joints = nil
		}
	}

	// Get weights (for skinning)
	var weights [][4]float32
	if weightIdx, ok := prim.Attributes[gltf.WEIGHTS_0]; ok {
		weights, err = modeler.ReadWeights(doc, doc.Accessors[weightIdx], nil)
		if err != nil {
			log.Printf("Failed to read weights: %v", err)
			weights = nil
		}
	}

	// Build interleaved vertex data: position (3) + normal (3) + texcoord (2) + joints (4) + weights (4) = 16 floats per vertex
	vertexData := make([]float32, 0, len(positions)*16)
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

		// Joint indices (as floats for shader)
		if joints != nil && i < len(joints) {
			vertexData = append(vertexData,
				float32(joints[i][0]),
				float32(joints[i][1]),
				float32(joints[i][2]),
				float32(joints[i][3]))
		} else {
			vertexData = append(vertexData, 0, 0, 0, 0)
		}

		// Weights
		if weights != nil && i < len(weights) {
			vertexData = append(vertexData,
				weights[i][0],
				weights[i][1],
				weights[i][2],
				weights[i][3])
		} else {
			vertexData = append(vertexData, 0, 0, 0, 0)
		}
	}

	// Create VAO
	gl.GenVertexArrays(1, &m.VAO)
	gl.BindVertexArray(m.VAO)

	// Create VBO
	gl.GenBuffers(1, &m.VBO)
	gl.BindBuffer(gl.ARRAY_BUFFER, m.VBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(vertexData)*4, gl.Ptr(vertexData), gl.STATIC_DRAW)

	stride := int32(16 * 4) // 16 floats * 4 bytes

	// Position attribute (location 0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(0)

	// Normal attribute (location 1)
	gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(1)

	// Texture coordinate attribute (location 2)
	gl.VertexAttribPointerWithOffset(2, 2, gl.FLOAT, false, stride, 6*4)
	gl.EnableVertexAttribArray(2)

	// Joint indices attribute (location 3)
	gl.VertexAttribPointerWithOffset(3, 4, gl.FLOAT, false, stride, 8*4)
	gl.EnableVertexAttribArray(3)

	// Weights attribute (location 4)
	gl.VertexAttribPointerWithOffset(4, 4, gl.FLOAT, false, stride, 12*4)
	gl.EnableVertexAttribArray(4)

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

// PlayAnimation starts playing an animation by name
func (r *GLBRenderer) PlayAnimation(name string, loop bool) error {
	anim, ok := r.Animations[name]
	if !ok {
		// List available animations for debugging
		available := make([]string, 0, len(r.Animations))
		for k := range r.Animations {
			available = append(available, k)
		}
		return fmt.Errorf("animation '%s' not found, available: %v", name, available)
	}

	r.CurrentAnim = anim
	r.AnimStartTime = time.Now()
	r.AnimLoop = loop
	log.Printf("Playing animation: %s (loop: %v)", name, loop)
	return nil
}

// StopAnimation stops the current animation
func (r *GLBRenderer) StopAnimation() {
	r.CurrentAnim = nil
	// Reset to base transforms
	for i := range r.NodeTransforms {
		r.NodeTransforms[i] = r.BaseTransforms[i]
	}
}

// UpdateAnimation updates the animation state - call this each frame
func (r *GLBRenderer) UpdateAnimation() {
	if r.CurrentAnim == nil {
		return
	}

	elapsed := float32(time.Since(r.AnimStartTime).Seconds())

	// Handle looping
	if r.AnimLoop && r.CurrentAnim.Duration > 0 {
		elapsed = float32(math.Mod(float64(elapsed), float64(r.CurrentAnim.Duration)))
	} else if elapsed > r.CurrentAnim.Duration {
		// Animation finished, stop
		r.CurrentAnim = nil
		return
	}

	// Reset to base transforms before applying animation
	for i := range r.NodeTransforms {
		r.NodeTransforms[i] = r.BaseTransforms[i]
	}

	// Apply animation channels
	for _, channel := range r.CurrentAnim.Channels {
		if channel.NodeIndex < 0 || channel.NodeIndex >= len(r.NodeTransforms) {
			continue
		}

		// Find the keyframe
		value := r.interpolateKeyframes(channel, elapsed)

		switch channel.Path {
		case "translation":
			if len(value) >= 3 {
				r.NodeTransforms[channel.NodeIndex].Translation = mgl32.Vec3{value[0], value[1], value[2]}
			}
		case "rotation":
			if len(value) >= 4 {
				r.NodeTransforms[channel.NodeIndex].Rotation = mgl32.Quat{
					W: value[3],
					V: mgl32.Vec3{value[0], value[1], value[2]},
				}
			}
		case "scale":
			if len(value) >= 3 {
				r.NodeTransforms[channel.NodeIndex].Scale = mgl32.Vec3{value[0], value[1], value[2]}
			}
		}
	}
}

// interpolateKeyframes interpolates between keyframes for a given time
func (r *GLBRenderer) interpolateKeyframes(channel AnimationChannel, t float32) []float32 {
	if len(channel.Timestamps) == 0 {
		return nil
	}

	// Determine component count based on path
	components := 3
	if channel.Path == "rotation" {
		components = 4
	}

	// Find keyframe indices using binary search
	count := len(channel.Timestamps)
	// Find smallest index i such that Timestamps[i] > t.
	// Then the interval is [i-1, i].
	idx := sort.Search(count, func(i int) bool {
		return channel.Timestamps[i] > t
	})

	// If idx == 0, t is before the first keyframe (shouldn't happen with mod, but for robustness)
	if idx == 0 {
		if components <= len(channel.Values) {
			return channel.Values[0:components]
		}
		return nil
	}

	// If idx == count, t is past the last keyframe (or equal to it)
	if idx == count {
		startIdx := (count - 1) * components
		if startIdx+components <= len(channel.Values) {
			return channel.Values[startIdx : startIdx+components]
		}
		return nil
	}

	// We are between idx-1 and idx
	keyIdx := idx - 1

	// Linear interpolation between keyframes
	t0 := channel.Timestamps[keyIdx]
	t1 := channel.Timestamps[keyIdx+1]
	factor := (t - t0) / (t1 - t0)
	if factor < 0 {
		factor = 0
	}
	if factor > 1 {
		factor = 1
	}

	startIdx0 := keyIdx * components
	startIdx1 := (keyIdx + 1) * components

	if startIdx1+components > len(channel.Values) {
		return channel.Values[startIdx0 : startIdx0+components]
	}

	result := make([]float32, components)
	if channel.Path == "rotation" {
		// Spherical linear interpolation for quaternions
		q0 := mgl32.Quat{
			W: channel.Values[startIdx0+3],
			V: mgl32.Vec3{channel.Values[startIdx0], channel.Values[startIdx0+1], channel.Values[startIdx0+2]},
		}
		q1 := mgl32.Quat{
			W: channel.Values[startIdx1+3],
			V: mgl32.Vec3{channel.Values[startIdx1], channel.Values[startIdx1+1], channel.Values[startIdx1+2]},
		}
		qr := mgl32.QuatSlerp(q0, q1, factor)
		result[0] = qr.V[0]
		result[1] = qr.V[1]
		result[2] = qr.V[2]
		result[3] = qr.W
	} else {
		// Linear interpolation for translation and scale
		for i := 0; i < components; i++ {
			v0 := channel.Values[startIdx0+i]
			v1 := channel.Values[startIdx1+i]
			result[i] = v0 + (v1-v0)*factor
		}
	}

	return result
}

// getNodeTransformMatrix returns the transform matrix for a node
func (r *GLBRenderer) getNodeTransformMatrix(nodeIndex int) mgl32.Mat4 {
	if nodeIndex < 0 || nodeIndex >= len(r.NodeTransforms) {
		return mgl32.Ident4()
	}

	t := r.NodeTransforms[nodeIndex]
	translation := mgl32.Translate3D(t.Translation[0], t.Translation[1], t.Translation[2])
	rotation := t.Rotation.Mat4()
	scale := mgl32.Scale3D(t.Scale[0], t.Scale[1], t.Scale[2])

	return translation.Mul4(rotation).Mul4(scale)
}

// readAccessorFloats reads float data from a glTF accessor
func (r *GLBRenderer) readAccessorFloats(doc *gltf.Document, accessorIndex int) ([]float32, error) {
	if accessorIndex < 0 || accessorIndex >= len(doc.Accessors) {
		return nil, fmt.Errorf("invalid accessor index: %d", accessorIndex)
	}

	accessor := doc.Accessors[accessorIndex]
	bufferView := doc.BufferViews[*accessor.BufferView]
	buffer := doc.Buffers[bufferView.Buffer]

	data := buffer.Data[bufferView.ByteOffset+accessor.ByteOffset:]

	// Determine element count based on accessor type
	var elemCount int
	switch accessor.Type {
	case gltf.AccessorScalar:
		elemCount = 1
	case gltf.AccessorVec2:
		elemCount = 2
	case gltf.AccessorVec3:
		elemCount = 3
	case gltf.AccessorVec4:
		elemCount = 4
	case gltf.AccessorMat4:
		elemCount = 16
	default:
		elemCount = 1
	}

	totalFloats := int(accessor.Count) * elemCount
	result := make([]float32, totalFloats)

	for i := 0; i < totalFloats; i++ {
		offset := i * 4
		if offset+4 <= len(data) {
			bits := uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
			result[i] = float32frombits(bits)
		}
	}

	return result, nil
}

func float32frombits(b uint32) float32 {
	return *(*float32)(unsafe.Pointer(&b))
}

// getGlobalNodeTransform computes the global (world) transform for a node
func (r *GLBRenderer) getGlobalNodeTransform(nodeIndex int) mgl32.Mat4 {
	if nodeIndex < 0 || nodeIndex >= len(r.NodeTransforms) {
		return mgl32.Ident4()
	}

	localTransform := r.getNodeTransformMatrix(nodeIndex)

	// Walk up the parent chain
	parentIdx := r.NodeParents[nodeIndex]
	if parentIdx >= 0 {
		parentGlobal := r.getGlobalNodeTransform(parentIdx)
		return parentGlobal.Mul4(localTransform)
	}

	return localTransform
}

// computeBoneMatrices calculates the bone matrices for skinned meshes
func (r *GLBRenderer) computeBoneMatrices(skinIndex int) {
	if skinIndex < 0 || skinIndex >= len(r.Skins) {
		return
	}

	skin := r.Skins[skinIndex]

	// Ensure we have enough space for bone matrices
	if len(r.BoneMatrices) < len(skin.Joints) {
		r.BoneMatrices = make([]mgl32.Mat4, len(skin.Joints))
	}

	for i, jointIndex := range skin.Joints {
		// Get global transform for the joint
		globalJointTransform := r.getGlobalNodeTransform(jointIndex)

		// Compute final bone matrix: globalJointTransform * inverseBindMatrix
		r.BoneMatrices[i] = globalJointTransform.Mul4(skin.InverseBindMatrices[i])
	}
}

// Render draws the loaded model with the current texture
func (r *GLBRenderer) Render(windowWidth, windowHeight int32) {
	// Update animation
	r.UpdateAnimation()

	gl.UseProgram(r.ShaderProgram)

	// Set up matrices
	aspect := float32(windowWidth) / float32(windowHeight)
	projection := mgl32.Perspective(mgl32.DegToRad(45.0), aspect, 0.1, 100.0)
	view := mgl32.LookAtV(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})

	gl.UniformMatrix4fv(r.projectionLoc, 1, false, &projection[0])
	gl.UniformMatrix4fv(r.viewLoc, 1, false, &view[0])

	// Bind texture
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, r.TextureID)
	gl.Uniform1i(r.textureLoc, 0)

	// Draw all meshes with their node transforms
	for _, mesh := range r.Meshes {
		// Base model rotation
		baseModel := mgl32.HomogRotate3DY(r.Rotation)

		// Compute and upload bone matrices for skinned meshes
		if mesh.SkinIndex >= 0 && mesh.SkinIndex < len(r.Skins) {
			r.computeBoneMatrices(mesh.SkinIndex)

			// Upload bone matrices to shader
			numJoints := len(r.Skins[mesh.SkinIndex].Joints)
			if numJoints > 128 {
				numJoints = 128
			}
			for i := 0; i < numJoints; i++ {
				loc := gl.GetUniformLocation(r.ShaderProgram, gl.Str(fmt.Sprintf("boneMatrices[%d]\x00", i)))
				gl.UniformMatrix4fv(loc, 1, false, &r.BoneMatrices[i][0])
			}
		} else {
			// For non-skinned meshes, set identity bone matrices
			identity := mgl32.Ident4()
			for i := 0; i < 128; i++ {
				loc := gl.GetUniformLocation(r.ShaderProgram, gl.Str(fmt.Sprintf("boneMatrices[%d]\x00", i)))
				gl.UniformMatrix4fv(loc, 1, false, &identity[0])
			}
		}

		gl.UniformMatrix4fv(r.modelLoc, 1, false, &baseModel[0])

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
