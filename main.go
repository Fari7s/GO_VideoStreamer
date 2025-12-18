// main.go - Multi-Quality HLS Video Streaming Server

package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync/atomic"

	// Import the new library package
	"HLSVideoStreamer/lib"
)

var (
	uploadDir = "./uploads"      // Directory for original uploaded video files
	hlsDir    = "./hls"          // Directory for HLS output (segments + playlists per video)
	counter   atomic.Uint64      // Atomic counter for generating unique video IDs
	tmpl      *template.Template // Pre-parsed HTML template
)

// Ensure required directories exist and initialize template at startup
func init() {
	//sumAll r:4, w:2, exec:1 to octo = 7 Owner (read,write,execute)
	//sumAll r:4, w:0, exec:1 to octo = 5 Group (read,no write,execute) (Unix stuff)
	//sumAll r:4, w:0, exec:1 to octo = 5 Others (read,no write,execute)
	os.MkdirAll(uploadDir, 0755)
	os.MkdirAll(hlsDir, 0755)

	// Initialize the template globally
	tmpl = template.Must(template.ParseFiles("index.html"))
}

func main() {
	// Initialize the library with necessary variables
	lib.Init(uploadDir, hlsDir, &counter, tmpl)

	// Route handlers
	http.HandleFunc("/", lib.RootHandler)               // Main page: list videos + upload form
	http.HandleFunc("/upload", lib.UploadHandler)       // Handle new video upload (NO conversion start)
	http.HandleFunc("/reconvert", lib.ReconvertHandler) // Reconvert/Convert an existing unconverted file

	// Delete Handlers
	http.HandleFunc("/delete_hls", lib.DeleteHLSHandler)
	http.HandleFunc("/delete_upload", lib.DeleteUploadHandler)

	// setts src bindings
	http.Handle("/hls/", http.StripPrefix("/hls/", http.FileServer(http.Dir(hlsDir))))
	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadDir))))

	fmt.Println("Go HLS Streaming Server (Multi-Quality)")
	fmt.Println("Visit: http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
