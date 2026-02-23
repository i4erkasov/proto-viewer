package protocbin

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// This package is responsible for ensuring protoc binary exists on the local machine.
//
// For Windows we ship protoc as an embedded zip (see protoc_windows_amd64.zip in this folder),
// unpack it to a per-user cache dir, and return absolute path to protoc.exe.
//
// For macOS/Linux we intentionally fallback to asking for protoc in PATH to keep repository size small.

//go:embed protoc_windows_amd64.zip
var embeddedFS embed.FS

func windowsZipBytes() ([]byte, error) {
	return embeddedFS.ReadFile("protoc_windows_amd64.zip")
}

func Ensure() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return ensureWindows()
	default:
		// On non-windows: rely on PATH.
		return "protoc", nil
	}
}

func ensureWindows() (string, error) {
	zipBytes, err := windowsZipBytes()
	if err != nil {
		return "", err
	}
	if len(zipBytes) == 0 {
		return "", errors.New("embedded protoc zip for windows is empty (add real protoc_windows_amd64.zip)")
	}

	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(zipBytes)
	dir := filepath.Join(base, "proto-viewer", "protoc", fmt.Sprintf("%x", sum[:8]))
	bin := filepath.Join(dir, "bin", "protoc.exe")
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}

	// Unzip into a temp dir and atomically move into place.
	// This prevents half-unzipped state if app crashes mid-way.
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), "protoc-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := unzipTo(bytes.NewReader(zipBytes), int64(len(zipBytes)), tmp); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(tmp, "bin", "protoc.exe")); err != nil {
		return "", fmt.Errorf("protoc.exe not found after unzip: %w", err)
	}

	// Best-effort cleanup of old dir then rename.
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		// Rename can fail across volumes. Fallback to copy.
		if err2 := copyDir(tmp, dir); err2 != nil {
			return "", err
		}
	}

	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("protoc.exe not found after install: %w", err)
	}
	return bin, nil
}

func unzipTo(r io.ReaderAt, size int64, dest string) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}

	dest = filepath.Clean(dest)
	for _, f := range zr.File {
		name := filepath.Clean(filepath.FromSlash(f.Name))
		// Protect against zip-slip
		if name == "." || name == string(filepath.Separator) {
			continue
		}
		if strings.HasPrefix(name, ".."+string(filepath.Separator)) || name == ".." {
			return fmt.Errorf("unsafe path in zip: %q", f.Name)
		}
		p := filepath.Join(dest, name)
		if !strings.HasPrefix(p, dest+string(filepath.Separator)) && p != dest {
			return fmt.Errorf("unsafe path in zip: %q", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		// 0644 is enough; Windows doesn't need +x.
		out, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			_ = in.Close()
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			_ = in.Close()
			return err
		}
		_ = out.Close()
		_ = in.Close()
	}
	return nil
}

func copyDir(src, dst string) error {
	r := func(path string) string { return strings.TrimPrefix(path, src+string(filepath.Separator)) }
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel := r(path)
		if rel == "" {
			return os.MkdirAll(dst, 0o755)
		}
		outPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(outPath, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}
