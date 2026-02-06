package vault

import (
	"path/filepath"
	"strings"
)

func hashPrefix(hash string) string {
	if len(hash) < 2 {
		return "xx"
	}
	return hash[:2]
}

func EmlRelPath(hash string) string {
	prefix := hashPrefix(hash)
	filename := hash + ".eml"
	return filepath.Join("blobs", "eml", prefix, filename)
}

func AttachmentRelPath(hash, ext string) string {
	prefix := hashPrefix(hash)
	ext = strings.TrimSpace(ext)
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	filename := hash + ext
	return filepath.Join("blobs", "att", prefix, filename)
}
