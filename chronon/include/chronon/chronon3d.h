#pragma once

// Chronon3D: minimal Vulkan-based overlay renderer interface.
// Skeleton only; the real render pipeline is implemented in chronon3d.cpp.

extern "C" int chronon3d_render(const char* input, const char* output, const char* backend);
