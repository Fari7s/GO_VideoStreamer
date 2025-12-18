// lib/lib.go - Contains types, global configuration, and HTTP handlers

package lib

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Global Variables (initialized by main.go)
var (
	uploadDir string
	hlsDir    string
	counter   *atomic.Uint64
	tmpl      *template.Template
)

// Data structures for template rendering (must be exported)
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

// Init initializes the library package's global variables.
func Init(uDir, hDir string, c *atomic.Uint64, t *template.Template) {
	uploadDir = uDir
	hlsDir = hDir
	counter = c
	tmpl = t
}

// DeleteHLSHandler deletes the HLS output directory and all its contents
func DeleteHLSHandler(w http.ResponseWriter, r *http.Request) {
	//Making sure that we are not GETing for safety(deleting stuff on GET is bad)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	//id from POST body where id is the video identifier
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

	//back to main page
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// DeleteUploadHandler deletes the original uploaded video file
func DeleteUploadHandler(w http.ResponseWriter, r *http.Request) {
	//Making sure that we are not GETing for safety(deleting stuff on GET is bad)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	//extract filename from POST body
	filename := r.FormValue("filename")
	if filename == "" {
		http.Error(w, "No filename provided", http.StatusBadRequest)
		return
	}

	//full path to the uploaded file
	inputPath := filepath.Join(uploadDir, filename)

	if err := os.Remove(inputPath); err != nil {
		log.Printf("Failed to delete upload file %s: %v", inputPath, err)
	} else {
		fmt.Printf("Deleted original upload file: %s\n", filename)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// RootHandler renders the main page with current video status
func RootHandler(w http.ResponseWriter, r *http.Request) {
	// Only respond to root path
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

// UploadHandler saves a new video and DOES NOT start background HLS conversion
func UploadHandler(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
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
		// ... (rest of the upload logic) ...
	}
	defer out.Close()

	//Copy uploaded content to the new file in uploads directory
	_, err = io.Copy(out, file)
	if err != nil {
		http.Error(w, "Save failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ReconvertHandler allows manual conversion of an already-uploaded but unconverted file
func ReconvertHandler(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
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

	go ConvertToHLS(inputPath, id) // Note: Calling exported function
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
