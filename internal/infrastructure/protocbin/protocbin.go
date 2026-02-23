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

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := unzipTo(bytes.NewReader(zipBytes), int64(len(zipBytes)), dir); err != nil {
		return "", err
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("protoc.exe not found after unzip: %w", err)
	}
	return bin, nil
}

func unzipTo(r io.ReaderAt, size int64, dest string) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		p := filepath.Join(dest, filepath.FromSlash(f.Name))
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
		out, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
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
