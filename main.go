// main.go - Multi-Quality HLS Video Streaming Server
// Features: Upload videos, automatic multi-resolution HLS conversion (360p/540p/720p),
//           master playlist generation, quality selector in UI, background processing.

package main

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

var (
	uploadDir = "./uploads"                                      // Directory for original uploaded video files
	hlsDir    = "./hls"                                          // Directory for HLS output (segments + playlists per video)
	counter   atomic.Uint64                                      // Atomic counter for generating unique video IDs
	tmpl      = template.Must(template.ParseFiles("index.html")) // Pre-parsed HTML template
)

// Data structures for template rendering
type Video struct {
	ID         string // Unique identifier (e.g., video_123456789_1)
	Ready      bool   // True if master.m3u8 exists (conversion complete)
	Processing bool   // True while conversion is in progress
}

type UploadedFile struct {
	Name string // Filename of unconverted upload (for reconvert dropdown)
}

type TemplateData struct {
	Videos        []Video
	UploadedFiles []UploadedFile
	HasProcessing bool // Used to enable auto-refresh on the page
}

// Ensure required directories exist at startup
func init() {
	os.MkdirAll(uploadDir, 0755)
	os.MkdirAll(hlsDir, 0755)
}

func main() {
	// Route handlers
	http.HandleFunc("/", rootHandler)               // Main page: list videos + upload form
	http.HandleFunc("/upload", uploadHandler)       // Handle new video upload (NO conversion start)
	http.HandleFunc("/reconvert", reconvertHandler) // Reconvert/Convert an existing unconverted file

	// --- NEW DELETE HANDLERS ---
	http.HandleFunc("/delete_hls", deleteHLSHandler)
	http.HandleFunc("/delete_upload", deleteUploadHandler)
	// ---------------------------

	// Static file serving
	http.Handle("/hls/", http.StripPrefix("/hls/", http.FileServer(http.Dir(hlsDir))))
	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadDir))))

	fmt.Println("Go HLS Streaming Server (Multi-Quality)")
	fmt.Println("Visit: http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// --- NEW FUNCTION: Deletes the HLS output directory and all its contents ---
func deleteHLSHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "No ID provided", http.StatusBadRequest)
		return
	}

	outputDir := filepath.Join(hlsDir, id)
	if err := os.RemoveAll(outputDir); err != nil {
		log.Printf("Failed to delete HLS directory %s: %v", outputDir, err)
	} else {
		fmt.Printf("Deleted HLS content for ID: %s\n", id)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func deleteUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filename := r.FormValue("filename")
	if filename == "" {
		http.Error(w, "No filename provided", http.StatusBadRequest)
		return
	}

	inputPath := filepath.Join(uploadDir, filename)

	if err := os.Remove(inputPath); err != nil {
		log.Printf("Failed to delete upload file %s: %v", inputPath, err)
	} else {
		fmt.Printf("Deleted original upload file: %s\n", filename)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// rootHandler renders the main page with current video status
func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	var videos []Video
	hlsVideoIDs := make(map[string]bool)
	hasProcessing := false

	// Scan HLS directory to find all processed/in-progress videos
	hlsDirs, _ := os.ReadDir(hlsDir)
	for _, f := range hlsDirs {
		if f.IsDir() {
			id := f.Name()
			masterPath := filepath.Join(hlsDir, id, "master.m3u8")
			_, err := os.Stat(masterPath)
			ready := (err == nil)

			videos = append(videos, Video{
				ID:         id,
				Ready:      ready,
				Processing: !ready,
			})

			hlsVideoIDs[id] = true
			if !ready {
				hasProcessing = true
			}
		}
	}

	// Find uploaded files that haven't been converted yet (for reconvert dropdown)
	var uploadedFiles []UploadedFile
	uploads, _ := os.ReadDir(uploadDir)

	for _, f := range uploads {
		if !f.IsDir() {
			filename := f.Name()
			ext := filepath.Ext(filename)
			id := strings.TrimSuffix(filename, ext)

			if _, exists := hlsVideoIDs[id]; !exists {
				uploadedFiles = append(uploadedFiles, UploadedFile{Name: filename})
			}
		}
	}

	// Render template with collected data
	data := TemplateData{
		Videos:        videos,
		UploadedFiles: uploadedFiles,
		HasProcessing: hasProcessing,
	}

	//Render/Update html
	tmpl.Execute(w, data)
}

// uploadHandler saves a new video and DOES NOT start background HLS conversion
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "Cannot read file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Generate unique ID to avoid collisions
	id := fmt.Sprintf("video_%d_%d", time.Now().UnixNano(), counter.Add(1))
	ext := filepath.Ext(header.Filename)
	filename := filepath.Join(uploadDir, id+ext)

	// Save uploaded file
	out, err := os.Create(filename)
	if err != nil {
		http.Error(w, "Cannot save file", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		http.Error(w, "Save failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// reconvertHandler allows manual conversion of an already-uploaded but unconverted file
func reconvertHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filename := r.FormValue("uploaded_file")
	if filename == "" {
		http.Error(w, "No file selected", http.StatusBadRequest)
		return
	}

	inputPath := filepath.Join(uploadDir, filename)
	ext := filepath.Ext(filename)
	id := strings.TrimSuffix(filename, ext)

	// Prevent duplicate conversions
	outputDir := filepath.Join(hlsDir, id)
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	go convertToHLS(inputPath, id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// convertToHLS performs multi-resolution encoding and generates a master playlist
func convertToHLS(inputPath, id string) {
	outputDir := filepath.Join(hlsDir, id)

	// Clean any previous failed attempt and prepare fresh directory
	os.RemoveAll(outputDir)

	//sumAll r:4, w:2, exec:1 to octo = 7 Owner (read,write,execute)
	//sumAll r:4, w:0, exec:1 to octo = 5 Group (read,no write,execute) (Unix stuff)
	//sumAll r:4, w:0, exec:1 to octo = 5 Others (read,no write,execute)
	os.MkdirAll(outputDir, 0755)

	type variant struct {
		name     string // e.g., "360p"
		height   string // Target height in pixels
		vbitrate string // Video bitrate
		width    string // Corresponding width for 16:9 aspect ratio
	}

	// Define quality variants (360p, 540p, 720p) with appropriate bitrates
	variants := []variant{
		{"360p", "360", "800k", "640"},
		{"540p", "540", "1800k", "960"},
		{"720p", "720", "3500k", "1280"},
	}

	success := true

	// Encode each variant separately (simpler and more reliable than complex filter_complex)
	for _, v := range variants {
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
	// For html video streamer
	var masterContent strings.Builder
	masterContent.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")

	for _, v := range variants {
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
