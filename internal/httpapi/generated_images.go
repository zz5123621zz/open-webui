package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

// A 50 MiB Base64 SSE event can carry at most about 37.5 MiB decoded, before
// JSON overhead. This guard therefore does not lower the quality of any image
// that can cross the agreed CPA/Nginx transport boundary.
const maxDecodedGeneratedImageBytes = 40 * 1024 * 1024

type generatedImage struct {
	AttachmentID string `json:"attachmentId"`
	URL          string `json:"url"`
	MediaType    string `json:"mediaType"`
	ByteSize     int64  `json:"byteSize"`
}

func (s *Server) saveGeneratedImage(ctx context.Context, userID, conversationID, messageID, encoded string) (generatedImage, error) {
	if separator := strings.Index(encoded, ","); strings.HasPrefix(encoded, "data:image/") && separator > 0 {
		encoded = encoded[separator+1:]
	}
	attachmentID, err := ids.New()
	if err != nil {
		return generatedImage{}, err
	}
	tempDir := filepath.Join(s.cfg.DataDir, "tmp", "provider")
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return generatedImage{}, err
	}
	tempFile, err := os.CreateTemp(tempDir, attachmentID+".*")
	if err != nil {
		return generatedImage{}, err
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
	}()
	if err := tempFile.Chmod(0o600); err != nil {
		return generatedImage{}, err
	}

	hasher := sha256.New()
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	written, err := io.Copy(io.MultiWriter(tempFile, hasher), io.LimitReader(decoder, maxDecodedGeneratedImageBytes+1))
	if err != nil {
		return generatedImage{}, errors.New("decode generated image")
	}
	if written == 0 || written > maxDecodedGeneratedImageBytes {
		return generatedImage{}, errors.New("generated image exceeds transport safety boundary")
	}
	if err := tempFile.Sync(); err != nil {
		return generatedImage{}, err
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		return generatedImage{}, err
	}
	header := make([]byte, 512)
	headerLength, _ := io.ReadFull(tempFile, header)
	mediaType := http.DetectContentType(header[:headerLength])
	extension, ok := imageExtension(mediaType)
	if !ok {
		return generatedImage{}, errors.New("provider returned an unsupported image format")
	}
	if err := validateImageFile(tempFile, mediaType); err != nil {
		return generatedImage{}, errors.New("provider returned an invalid image")
	}

	now := time.Now().UTC()
	relativePath := filepath.Join("generated", userID, now.Format("2006"), now.Format("01"), attachmentID+extension)
	destination := filepath.Join(s.cfg.DataDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return generatedImage{}, err
	}
	if err := tempFile.Close(); err != nil {
		return generatedImage{}, err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return generatedImage{}, err
	}
	tempPath = ""

	attachment, err := s.store.CreateAttachmentWithinQuota(ctx, store.Attachment{
		ID: attachmentID, UserID: userID, ConversationID: conversationID, MessageID: messageID,
		Kind: "generated", MediaType: mediaType, ByteSize: written,
		SHA256: hex.EncodeToString(hasher.Sum(nil)), StoragePath: relativePath,
	}, s.cfg.Lifecycle.MaxStorageBytes)
	if err != nil {
		_ = os.Remove(destination)
		return generatedImage{}, err
	}
	return generatedImage{
		AttachmentID: attachment.ID,
		URL:          "/api/v1/attachments/" + attachment.ID + "/content",
		MediaType:    attachment.MediaType,
		ByteSize:     attachment.ByteSize,
	}, nil
}
