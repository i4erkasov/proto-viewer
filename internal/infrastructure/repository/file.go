package repository

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// FileRepo provides basic IO for local files and HTTP(S) URLs.
//
// It's placed in infrastructure because it depends on os/net/http.
// Decoding / domain logic should be handled elsewhere.
//
// Repo keeps a couple of hints that UI can use (e.g. for gzip auto-toggle).
type FileRepo struct {
	lastHTTPWasGzipped bool
	lastInputLooksGzip bool
}

func NewFile() *FileRepo { return &FileRepo{} }

func (r *FileRepo) LastHTTPWasGzipped() bool { return r.lastHTTPWasGzipped }
func (r *FileRepo) LastInputLooksGzip() bool { return r.lastInputLooksGzip }

func normalizeFileInput(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "file://") {
		u, err := url.Parse(s)
		if err == nil {
			if u.Path != "" {
				return u.Path
			}
		}
	}
	return s
}

func (r *FileRepo) Fetch(ctx context.Context, pathOrURL string) ([]byte, error) {
	in := normalizeFileInput(pathOrURL)
	if in == "" {
		return nil, fmt.Errorf("enter file path or URL")
	}

	r.lastHTTPWasGzipped = false
	r.lastInputLooksGzip = false

	if strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, in, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("http status: %s", resp.Status)
		}

		if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
			r.lastHTTPWasGzipped = true
		}

		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if !r.lastHTTPWasGzipped && looksLikeGzipBytes(b) {
			r.lastInputLooksGzip = true
		}
		return b, nil
	}

	b, err := os.ReadFile(in)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(in), ".gz") || looksLikeGzipBytes(b) {
		r.lastInputLooksGzip = true
	}
	return b, nil
}

func looksLikeGzipBytes(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}
