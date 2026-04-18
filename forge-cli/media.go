package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// imageExts is the set of extensions treated as images — description is
// required for these to satisfy WCAG 1.1.1.
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".webp": true, ".svg": true,
}

// runMediaCommand dispatches media subcommands: upload, list, delete.
func runMediaCommand(args []string) {
	if len(args) == 0 {
		printMediaUsage(os.Stderr)
		os.Exit(1)
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	switch args[0] {
	case "upload":
		runMediaUpload(cfg, args[1:])
	case "list":
		runMediaList(cfg, args[1:])
	case "delete":
		runMediaDelete(cfg, args[1:])
	case "-h", "--help", "help":
		printMediaUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown media subcommand %q\n", args[0])
		printMediaUsage(os.Stderr)
		os.Exit(1)
	}
}

// ─── upload ───────────────────────────────────────────────────────────────────

func runMediaUpload(cfg Config, args []string) {
	var (
		filePath    string
		description string
	)

	// Parse positional and flag args.
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--description", "-d":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --description requires a value")
				os.Exit(1)
			}
			description = args[i]
		default:
			if filePath == "" && !strings.HasPrefix(args[i], "-") {
				filePath = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
				os.Exit(1)
			}
		}
	}

	if filePath == "" {
		fmt.Fprintln(os.Stderr, "error: file path required")
		printMediaUsage(os.Stderr)
		os.Exit(1)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if imageExts[ext] && description == "" {
		fmt.Fprintln(os.Stderr, "error: --description is required for image files (WCAG 1.1.1)")
		os.Exit(1)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error reading file:", err)
		os.Exit(1)
	}

	body, contentType, err := buildMultipart(filepath.Base(filePath), data, description)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error building upload:", err)
		os.Exit(1)
	}

	raw, code, err := multipartRequest(cfg, cfg.ForgeURL+"/media", body, contentType)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if code != http.StatusCreated {
		fmt.Fprintf(os.Stderr, "upload failed (%d): %s\n", code, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}

	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		fmt.Fprintln(os.Stderr, "error decoding response:", err)
		os.Exit(1)
	}
	fmt.Println(resp["url"])
}

// buildMultipart constructs a multipart/form-data body for a file upload.
func buildMultipart(filename string, data []byte, description string) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := fw.Write(data); err != nil {
		return nil, "", err
	}
	if description != "" {
		if err := mw.WriteField("description", description); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return &buf, mw.FormDataContentType(), nil
}

// multipartRequest sends a multipart POST and returns raw response bytes and status code.
func multipartRequest(cfg Config, url string, body io.Reader, contentType string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return raw, resp.StatusCode, nil
}

// ─── list ─────────────────────────────────────────────────────────────────────

func runMediaList(cfg Config, args []string) {
	var typeFilter string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--type", "-t":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --type requires a value")
				os.Exit(1)
			}
			typeFilter = args[i]
		default:
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	url := cfg.ForgeURL + "/media"
	if typeFilter != "" {
		url += "?type=" + typeFilter
	}

	raw, code, err := request(cfg, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if code >= 400 {
		fmt.Fprintf(os.Stderr, "list failed (%d): %s\n", code, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}

	var records []map[string]any
	if err := json.Unmarshal(raw, &records); err != nil {
		fmt.Fprintln(os.Stderr, "error decoding response:", err)
		os.Exit(1)
	}

	if len(records) == 0 {
		fmt.Println("no media records found")
		return
	}

	// Print a simple table.
	fmt.Printf("%-22s  %-10s  %-20s  %s\n", "ID", "Type", "Uploaded", "URL")
	fmt.Println(strings.Repeat("-", 80))
	for _, r := range records {
		id, _ := r["id"].(string)
		mt, _ := r["media_type"].(string)
		rawURL, _ := r["url"].(string)
		uploadedAt, _ := r["uploaded_at"].(string)

		uploaded := uploadedAt
		if t, err := time.Parse(time.RFC3339, uploadedAt); err == nil {
			uploaded = t.Format("2006-01-02 15:04")
		}
		fmt.Printf("%-22s  %-10s  %-20s  %s\n", id, mt, uploaded, rawURL)
	}
}

// ─── delete ───────────────────────────────────────────────────────────────────

func runMediaDelete(cfg Config, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: media delete requires an ID")
		os.Exit(1)
	}
	id := args[0]

	raw, code, err := request(cfg, http.MethodDelete, cfg.ForgeURL+"/media/"+id, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	switch code {
	case http.StatusNoContent:
		fmt.Printf("deleted %s\n", id)
	case http.StatusNotFound:
		fmt.Fprintf(os.Stderr, "not found: %s\n", id)
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "delete failed (%d): %s\n", code, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}
}

// ─── usage ────────────────────────────────────────────────────────────────────

func printMediaUsage(w *os.File) {
	fmt.Fprintf(w, `forge-cli media — file upload, listing, and deletion

Usage:
  forge-cli media upload <file> [--description <text>]
  forge-cli media list [--type image|document|video|audio|other]
  forge-cli media delete <id>

Subcommands:
  upload    Upload a file to the Forge media library.
            --description / -d is required for image files (WCAG 1.1.1).
            Prints the URL of the uploaded file on success.
  list      List all media records. Optional --type / -t filter.
            Prints a table of ID, Type, Uploaded, URL.
  delete    Permanently delete a media record by ID.

Environment:
  FORGE_URL    base URL of the running Forge instance (required)
  FORGE_TOKEN  bearer token with Editor or Admin role (required)
`)
}
