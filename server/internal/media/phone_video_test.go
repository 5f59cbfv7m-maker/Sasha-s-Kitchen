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

// TestPhoneVideoPlaysOnApple runs the real ffmpeg on what an iPhone actually
// records: 10-bit HEVC, landscape frames with a 90° display rotation, with
// sound. Each of these broke the ladder before: the output was H.264 High 10
// (no hardware decode on iPhone), a vertical short's "1080p" was 608×1080,
// the asset was recorded as landscape and the whole clip was one segment.
func TestPhoneVideoPlaysOnApple(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes a real clip")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	if out, _ := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output(); !strings.Contains(string(out), "libx265") {
		t.Skip("ffmpeg without libx265")
	}
	dir := t.TempDir()
	raw, src := filepath.Join(dir, "raw.mov"), filepath.Join(dir, "phone.mov")
	for _, args := range [][]string{
		{"-y", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30:duration=13",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=13",
			"-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-tag:v", "hvc1", "-preset", "ultrafast",
			"-c:a", "aac", "-shortest", raw},
		{"-y", "-display_rotation", "90", "-i", raw, "-c", "copy", src},
	} {
		if out, err := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("make source: %v: %s", err, out)
		}
	}

	res, err := (&FFmpegTranscoder{}).TranscodeVideo(context.Background(), src, uuid.New())
	if err != nil {
		t.Fatalf("TranscodeVideo: %v", err)
	}
	if res.Width != 1080 || res.Height != 1920 {
		t.Errorf("asset size = %dx%d, want the displayed 1080x1920", res.Width, res.Height)
	}

	segments := map[string]int{}
	for _, d := range res.Derivatives {
		switch {
		case strings.HasSuffix(d.Key, "master.m3u8"):
			if !strings.Contains(string(d.Data), "RESOLUTION=1080x1920") {
				t.Errorf("master manifest:\n%s", d.Data)
			}
		case strings.HasSuffix(d.Key, ".ts"):
			rung := filepath.Base(filepath.Dir(d.Key))
			segments[rung]++
			if !strings.HasSuffix(d.Key, "seg0000.ts") {
				continue
			}
			p := filepath.Join(dir, rung+".ts")
			if err := os.WriteFile(p, d.Data, 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("ffprobe", "-v", "error", "-show_entries",
				"stream=codec_type,width,height,pix_fmt,profile", "-of", "csv=p=0", p).Output()
			if err != nil {
				t.Fatalf("probe %s: %v", rung, err)
			}
			info := string(out)
			if !strings.Contains(info, "High,") || !strings.Contains(info, "yuv420p\n") && !strings.Contains(info, "yuv420p,") {
				t.Errorf("%s: %q, want 8-bit High profile", rung, info)
			}
			if rung == "1080p" && !strings.Contains(info, ",1080,1920,") {
				t.Errorf("1080p rung of a vertical short: %q, want 1080x1920", info)
			}
			if !strings.Contains(info, "audio") {
				t.Errorf("%s lost its sound: %q", rung, info)
			}
		}
	}
	if len(segments) != 3 {
		t.Errorf("rungs = %v, want 480p, 720p and 1080p", segments)
	}
	for rung, n := range segments {
		if n < 2 {
			t.Errorf("%s: %d segment(s) for 13 s with 6 s segments — keyframes are not forced", rung, n)
		}
	}
}
