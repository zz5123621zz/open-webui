package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
	_ "golang.org/x/image/webp"
)

const maxUploadBytes = 12 * 1024 * 1024

func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !s.uploads.acquire(session.User.ID) {
		writeError(w, http.StatusTooManyRequests, "upload_in_progress", "Only one upload can run at a time.")
		return
	}
	defer s.uploads.release(session.User.ID)

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "Expected a multipart image upload.")
		return
	}
	var filePart *multipart.Part
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_upload", "Could not read the upload.")
			return
		}
		if part.FormName() == "file" && part.FileName() != "" {
			filePart = part
			break
		}
		_ = part.Close()
	}
	if filePart == nil {
		writeError(w, http.StatusBadRequest, "file_required", "An image file is required.")
		return
	}
	defer filePart.Close()

	attachmentID, err := ids.New()
	if err != nil {
		s.internalError(w, "generate attachment id", err)
		return
	}
	tmpDir := filepath.Join(s.cfg.DataDir, "tmp", "uploads")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		s.internalError(w, "create upload temp directory", err)
		return
	}
	tempFile, err := os.CreateTemp(tmpDir, attachmentID+".*")
	if err != nil {
		s.internalError(w, "create upload temp file", err)
		return
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()
	if err := tempFile.Chmod(0o600); err != nil {
		s.internalError(w, "secure upload temp file", err)
		return
	}

	hasher := sha256.New()
	limited := io.LimitReader(filePart, maxUploadBytes+1)
	written, err := io.Copy(io.MultiWriter(tempFile, hasher), limited)
	if err != nil {
		s.internalError(w, "write upload", err)
		return
	}
	if written == 0 {
		writeError(w, http.StatusBadRequest, "empty_file", "The uploaded file is empty.")
		return
	}
	if written > maxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "upload_too_large", "Each image must be 12 MiB or smaller.")
		return
	}
	if err := tempFile.Sync(); err != nil {
		s.internalError(w, "sync upload", err)
		return
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		s.internalError(w, "inspect upload", err)
		return
	}
	header := make([]byte, 512)
	headerLength, _ := io.ReadFull(tempFile, header)
	mediaType := http.DetectContentType(header[:headerLength])
	extension, ok := imageExtension(mediaType)
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_image", "Only PNG, JPEG and WebP images are supported.")
		return
	}
	if err := validateImageFile(tempFile, mediaType); err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_image", "The uploaded file is not a valid image.")
		return
	}

	now := time.Now().UTC()
	relativePath := filepath.Join("uploads", session.User.ID, now.Format("2006"), now.Format("01"), attachmentID+extension)
	destination := filepath.Join(s.cfg.DataDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		s.internalError(w, "create upload directory", err)
		return
	}
	if err := tempFile.Close(); err != nil {
		s.internalError(w, "close upload", err)
		return
	}
	if err := os.Rename(tempPath, destination); err != nil {
		s.internalError(w, "commit upload file", err)
		return
	}
	tempPath = ""

	attachment, err := s.store.CreateAttachmentWithinQuota(r.Context(), store.Attachment{
		ID: attachmentID, UserID: session.User.ID, Kind: "upload",
		OriginalName: safeOriginalName(filePart.FileName()), MediaType: mediaType,
		ByteSize: written, SHA256: hex.EncodeToString(hasher.Sum(nil)), StoragePath: relativePath,
	}, s.cfg.Lifecycle.MaxStorageBytes)
	if err != nil {
		_ = os.Remove(destination)
		if errors.Is(err, store.ErrStorageQuota) {
			writeError(
				w,
				http.StatusInsufficientStorage,
				"storage_quota_exceeded",
				"Your active workspace has reached its 3 GB storage allowance. Retain or delete a conversation before uploading more images.",
			)
			return
		}
		s.internalError(w, "save upload record", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"attachment": attachment,
		"url":        "/api/v1/attachments/" + attachment.ID + "/content",
	})
}

func (s *Server) attachmentContent(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	var attachment store.Attachment
	var err error
	if session.User.Role == "admin" {
		attachment, err = s.store.AttachmentByIDAny(r.Context(), r.PathValue("id"))
	} else {
		attachment, err = s.store.AttachmentByID(r.Context(), session.User.ID, r.PathValue("id"))
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, "lookup attachment content", err)
		return
	}
	fullPath := filepath.Join(s.cfg.DataDir, filepath.Clean(attachment.StoragePath))
	dataRoot := filepath.Clean(s.cfg.DataDir) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(fullPath), dataRoot) {
		writeError(w, http.StatusInternalServerError, "invalid_storage_path", "Attachment path is invalid.")
		return
	}
	file, err := os.Open(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, "open attachment", err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", attachment.MediaType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", attachment.ByteSize))
	w.Header().Set("Content-Disposition", `inline; filename="image`+filepath.Ext(attachment.StoragePath)+`"`)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.UnixMilli(attachment.CreatedAt), file)
}

func (s *Server) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	attachment, err := s.store.DeleteAttachment(r.Context(), session.User.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Attachment not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, "attachment_in_use", "This attachment is already used by a message.")
		return
	}
	if err := os.Remove(filepath.Join(s.cfg.DataDir, attachment.StoragePath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Warn("delete attachment file failed", "error", err, "attachment_id", attachment.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func imageExtension(mediaType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/png":
		return ".png", true
	case "image/jpeg":
		return ".jpg", true
	case "image/webp":
		return ".webp", true
	default:
		return "", false
	}
}

func validateImageFile(file *os.File, expectedMediaType string) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return err
	}
	expectedFormats := map[string]string{
		"image/png": "png", "image/jpeg": "jpeg", "image/webp": "webp",
	}
	if format != expectedFormats[expectedMediaType] || config.Width < 1 || config.Height < 1 ||
		config.Width > 32768 || config.Height > 32768 {
		return errors.New("image metadata is invalid")
	}
	return nil
}

func safeOriginalName(value string) string {
	value = strings.TrimSpace(filepath.Base(value))
	value = strings.ReplaceAll(value, "\x00", "")
	runes := []rune(value)
	if len(runes) > 180 {
		value = string(runes[:180])
	}
	return value
}
