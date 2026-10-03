package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
)

// scopeHTTPHistoryRetentionToken binds consent to the selected database, not
// merely its contents. Identical copies must each be previewed independently.
// Lexical path aliases are normalized; filesystem validation remains with the
// history opener, which rejects missing files and symlinks.
func scopeHTTPHistoryRetentionToken(path, contentToken string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", errors.New("历史策略需要明确的数据库路径")
	}
	content, err := hex.DecodeString(contentToken)
	if err != nil || len(content) != sha256.Size {
		return "", errors.New("历史策略需要完整内容预览")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	hash.Write([]byte("iotools/http-history-retention/v1\x00"))
	hash.Write([]byte(abs))
	hash.Write([]byte{0})
	hash.Write(content)
	return hex.EncodeToString(hash.Sum(nil)), nil
}
