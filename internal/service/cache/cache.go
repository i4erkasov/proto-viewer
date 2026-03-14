package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Meta describes a cached decode result.
type Meta struct {
	InputPath   string `json:"inputPath"`
	InputSize   int64  `json:"inputSize"`
	InputMtime  int64  `json:"inputMtime"`
	ProtoFile   string `json:"protoFile"`
	ProtoSize   int64  `json:"protoSize"`
	ProtoMtime  int64  `json:"protoMtime"`
	MessageType string `json:"messageType"`
	Gzip        bool   `json:"gzip"`
	Key         string `json:"key"`
	CreatedAt   int64  `json:"createdAt"`
}

// Cache manages on-disk decode result caching.
type Cache struct {
	dir string
}

// New creates a Cache that stores files under dir.
func New(dir string) *Cache {
	return &Cache{dir: dir}
}

func (c *Cache) jsonDir() string { return c.dir }
func (c *Cache) metaDir() string { return filepath.Join(c.dir, "meta") }

func (c *Cache) paths(key string) (jsonPath, metaPath string) {
	jsonPath = filepath.Join(c.jsonDir(), key+".json")
	metaPath = filepath.Join(c.metaDir(), key+".meta.json")
	return
}

func (c *Cache) ensureDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("empty dir")
	}
	return os.MkdirAll(dir, 0o755)
}

// Dir returns the root cache directory.
func (c *Cache) Dir() string { return c.dir }

// Read returns the cached JSON text for the given key, or ok=false if not found.
func (c *Cache) Read(key string) (jsonText string, ok bool, _ error) {
	jsonPath, metaPath := c.paths(key)
	mb, err := os.ReadFile(metaPath)
	if err != nil {
		return "", false, nil
	}
	var meta Meta
	if err := json.Unmarshal(mb, &meta); err != nil {
		return "", false, nil
	}
	if meta.Key != key {
		return "", false, nil
	}
	jb, err := os.ReadFile(jsonPath)
	if err != nil {
		return "", false, nil
	}
	return string(jb), true, nil
}

// Write stores jsonText on disk and returns the path to the JSON file.
func (c *Cache) Write(key string, meta Meta, jsonText string) (jsonPath string, _ error) {
	if err := c.ensureDir(c.jsonDir()); err != nil {
		return "", err
	}
	if err := c.ensureDir(c.metaDir()); err != nil {
		return "", err
	}
	jsonPath, metaPath := c.paths(key)
	meta.Key = key
	meta.CreatedAt = time.Now().Unix()
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")

	writeOne := func(path string, content []byte) error {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, content, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	if err := writeOne(jsonPath, []byte(jsonText)); err != nil {
		return "", err
	}
	if err := writeOne(metaPath, metaBytes); err != nil {
		return "", err
	}
	return jsonPath, nil
}

// JSONPath returns the path where a cached JSON would be stored for this key.
func (c *Cache) JSONPath(key string) string {
	p, _ := c.paths(key)
	return p
}

// FileKey builds a cache key for a file-based decode.
func FileKey(inputPath, protoFileAbs, msgType string, gzip bool, inFI, protoFI os.FileInfo) string {
	h := sha256.New()
	_, _ = h.Write([]byte(inputPath))
	_, _ = h.Write([]byte("\n"))
	_, _ = h.Write([]byte(protoFileAbs))
	_, _ = h.Write([]byte("\n"))
	_, _ = h.Write([]byte(msgType))
	_, _ = h.Write([]byte("\n"))
	if gzip {
		_, _ = h.Write([]byte("gzip=1\n"))
	} else {
		_, _ = h.Write([]byte("gzip=0\n"))
	}
	if inFI != nil {
		_, _ = h.Write([]byte(fmt.Sprintf("in_mtime=%d in_size=%d\n", inFI.ModTime().UnixNano(), inFI.Size())))
	}
	if protoFI != nil {
		_, _ = h.Write([]byte(fmt.Sprintf("proto_mtime=%d proto_size=%d\n", protoFI.ModTime().UnixNano(), protoFI.Size())))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// RedisKey builds a cache key for a redis-based decode.
func RedisKey(db int, key, field, protoFileAbs, msgType string, gzip bool, payload []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(fmt.Sprintf("redis-db=%d\n", db)))
	_, _ = h.Write([]byte("redis-key=" + key + "\n"))
	_, _ = h.Write([]byte("redis-field=" + field + "\n"))
	_, _ = h.Write([]byte("proto-file=" + protoFileAbs + "\n"))
	_, _ = h.Write([]byte("msg-type=" + msgType + "\n"))
	if gzip {
		_, _ = h.Write([]byte("gzip=1\n"))
	} else {
		_, _ = h.Write([]byte("gzip=0\n"))
	}
	if len(payload) > 0 {
		sum := sha256.Sum256(payload)
		_, _ = h.Write([]byte(fmt.Sprintf("payload=%x\n", sum)))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// EnsureDirs creates cache directories if they don't exist.
func (c *Cache) EnsureDirs() error {
	if err := c.ensureDir(c.jsonDir()); err != nil {
		return err
	}
	return c.ensureDir(c.metaDir())
}
