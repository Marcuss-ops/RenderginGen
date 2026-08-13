#include "chronon/chronon3d.h"

#include <cstdio>
#include <cstring>

int main(int argc, char** argv) {
    const char* input = nullptr;
    const char* output = nullptr;
    const char* backend = "vulkan";

    for (int i = 1; i < argc; ++i) {
        if (std::strcmp(argv[i], "--input") == 0 && i + 1 < argc) {
            input = argv[++i];
        } else if (std::strcmp(argv[i], "--output") == 0 && i + 1 < argc) {
            output = argv[++i];
        } else if (std::strcmp(argv[i], "--backend") == 0 && i + 1 < argc) {
            backend = argv[++i];
        }
    }

    if (input == nullptr || output == nullptr) {
        std::fprintf(stderr, "usage: chronon3d_cli --input <path> --output <path> [--backend vulkan]\n");
        return 2;
    }

    return chronon3d_render(input, output, backend);
}
