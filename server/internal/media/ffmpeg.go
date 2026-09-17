package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// FFmpegTranscoder is the real Transcoder: it shells out to ffmpeg and
// ffprobe. Every invocation below builds its argument list as a []string and
// hands it straight to exec.CommandContext — never through a shell (no
// os/exec Command with a joined string, no sh -c) — so nothing in a path ever
// gets a chance to be interpreted as a second argument or a shell
// metacharacter. In this codebase every path FFmpegTranscoder touches is one
// this package created itself under a temp directory (see Worker), never a
// client-supplied filename, so this is defence in depth rather than the only
// thing preventing injection.
//
// NOT EXECUTED HERE: this container has neither ffmpeg nor ffprobe installed
// (by design — see the task instructions), so TranscodePhoto/TranscodeVideo
// have been reviewed by hand but never actually run. Only the pure helpers
// below them (parseFFProbe, pickRungs, the argv builders, buildMasterManifest)
// are exercised by ffmpeg_test.go, using canned input instead of a real
// process. If ffmpeg/ffprobe happen to be present wherever this runs,
// TestFFmpegTranscoder_RealBinarySmoke opts itself back in and actually
// exercises this path; everywhere else it skips.
type FFmpegTranscoder struct {
	FFmpegPath  string // defaults to "ffmpeg"
	FFprobePath string // defaults to "ffprobe"
}

func (t *FFmpegTranscoder) ffmpegBin() string {
	if t.FFmpegPath != "" {
		return t.FFmpegPath
	}
	return "ffmpeg"
}

func (t *FFmpegTranscoder) ffprobeBin() string {
	if t.FFprobePath != "" {
		return t.FFprobePath
	}
	return "ffprobe"
}

// probeResult is what ffprobe tells us about a source file.
type probeResult struct {
	Width, Height, DurationMs int
}

func (t *FFmpegTranscoder) probe(ctx context.Context, srcPath string) (probeResult, error) {
	cmd := exec.CommandContext(ctx, t.ffprobeBin(), buildProbeArgs(srcPath)...)
	out, err := cmd.Output()
	if err != nil {
		return probeResult{}, fmt.Errorf("media: ffprobe %s: %w", srcPath, err)
	}
	return parseFFProbe(out)
}

// buildProbeArgs is the argv for reading a source file's dimensions and
// duration as JSON, without transcoding anything.
func buildProbeArgs(srcPath string) []string {
	return []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		srcPath,
	}
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// parseFFProbe extracts the dimensions of the first video stream and the
// overall duration from ffprobe's JSON output. It is a standalone function,
// independent of running an actual process, precisely so it can be unit
// tested with canned JSON.
func parseFFProbe(data []byte) (probeResult, error) {
	var out ffprobeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return probeResult{}, fmt.Errorf("media: parse ffprobe output: %w", err)
	}
	var res probeResult
	for _, s := range out.Streams {
		if s.CodecType == "video" {
			res.Width = s.Width
			res.Height = s.Height
			break
		}
	}
	if res.Width <= 0 || res.Height <= 0 {
		return probeResult{}, fmt.Errorf("media: ffprobe reported no usable video stream")
	}
	if out.Format.Duration != "" {
		if secs, err := strconv.ParseFloat(out.Format.Duration, 64); err == nil && secs > 0 {
			res.DurationMs = int(secs * 1000)
		}
	}
	return res, nil
}

// rung is one step of a video's HLS quality ladder.
type rung struct {
	Name    string // e.g. "480p"
	Height  int    // target height in pixels
	Bitrate int    // target video bitrate, bits/sec
}

// standardRungs is the full quality ladder this service offers, highest
// height last. pickRungs trims it to whatever does not exceed the source.
var standardRungs = []rung{
	{Name: "480p", Height: 480, Bitrate: 1_400_000},
	{Name: "720p", Height: 720, Bitrate: 2_800_000},
	{Name: "1080p", Height: 1080, Bitrate: 5_000_000},
}

// pickRungs selects the rungs to encode for a source of the given height:
// every standard rung that fits within the source, so the ladder is never
// upscaled. A source shorter than the smallest standard rung still gets
// exactly one rung, at the source's own height, rather than being upscaled
// to reach 480p.
func pickRungs(sourceHeight int) []rung {
	if sourceHeight <= 0 {
		return nil
	}
	var out []rung
	for _, r := range standardRungs {
		if r.Height <= sourceHeight {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		out = append(out, rung{
			Name:    fmt.Sprintf("%dp", sourceHeight),
			Height:  sourceHeight,
			Bitrate: standardRungs[0].Bitrate,
		})
	}
	return out
}

// buildHLSRungArgs is the argv to encode one rung of the HLS ladder into
// outDir, scaling to r.Height while preserving aspect ratio (width computed
// by ffmpeg via -2, always even). It never upscales because pickRungs never
// selects a rung taller than the source.
func buildHLSRungArgs(srcPath, outDir string, r rung, segmentSeconds int) []string {
	return []string{
		"-y",
		"-i", srcPath,
		"-vf", fmt.Sprintf("scale=-2:%d", r.Height),
		"-c:v", "h264",
		"-b:v", strconv.Itoa(r.Bitrate),
		"-c:a", "aac",
		"-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outDir, "seg%04d.ts"),
		filepath.Join(outDir, "playlist.m3u8"),
	}
}

// buildPosterArgs is the argv to extract a single frame at atSeconds.
func buildPosterArgs(srcPath, dstPath string, atSeconds float64) []string {
	return []string{
		"-y",
		"-ss", strconv.FormatFloat(atSeconds, 'f', 3, 64),
		"-i", srcPath,
		"-frames:v", "1",
		dstPath,
	}
}

// buildPhotoResizeArgs is the argv to resize a photo so neither dimension
// exceeds maxDim, preserving aspect ratio, and never upscaling (the
// min(maxDim, iw/ih) terms make scale() a no-op when the source is already
// smaller than maxDim).
func buildPhotoResizeArgs(srcPath, dstPath string, maxDim int) []string {
	return []string{
		"-y",
		"-i", srcPath,
		"-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", maxDim, maxDim),
		"-frames:v", "1",
		dstPath,
	}
}

// buildMasterManifest writes the top-level HLS manifest referencing each
// rung's own playlist. width/height are the source's, used only to estimate
// each rung's encoded width for the RESOLUTION attribute (informational;
// players fall back to measuring the stream directly).
func buildMasterManifest(rungs []rung, sourceWidth, sourceHeight int) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, r := range rungs {
		width := r.Height
		if sourceHeight > 0 {
			width = int(float64(sourceWidth) * float64(r.Height) / float64(sourceHeight))
			if width%2 != 0 {
				width++ // ffmpeg's -2 scale always yields an even width
			}
		}
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n%s/playlist.m3u8\n",
			r.Bitrate, width, r.Height, r.Name)
	}
	return b.String()
}

const photoBlurhashComponentsX, photoBlurhashComponentsY = 4, 3

// TranscodePhoto resizes srcPath into thumb/card/full derivatives (each
// capped at its own maxDim, never upscaled) and computes a BlurHash from the
// card-sized derivative.
func (t *FFmpegTranscoder) TranscodePhoto(ctx context.Context, srcPath string, assetID uuid.UUID) (TranscodeResult, error) {
	info, err := t.probe(ctx, srcPath)
	if err != nil {
		return TranscodeResult{}, err
	}

	dir, err := os.MkdirTemp("", "media-photo-*")
	if err != nil {
		return TranscodeResult{}, fmt.Errorf("media: temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	sizes := []struct {
		name   string
		maxDim int
	}{
		{"thumb", 200},
		{"card", 800},
		{"full", 2000},
	}

	var derivatives []Derivative
	var cardData []byte
	for _, s := range sizes {
		dst := filepath.Join(dir, s.name+".jpg")
		cmd := exec.CommandContext(ctx, t.ffmpegBin(), buildPhotoResizeArgs(srcPath, dst, s.maxDim)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return TranscodeResult{}, fmt.Errorf("media: ffmpeg resize %s: %w: %s", s.name, err, out)
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			return TranscodeResult{}, fmt.Errorf("media: read %s derivative: %w", s.name, err)
		}
		derivatives = append(derivatives, Derivative{
			Key:         PhotoDerivativeKey(assetID, s.name),
			ContentType: "image/jpeg",
			Size:        int64(len(data)),
			Data:        data,
		})
		if s.name == "card" {
			cardData = data
		}
	}

	blurhash := ""
	if len(cardData) > 0 {
		img, err := jpeg.Decode(bytes.NewReader(cardData))
		if err != nil {
			return TranscodeResult{}, fmt.Errorf("media: decode card derivative for blurhash: %w", err)
		}
		blurhash, err = EncodeBlurhash(img, photoBlurhashComponentsX, photoBlurhashComponentsY)
		if err != nil {
			return TranscodeResult{}, fmt.Errorf("media: blurhash: %w", err)
		}
	}

	return TranscodeResult{
		Width:       info.Width,
		Height:      info.Height,
		PosterKey:   PhotoDerivativeKey(assetID, "card"),
		Blurhash:    blurhash,
		Derivatives: derivatives,
	}, nil
}

const hlsSegmentSeconds = 6

// TranscodeVideo produces an HLS ladder (capped at the source resolution),
// a poster frame, and a BlurHash of that poster.
func (t *FFmpegTranscoder) TranscodeVideo(ctx context.Context, srcPath string, assetID uuid.UUID) (TranscodeResult, error) {
	info, err := t.probe(ctx, srcPath)
	if err != nil {
		return TranscodeResult{}, err
	}
	rungs := pickRungs(info.Height)
	if len(rungs) == 0 {
		return TranscodeResult{}, fmt.Errorf("media: could not determine an HLS ladder for source height %d", info.Height)
	}

	dir, err := os.MkdirTemp("", "media-video-*")
	if err != nil {
		return TranscodeResult{}, fmt.Errorf("media: temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	var derivatives []Derivative
	for _, r := range rungs {
		rungDir := filepath.Join(dir, r.Name)
		if err := os.MkdirAll(rungDir, 0o755); err != nil {
			return TranscodeResult{}, fmt.Errorf("media: mkdir rung dir: %w", err)
		}
		cmd := exec.CommandContext(ctx, t.ffmpegBin(), buildHLSRungArgs(srcPath, rungDir, r, hlsSegmentSeconds)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return TranscodeResult{}, fmt.Errorf("media: ffmpeg encode rung %s: %w: %s", r.Name, err, out)
		}

		playlist, err := os.ReadFile(filepath.Join(rungDir, "playlist.m3u8"))
		if err != nil {
			return TranscodeResult{}, fmt.Errorf("media: read rung %s playlist: %w", r.Name, err)
		}
		derivatives = append(derivatives, Derivative{
			Key:         VideoRungKey(assetID, r.Name),
			ContentType: "application/vnd.apple.mpegurl",
			Size:        int64(len(playlist)),
			Data:        playlist,
		})

		segments, err := filepath.Glob(filepath.Join(rungDir, "seg*.ts"))
		if err != nil {
			return TranscodeResult{}, fmt.Errorf("media: list rung %s segments: %w", r.Name, err)
		}
		sort.Strings(segments)
		for i, seg := range segments {
			data, err := os.ReadFile(seg)
			if err != nil {
				return TranscodeResult{}, fmt.Errorf("media: read segment %s: %w", seg, err)
			}
			derivatives = append(derivatives, Derivative{
				Key:         VideoSegmentKey(assetID, r.Name, i),
				ContentType: "video/mp2t",
				Size:        int64(len(data)),
				Data:        data,
			})
		}
	}

	manifest := buildMasterManifest(rungs, info.Width, info.Height)
	derivatives = append(derivatives, Derivative{
		Key:         VideoHLSKey(assetID),
		ContentType: "application/vnd.apple.mpegurl",
		Size:        int64(len(manifest)),
		Data:        []byte(manifest),
	})

	posterAt := 1.0
	if info.DurationMs > 0 {
		tenPercent := float64(info.DurationMs) / 1000 * 0.1
		if tenPercent < posterAt {
			posterAt = tenPercent
		}
	}
	posterPath := filepath.Join(dir, "poster.jpg")
	cmd := exec.CommandContext(ctx, t.ffmpegBin(), buildPosterArgs(srcPath, posterPath, posterAt)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return TranscodeResult{}, fmt.Errorf("media: ffmpeg poster: %w: %s", err, out)
	}
	posterData, err := os.ReadFile(posterPath)
	if err != nil {
		return TranscodeResult{}, fmt.Errorf("media: read poster: %w", err)
	}
	derivatives = append(derivatives, Derivative{
		Key:         VideoPosterKey(assetID),
		ContentType: "image/jpeg",
		Size:        int64(len(posterData)),
		Data:        posterData,
	})

	posterImg, err := jpeg.Decode(bytes.NewReader(posterData))
	if err != nil {
		return TranscodeResult{}, fmt.Errorf("media: decode poster for blurhash: %w", err)
	}
	blurhash, err := EncodeBlurhash(posterImg, photoBlurhashComponentsX, photoBlurhashComponentsY)
	if err != nil {
		return TranscodeResult{}, fmt.Errorf("media: blurhash: %w", err)
	}

	return TranscodeResult{
		Width:       info.Width,
		Height:      info.Height,
		DurationMs:  info.DurationMs,
		PosterKey:   VideoPosterKey(assetID),
		HLSKey:      VideoHLSKey(assetID),
		Blurhash:    blurhash,
		Derivatives: derivatives,
	}, nil
}

var _ Transcoder = (*FFmpegTranscoder)(nil)
