#include <fstream>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

struct Variant {
    const char* id;
    int line;
    double width;
    const char* color;
    double pan;
    double yaw;
    double tilt;
    bool is_marker;
};

std::string quote(const std::string& value) {
    std::string result = "\"";
    for (char c : value) {
        if (c == '\\' || c == '"') result += '\\';
        if (c == '\n') { result += "\\n"; continue; }
        result += c;
    }
    return result + "\"";
}

std::string tracks_json(const Variant& variant) {
    std::ostringstream ss;
    bool first = true;
    const auto add_track = [&](const char* prop, double from, double to) {
        if (!first) ss << ',';
        ss << "{\"property\":\"" << prop << "\",\"easing\":\"in_out_sine\",\"keyframes\":["
           << "{\"frame\":12,\"value\":" << from << "},{\"frame\":48,\"value\":" << to << "},"
           << "{\"frame\":72,\"value\":" << to << "}]}";
        first = false;
    };
    if (variant.pan != 0.0) add_track("position_x", variant.pan, 0.0);
    if (variant.yaw != 0.0) add_track("rotation_y", variant.yaw, 0.0);
    if (variant.tilt != 0.0) add_track("rotation_x", variant.tilt, 0.0);
    return ss.str();
}

std::string text_layer(int index, const std::string& phrase, int y_3d, bool is_selected, const Variant& variant) {
    std::ostringstream layer;
    const std::string anim = tracks_json(variant);
    const char* fill_color = is_selected ? "#181B20" : "#7A828E";
    layer << "{\"id\":\"phrase_" << index + 1 << "\",\"type\":\"text\",\"text\":"
          << quote(phrase)
          << ",\"size\":[1440,68],\"position\":[960," << y_3d
          << "],\"enable_3d\":true,\"start_frame\":0,\"duration_frames\":150";
    if (!anim.empty()) {
        layer << ",\"animation\":{\"tracks\":[" << anim << "]}";
    }
    layer << ",\"style\":{"
          << "\"font\":\"assets/fonts/Inter-SemiBold.ttf\",\"font_size\":42,"
          << "\"fill\":\"" << fill_color << "\",\"fit_mode\":\"shrink_only\","
          << "\"min_font_size\":42,\"max_font_size\":42}}";
    return layer.str();
}

std::string highlighter_shape(const Variant& variant, int y_3d) {
    const double half = variant.width / 2.0;
    const std::string extra_tracks = tracks_json(variant);
    std::ostringstream layer;

    if (variant.is_marker) {
        // Classic documentary yellow brush highlighter swath (height ~46px over text)
        layer << "{\"id\":\"doc_highlighter\",\"type\":\"shape\",\"size\":["
              << variant.width << ",68],\"position\":[960," << y_3d - 3 << "],\"enable_3d\":true,\"start_frame\":0,"
              << "\"duration_frames\":150,\"animation\":{\"tracks\":["
              << "{\"property\":\"opacity\",\"easing\":\"linear\",\"keyframes\":["
              << "{\"frame\":0,\"value\":0},{\"frame\":12,\"value\":0.85},{\"frame\":149,\"value\":0.85}]}";

        if (!extra_tracks.empty()) {
            layer << ',' << extra_tracks;
        }

        layer << "]},\"shape\":{\"type\":\"path\",\"fill\":[0,0,0,0],"
              << "\"stroke\":{\"color\":\"" << variant.color << "\",\"width\":46},\"path\":["
              << "{\"type\":\"move_to\",\"point\":[" << -half << ",0]},"
              << "{\"type\":\"cubic_to\",\"point\":[0,-1],\"control1\":[" << -half * 0.3 << ",2],"
              << "\"control2\":[" << -half * 0.1 << ",-2]},"
              << "{\"type\":\"cubic_to\",\"point\":[" << half << ",0],\"control1\":["
              << half * 0.1 << ",1],\"control2\":[" << half * 0.3 << ",-1]}],"
              << "\"operators\":[{\"kind\":\"trim\",\"params\":{\"start\":0,\"end\":1,"
              << "\"animation\":{\"easing\":\"in_out_quad\",\"keyframes\":["
              << "{\"frame\":12,\"value\":[0,0]},{\"frame\":48,\"value\":[0,1]},"
              << "{\"frame\":149,\"value\":[0,1]}]}}}]}}";
    } else {
        // Organic Brush underline below text
        const double bend = variant.width * 0.16;
        layer << "{\"id\":\"brush_underline\",\"type\":\"shape\",\"size\":["
              << variant.width << ",76],\"position\":[960," << y_3d << "],\"enable_3d\":true,\"start_frame\":0,"
              << "\"duration_frames\":150,\"animation\":{\"tracks\":["
              << "{\"property\":\"opacity\",\"easing\":\"linear\",\"keyframes\":["
              << "{\"frame\":0,\"value\":0},{\"frame\":12,\"value\":1},{\"frame\":149,\"value\":1}]}";

        if (!extra_tracks.empty()) {
            layer << ',' << extra_tracks;
        }

        layer << "]},\"shape\":{\"type\":\"path\",\"fill\":[0,0,0,0],"
              << "\"stroke\":{\"color\":\"" << variant.color << "\",\"width\":13},\"path\":["
              << "{\"type\":\"move_to\",\"point\":[" << -half << ",28]},"
              << "{\"type\":\"cubic_to\",\"point\":[0,29],\"control1\":[" << -bend << ",18],"
              << "\"control2\":[" << -variant.width * 0.07 << ",38]},"
              << "{\"type\":\"cubic_to\",\"point\":[" << half << ",27],\"control1\":["
              << variant.width * 0.07 << ",21],\"control2\":[" << bend << ",37]}],"
              << "\"operators\":[{\"kind\":\"trim\",\"params\":{\"start\":0,\"end\":1,"
              << "\"animation\":{\"easing\":\"in_out_quad\",\"keyframes\":["
              << "{\"frame\":12,\"value\":[0,0]},{\"frame\":48,\"value\":[0,1]},"
              << "{\"frame\":149,\"value\":[0,1]}]}}}]}}";
    }
    return layer.str();
}

int main(int argc, char** argv) {
    if (argc != 2) return 2;
    const std::string out = argv[1];
    const std::vector<std::string> phrases = {
        "Most people scan a page before they decide to read.",
        "Short paragraphs make useful details easier to find.",
        "A clear sentence can hold a reader's attention.",
        "Color helps important words stand out from the page.",
        "The best layouts leave room for the words to breathe."
    };
    const std::vector<Variant> variants = {
        // 1..6: Underline presets (Brush family)
        {"01_lime",                0, 1080, "#84CC16",   0.0,   0.0,  0.0, false},
        {"02_yellow",              1, 1080, "#EAB308", -38.0,   0.0,  0.0, false},
        {"03_coral",               2,  980, "#FB7185",   0.0, -12.0,  0.0, false},
        {"04_blue",                3, 1120, "#0EA5E9",   0.0,   0.0, 10.0, false},
        {"05_violet",              4, 1120, "#A855F7",   0.0, -10.0, -7.0, false},
        {"06_lime_wide",           2, 1220, "#84CC16", -40.0,   8.0,  8.0, false},

        // 7..12: Classic Documentary Yellow Brush Highlighter presets
        {"doc_01_yellow_focus",    0, 1064, "#FFE600",   0.0,   0.0,  0.0, true},
        {"doc_02_yellow_pan",      1, 1046, "#FFE600", -38.0,   0.0,  0.0, true},
        {"doc_03_yellow_yaw",      2,  946, "#FFE600",   0.0, -12.0,  0.0, true},
        {"doc_04_yellow_tilt",     3, 1086, "#FFE600",   0.0,   0.0, 10.0, true},
        {"doc_05_yellow_yaw_tilt", 4, 1080, "#FFE600",   0.0, -10.0, -7.0, true},
        {"doc_06_yellow_wide",     2, 1180, "#FFE600", -40.0,   8.0,  8.0, true}
    };

    for (const auto& variant : variants) {
        std::ostringstream plan;
        plan << "{\"schema\":\"chronon.render-plan.v3\",\"version\":3,\"job_id\":\"web-page-"
             << variant.id << "\",\"canvas\":{\"width\":1920,\"height\":1080,"
             << "\"fps_num\":30,\"fps_den\":1,\"duration_frames\":150},"
             << "\"output\":{\"path\":\"" << variant.id << ".mp4\",\"format\":\"mp4\",\"codec\":\"h264\"},"
             << "\"layers\":[{\"id\":\"white_page\",\"type\":\"color\",\"color\":[1,1,1,1],"
             << "\"size\":[1920,1080],\"screen_space\":true,\"start_frame\":0,\"duration_frames\":150},";

        const int first_y = 398;
        const int line_step = 72;
        const int text_screen_y = first_y + variant.line * line_step;
        const int highlight_y_3d = variant.is_marker ? (1080 - text_screen_y) : (1077 - text_screen_y);

        plan << highlighter_shape(variant, highlight_y_3d);
        for (int i = 0; i < static_cast<int>(phrases.size()); ++i) {
            const int phrase_screen_y = first_y + i * line_step;
            const int phrase_y_3d = 1080 - phrase_screen_y;
            const bool is_sel = (i == variant.line);
            plan << ',' << text_layer(i, phrases[i], phrase_y_3d, is_sel, variant);
        }
        plan << "]}";

        std::ofstream file(out + "/" + variant.id + ".plan.json");
        file << plan.str() << '\n';
        std::cout << "generated " << variant.id << '\n';
    }

    std::ofstream manifest(out + "/web_page_highlight_manifest.json");
    manifest << "{\"schema\":\"renderinggen.web.simple_highlight.v3\","
             << "\"canvas\":\"1920x1080, 30 fps, 5 seconds\",\"font\":\"Inter SemiBold\","
             << "\"text_motion\":\"static layout, visible frame 0\","
             << "\"text_hierarchy\":{\"selected\":\"#181B20\",\"unselected\":\"#7A828E\"},"
             << "\"families\":[\"underline_brush\",\"documentary_yellow_highlighter\"],"
             << "\"phrases\":[";
    for (size_t i = 0; i < phrases.size(); ++i) {
        if (i) manifest << ',';
        manifest << quote(phrases[i]);
    }
    manifest << "],\"variants\":[";
    for (size_t i = 0; i < variants.size(); ++i) {
        if (i) manifest << ',';
        manifest << "{\"id\":" << quote(variants[i].id)
                 << ",\"type\":" << quote(variants[i].is_marker ? "documentary_highlighter" : "brush_underline")
                 << ",\"line\":" << variants[i].line
                 << ",\"color\":" << quote(variants[i].color)
                 << ",\"width\":" << variants[i].width << "}";
    }
    manifest << "]}\n";
}
