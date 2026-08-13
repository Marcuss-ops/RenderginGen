cmake_minimum_required(VERSION 3.20)
project(chronon LANGUAGES CXX VERSION 0.9.4)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Vulkan REQUIRED)

add_library(chronon3d SHARED src/chronon3d.cpp)
target_include_directories(chronon3d PUBLIC include)
target_link_libraries(chronon3d PRIVATE Vulkan::Vulkan)

add_executable(chronon3d_cli src/main.cpp)
target_link_libraries(chronon3d_cli PRIVATE chronon3d)

install(TARGETS chronon3d chronon3d_cli
        RUNTIME DESTINATION bin
        LIBRARY DESTINATION lib)
install(DIRECTORY shaders/ DESTINATION shaders)
