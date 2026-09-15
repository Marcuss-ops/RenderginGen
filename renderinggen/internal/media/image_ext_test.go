package media

import "testing"

// Real magic-number prefixes, one per format the engine's loader can name. These
// are the actual signatures net/http.DetectContentType keys on, so the table
// fails if the vocabulary stops agreeing with the sniffing it is built on.
var (
	pngHeader  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 24)...)
	jpegHeader = append([]byte("\xff\xd8\xff\xe0"), make([]byte, 24)...)
	gifHeader  = append([]byte("GIF89a"), make([]byte, 24)...)
	webpHeader = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 16)...)
)

func TestImageExtensionForData(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want string
		ok   bool
	}{
		{"png", pngHeader, ExtPNG, true},
		{"jpeg", jpegHeader, ExtJPEG, true},
		{"webp", webpHeader, ExtWebP, true},
		{"gif", gifHeader, ExtGIF, true},
		// Formats the loader cannot name must report ok=false, so the caller
		// keeps the producer's path instead of renaming to a guess. BMP and
		// SVG are the two that look like images but are outside the decoder
		// set this vocabulary mirrors.
		{"bmp is not a loader format", append([]byte("BM"), make([]byte, 32)...), "", false},
		{"svg is not a loader format", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "", false},
		{"pdf", []byte("%PDF-1.7\n"), "", false},
		{"plain text", []byte("not an image at all"), "", false},
		{"empty", nil, "", false},
		// A signature split by a short prefix is not readable; refusing is the
		// fail-safe direction (no rename) rather than a wrong extension.
		{"truncated png", []byte("\x89PNG"), "", false},
		{"jpeg marker only", []byte("\xff\xd8\xff"), ExtJPEG, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ImageExtensionForData(tc.head)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ImageExtensionForData(%q) = (%q, %v), want (%q, %v)", tc.head, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestImageExtensionMatches(t *testing.T) {
	cases := []struct {
		name string
		path string
		head []byte
		want bool
	}{
		{"png behind a png path", "assets/photo.png", pngHeader, true},
		{"png behind an uppercase .PNG path", "assets/photo.PNG", pngHeader, true},
		{"jpeg bytes behind a png path", "assets/photo.png", jpegHeader, false},
		{"jpeg bytes behind a jpg path", "assets/photo.jpg", jpegHeader, true},
		{"jpeg bytes behind a jpeg path", "assets/photo.jpeg", jpegHeader, false},
		{"webp bytes behind a webp path", "assets/photo.webp", webpHeader, true},
		{"gif bytes behind a gif path", "assets/photo.gif", gifHeader, true},
		{"no extension at all", "assets/photo", pngHeader, false},
		{"unrecognized format is never a match", "assets/photo.bmp", []byte("BM\x00\x00\x00\x00"), false},
		{"nested and dotted path", "assets/a.b/photo.png", pngHeader, true},
		{"dot only in a directory name", "assets.v2/photo", pngHeader, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := ImageExtensionMatches(tc.path, tc.head); got != tc.want {
				t.Fatalf("ImageExtensionMatches(%q, ...) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestExtensionOf pins the local extension reader, including the case
// filepath.Ext gets right and a naive LastIndexByte would get wrong: a dot in a
// DIRECTORY name is not an extension.
func TestExtensionOf(t *testing.T) {
	cases := map[string]string{
		"photo.png":            ".png",
		"a/photo.png":          ".png",
		"a.v2/photo":           "",
		"a.v2/photo.png":       ".png",
		"photo":                "",
		".hidden":              ".hidden",
		"dir/sub.dir/file.png": ".png",
	}
	for path, want := range cases {
		if got := extensionOf(path); got != want {
			t.Errorf("extensionOf(%q) = %q, want %q", path, got, want)
		}
	}
}
