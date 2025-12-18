// lib/converter.go - Contains the core HLS conversion logic (FFmpeg execution)

package lib

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// variant defines the parameters for a single HLS quality stream.
type variant struct {
	name     string // e.g., "360p"
	height   string // Target height in pixels
	vbitrate string // Video bitrate
	width    string // Corresponding width for 16:9 aspect ratio
}

// Define quality variants (360p, 540p, 720p) with appropriate bitrates
var HLS_VARIANTS = []variant{
	{"360p", "360", "800k", "640"},
	{"540p", "540", "1800k", "960"},
	{"720p", "720", "3500k", "1280"},
	{"1080p", "1080", "6000k", "1920"},
}

// ConvertToHLS performs multi-resolution encoding and generates a master playlist.
// It is run as a background goroutine.
func ConvertToHLS(inputPath, id string) {
	outputDir := filepath.Join(hlsDir, id)

	// Clean any previous failed attempt and prepare fresh directory
	os.RemoveAll(outputDir)
	os.MkdirAll(outputDir, 0755)

	success := true

	// Encode each variant separately
	for _, v := range HLS_VARIANTS {
		streamName := fmt.Sprintf("stream_%s.m3u8", v.name)
		segmentPattern := filepath.Join(outputDir, fmt.Sprintf("%s_segment_%%03d.ts", v.name))

		cmd := exec.Command("ffmpeg",
			"-i", inputPath,
			"-vf", fmt.Sprintf("scale=%s:%s", v.width, v.height), // Force exact resolution
			"-c:v", "libx264", // H.264 codec

			"-preset", "veryfast", // Preset - I guess its a playback stuff
			"-b:v", v.vbitrate, // selected bitrate
			"-maxrate", v.vbitrate, // max bitrate
			"-bufsize", "2M",
			"-g", "30", // Group of Pictures size, 30FPS each

			"-c:a", "aac", //audio codec
			"-b:a", "128k", //audio bitrate

			"-hls_time", "10", // segment dur
			"-hls_playlist_type", "vod", //video on demand
			"-hls_segment_filename", segmentPattern,

			filepath.Join(outputDir, streamName), //output file
		)

		fmt.Printf("Encoding %s variant for %s...\n", v.name, id)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("FFmpeg failed for %s (%s): %v\n", id, v.name, err)
			success = false
			break
		}
	}

	// If any variant failed, clean up to allow retry
	if !success {
		os.RemoveAll(outputDir)
		return
	}

	// Build master playlist that references all variant streams
	var masterContent strings.Builder
	masterContent.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")

	for _, v := range HLS_VARIANTS {
		bandwidth := strings.TrimSuffix(v.vbitrate, "k") + "000"
		streamName := fmt.Sprintf("stream_%s.m3u8", v.name)
		masterContent.WriteString(fmt.Sprintf(
			"#EXT-X-STREAM-INF:BANDWIDTH=%s,RESOLUTION=%sx%s,NAME=\"%s\"\n",
			bandwidth, v.width, v.height, v.name,
		))
		masterContent.WriteString(streamName + "\n")
	}

	// Write master playlist — its presence signals "ready" status
	masterPlaylistPath := filepath.Join(outputDir, "master.m3u8")
	if err := os.WriteFile(masterPlaylistPath, []byte(masterContent.String()), 0644); err != nil {
		fmt.Printf("Failed to write master playlist: %v\n", err)
		os.RemoveAll(outputDir)
		return
	}

	fmt.Printf("Multi-quality HLS ready: /hls/%s/master.m3u8\n", id)
}
