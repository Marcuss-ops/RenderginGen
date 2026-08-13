#include "chronon/chronon3d.h"

#include <cstdio>

extern "C" int chronon3d_render(const char* input, const char* output, const char* backend) {
    // Skeleton: wire up the Vulkan render pipeline here.
    std::fprintf(stderr, "chronon3d_render(input=%s, output=%s, backend=%s) not implemented\n",
                 input, output, backend);
    return 1;
}
