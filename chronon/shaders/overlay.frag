#version 450

// Placeholder overlay fragment shader (skeleton).
// The real overlay shading pipeline is implemented in chronon3d.cpp.

layout(location = 0) out vec4 outColor;

void main() {
    outColor = vec4(1.0, 0.0, 0.0, 0.5); // solid red, semi-transparent
}
