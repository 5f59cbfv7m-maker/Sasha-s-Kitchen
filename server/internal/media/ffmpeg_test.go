package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseFFProbe(t *testing.T) {
	data := []byte(`{
		"streams": [
			{"codec_type": "audio"},
			{"codec_type": "video", "width": 1920, "height": 1080}
		],
		"format": {"duration": "12.345000"}
	}`)
	res, err := parseFFProbe(data)
	if err != nil {
		t.Fatalf("parseFFProbe: %v", err)
	}
	if res.Width != 1920 || res.Height != 1080 {
		t.Fatalf("dimensions = %dx%d, want 1920x1080", res.Width, res.Height)
	}
	if res.DurationMs != 12345 {
		t.Fatalf("DurationMs = %d, want 12345", res.DurationMs)
	}
}

func TestParseFFProbe_NoVideoStream(t *testing.T) {
	data := []byte(`{"streams": [{"codec_type": "audio"}], "format": {"duration": "1.0"}}`)
	if _, err := parseFFProbe(data); err == nil {
		t.Fatal("expected an error when there is no video stream")
	}
}

func TestParseFFProbe_MalformedJSON(t *testing.T) {
	if _, err := parseFFProbe([]byte("not json")); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestParseFFProbe_MissingDuration(t *testing.T) {
	// A still photo run through ffprobe reports a video stream but often no
	// (or a zero) format.duration; that must not be treated as an error.
	data := []byte(`{"streams": [{"codec_type": "video", "width": 800, "height": 600}], "format": {}}`)
	res, err := parseFFProbe(data)
	if err != nil {
		t.Fatalf("parseFFProbe: %v", err)
	}
	if res.Width != 800 || res.Height != 600 || res.DurationMs != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestPickRungs_NeverUpscales(t *testing.T) {
	cases := []struct {
		sourceHeight int
		wantNames    []string
	}{
		{1080, []string{"480p", "720p", "1080p"}},
		{720, []string{"480p", "720p"}},
		{600, []string{"480p"}},
		{360, []string{"360p"}}, // below the smallest rung: one rung at source height, not upscaled to 480p
		{0, nil},
	}
	for _, c := range cases {
		got := pickRungs(c.sourceHeight)
		var names []string
		for _, r := range got {
			names = append(names, r.Name)
			if r.Height > c.sourceHeight && c.sourceHeight > 0 {
				t.Fatalf("source %d: rung %s (%dpx) exceeds source height — that is upscaling", c.sourceHeight, r.Name, r.Height)
			}
		}
		if len(names) != len(c.wantNames) {
			t.Fatalf("source %d: rungs = %v, want %v", c.sourceHeight, names, c.wantNames)
		}
		for i := range names {
			if names[i] != c.wantNames[i] {
				t.Fatalf("source %d: rungs = %v, want %v", c.sourceHeight, names, c.wantNames)
			}
		}
	}
}

func TestBuildProbeArgs_NoShellString(t *testing.T) {
	args := buildProbeArgs("/tmp/some file; rm -rf /.mp4")
	// The path must appear as exactly one argv element, never concatenated
	// into a shell-interpretable string.
	found := false
	for _, a := range args {
		if a == "/tmp/some file; rm -rf /.mp4" {
			found = true
		}
		if strings.Contains(a, ";") && a != "/tmp/some file; rm -rf /.mp4" {
			t.Fatalf("argv element unexpectedly contains a shell metacharacter: %q", a)
		}
	}
	if !found {
		t.Fatalf("source path not found as a single argv element: %v", args)
	}
}

func TestBuildHLSRungArgs_ArgvShape(t *testing.T) {
	args := buildHLSRungArgs("/tmp/src.mp4", "/tmp/out/720p", rung{Name: "720p", Height: 720, Bitrate: 2_800_000}, 6)
	want := []string{
		"-y",
		"-i", "/tmp/src.mp4",
		"-vf", "scale=-2:720",
		"-c:v", "h264",
		"-b:v", "2800000",
		"-c:a", "aac",
		"-hls_time", "6",
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join("/tmp/out/720p", "seg%04d.ts"),
		filepath.Join("/tmp/out/720p", "playlist.m3u8"),
	}
	assertStringSlicesEqual(t, args, want)
}

func TestBuildPosterArgs_ArgvShape(t *testing.T) {
	args := buildPosterArgs("/tmp/src.mp4", "/tmp/poster.jpg", 1.5)
	want := []string{"-y", "-ss", "1.500", "-i", "/tmp/src.mp4", "-frames:v", "1", "/tmp/poster.jpg"}
	assertStringSlicesEqual(t, args, want)
}

func TestBuildPhotoResizeArgs_ArgvShape(t *testing.T) {
	args := buildPhotoResizeArgs("/tmp/src.jpg", "/tmp/thumb.jpg", 200)
	want := []string{
		"-y", "-i", "/tmp/src.jpg",
		"-vf", "scale='min(200,iw)':'min(200,ih)':force_original_aspect_ratio=decrease",
		"-frames:v", "1", "/tmp/thumb.jpg",
	}
	assertStringSlicesEqual(t, args, want)
}

func TestBuildMasterManifest(t *testing.T) {
	rungs := []rung{
		{Name: "480p", Height: 480, Bitrate: 1_400_000},
		{Name: "720p", Height: 720, Bitrate: 2_800_000},
	}
	manifest := buildMasterManifest(rungs, 1920, 1080)
	if !strings.HasPrefix(manifest, "#EXTM3U\n") {
		t.Fatalf("manifest does not start with #EXTM3U: %q", manifest)
	}
	for _, want := range []string{
		"#EXT-X-STREAM-INF:BANDWIDTH=1400000,RESOLUTION=854x480",
		"480p/playlist.m3u8",
		"#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720",
		"720p/playlist.m3u8",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
}

func assertStringSlicesEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv = %v (%d elems), want %v (%d elems)", got, len(got), want, len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full: got=%v want=%v)", i, got[i], want[i], got, want)
		}
	}
}

// TestFFmpegTranscoder_RealBinarySmoke actually runs FFmpegTranscoder against
// ffmpeg/ffprobe when they are available on PATH. This container has neither
// installed, so this test is expected to skip here; it exists so the real
// exec.CommandContext path gets exercised automatically wherever ffmpeg is
// present (e.g. a CI image or a developer machine), rather than only ever
// being reviewed by hand.
func TestFFmpegTranscoder_RealBinarySmoke(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary smoke test")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed; skipping real-binary smoke test")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	// Generate a tiny synthetic clip with ffmpeg itself (lavfi source),
	// so the smoke test needs no checked-in binary fixture.
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=1", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate synthetic source: %v: %s", err, out)
	}

	tc := &FFmpegTranscoder{}
	res, err := tc.TranscodeVideo(context.Background(), src, uuid.New())
	if err != nil {
		t.Fatalf("TranscodeVideo: %v", err)
	}
	if res.Width == 0 || res.Height == 0 {
		t.Fatalf("res = %+v, want non-zero dimensions", res)
	}
	if len(res.Derivatives) == 0 {
		t.Fatal("expected at least one derivative")
	}
	_ = os.Getenv // keep os imported even if unused paths change
}
