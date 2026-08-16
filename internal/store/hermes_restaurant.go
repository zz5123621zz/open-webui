package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
)

const hermesRestaurantTokenPrefix = "hbr_"

type HermesRestaurantCredential struct {
	ID              string `json:"id"`
	UserID          string `json:"userId"`
	Username        string `json:"username"`
	Label           string `json:"label"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
	Status          string `json:"status"`
	CreatedAt       int64  `json:"createdAt"`
	LastUsedAt      int64  `json:"lastUsedAt,omitempty"`
	RevokedAt       int64  `json:"revokedAt,omitempty"`
	User            User   `json:"-"`
}

type HermesRestaurantAudio struct {
	ID           string
	CredentialID string
	UserID       string
	RequestKey   string
	PartIndex    int
	FileName     string
	StoragePath  string
	ByteSize     int64
	SHA256       string
	CreatedAt    int64
	ExpiresAt    int64
}

func (s *Store) CreateHermesRestaurantCredential(
	ctx context.Context,
	username string,
	label string,
	model string,
	reasoningEffort string,
) (HermesRestaurantCredential, string, error) {
	username = strings.TrimSpace(username)
	label = strings.TrimSpace(label)
	model = strings.TrimSpace(model)
	reasoningEffort = strings.ToLower(strings.TrimSpace(reasoningEffort))
	if !validHermesRestaurantCLIText(label, 100) {
		return HermesRestaurantCredential{}, "", errors.New(
			"credential label must contain 1 to 100 characters",
		)
	}
	if !validHermesRestaurantCLIText(model, 200) {
		return HermesRestaurantCredential{}, "", errors.New(
			"credential model is invalid",
		)
	}
	switch reasoningEffort {
	case "auto", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return HermesRestaurantCredential{}, "", errors.New(
			"credential reasoning effort is invalid",
		)
	}

	var user User
	var initialWorkbench, effectiveWorkbench string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, display_name,
		       COALESCE(preferred_model, ''), role, status, created_at, updated_at,
		       initial_workbench,
		       COALESCE(workbench_preference, initial_workbench)
		FROM users
		WHERE username = ? COLLATE NOCASE AND status = 'active'
	`, username).Scan(
		&user.ID,
		&user.Username,
		&user.PasswordHash,
		&user.DisplayName,
		&user.PreferredModel,
		&user.Role,
		&user.Status,
		&user.CreatedAt,
		&user.UpdatedAt,
		&initialWorkbench,
		&effectiveWorkbench,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return HermesRestaurantCredential{}, "", ErrNotFound
	}
	if err != nil {
		return HermesRestaurantCredential{}, "", err
	}
	if initialWorkbench != guidance.WorkbenchRestaurant ||
		effectiveWorkbench != guidance.WorkbenchRestaurant {
		return HermesRestaurantCredential{}, "", errors.New(
			"user must have the restaurant workbench assigned and active",
		)
	}

	id, err := ids.New()
	if err != nil {
		return HermesRestaurantCredential{}, "", err
	}
	secret, err := ids.NewSecret()
	if err != nil {
		return HermesRestaurantCredential{}, "", err
	}
	rawToken := hermesRestaurantTokenPrefix + secret
	tokenHash := sha256.Sum256([]byte(rawToken))
	now := time.Now().UnixMilli()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO hermes_restaurant_credentials(
			id, user_id, label, token_hash, model, reasoning_effort,
			status, created_at
		)
		VALUES(?, ?, ?, ?, ?, ?, 'active', ?)
	`, id, user.ID, label, tokenHash[:], model, reasoningEffort, now)
	if err != nil {
		return HermesRestaurantCredential{}, "", fmt.Errorf(
			"create hermes restaurant credential: %w",
			err,
		)
	}
	return HermesRestaurantCredential{
		ID: id, UserID: user.ID, Username: user.Username,
		Label: label, Model: model, ReasoningEffort: reasoningEffort,
		Status: "active", CreatedAt: now, User: user,
	}, rawToken, nil
}

func (s *Store) AuthenticateHermesRestaurantCredential(
	ctx context.Context,
	rawToken string,
) (HermesRestaurantCredential, error) {
	rawToken = strings.TrimSpace(rawToken)
	if !strings.HasPrefix(rawToken, hermesRestaurantTokenPrefix) ||
		len(rawToken) != len(hermesRestaurantTokenPrefix)+43 {
		return HermesRestaurantCredential{}, ErrNotFound
	}
	tokenHash := sha256.Sum256([]byte(rawToken))
	var credential HermesRestaurantCredential
	var user User
	var initialWorkbench, effectiveWorkbench string
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.user_id, u.username, c.label, c.model,
		       c.reasoning_effort, c.status, c.created_at,
		       COALESCE(c.last_used_at, 0), COALESCE(c.revoked_at, 0),
		       u.id, u.username, u.password_hash, u.display_name,
		       COALESCE(u.preferred_model, ''), u.role, u.status,
		       u.created_at, u.updated_at, u.initial_workbench,
		       COALESCE(u.workbench_preference, u.initial_workbench)
		FROM hermes_restaurant_credentials c
		JOIN users u ON u.id = c.user_id
		WHERE c.token_hash = ? AND c.status = 'active' AND u.status = 'active'
	`, tokenHash[:]).Scan(
		&credential.ID,
		&credential.UserID,
		&credential.Username,
		&credential.Label,
		&credential.Model,
		&credential.ReasoningEffort,
		&credential.Status,
		&credential.CreatedAt,
		&credential.LastUsedAt,
		&credential.RevokedAt,
		&user.ID,
		&user.Username,
		&user.PasswordHash,
		&user.DisplayName,
		&user.PreferredModel,
		&user.Role,
		&user.Status,
		&user.CreatedAt,
		&user.UpdatedAt,
		&initialWorkbench,
		&effectiveWorkbench,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return HermesRestaurantCredential{}, ErrNotFound
	}
	if err != nil {
		return HermesRestaurantCredential{}, fmt.Errorf(
			"authenticate hermes restaurant credential: %w",
			err,
		)
	}
	if initialWorkbench != guidance.WorkbenchRestaurant ||
		effectiveWorkbench != guidance.WorkbenchRestaurant {
		return HermesRestaurantCredential{}, ErrNotFound
	}
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE hermes_restaurant_credentials
		SET last_used_at = ?
		WHERE id = ? AND status = 'active'
	`, now, credential.ID); err != nil {
		return HermesRestaurantCredential{}, fmt.Errorf(
			"touch hermes restaurant credential: %w",
			err,
		)
	}
	credential.LastUsedAt = now
	credential.User = user
	return credential, nil
}

func (s *Store) ListHermesRestaurantCredentials(
	ctx context.Context,
) ([]HermesRestaurantCredential, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.user_id, u.username, c.label, c.model,
		       c.reasoning_effort, c.status, c.created_at,
		       COALESCE(c.last_used_at, 0), COALESCE(c.revoked_at, 0)
		FROM hermes_restaurant_credentials c
		JOIN users u ON u.id = c.user_id
		ORDER BY c.created_at DESC, c.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	credentials := make([]HermesRestaurantCredential, 0)
	for rows.Next() {
		var credential HermesRestaurantCredential
		if err := rows.Scan(
			&credential.ID,
			&credential.UserID,
			&credential.Username,
			&credential.Label,
			&credential.Model,
			&credential.ReasoningEffort,
			&credential.Status,
			&credential.CreatedAt,
			&credential.LastUsedAt,
			&credential.RevokedAt,
		); err != nil {
			return nil, err
		}
		credentials = append(credentials, credential)
	}
	return credentials, rows.Err()
}

func (s *Store) RevokeHermesRestaurantCredential(
	ctx context.Context,
	id string,
) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrNotFound
	}
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `
		UPDATE hermes_restaurant_credentials
		SET status = 'revoked', revoked_at = ?
		WHERE id = ? AND status = 'active'
	`, now, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) HermesRestaurantConversation(
	ctx context.Context,
	credential HermesRestaurantCredential,
	externalSessionID string,
	title string,
	maxActive int,
) (Conversation, error) {
	externalSessionID = strings.TrimSpace(externalSessionID)
	if externalSessionID == "" || len(externalSessionID) > 128 {
		return Conversation{}, errors.New("external session id is invalid")
	}
	sessionHash := sha256.Sum256([]byte(externalSessionID))
	var conversationID string
	err := s.db.QueryRowContext(ctx, `
		SELECT s.conversation_id
		FROM hermes_restaurant_sessions s
		JOIN conversations c ON c.id = s.conversation_id
		WHERE s.credential_id = ? AND s.external_session_hash = ?
		  AND c.user_id = ? AND c.archived_at IS NULL
	`, credential.ID, sessionHash[:], credential.UserID).Scan(&conversationID)
	switch {
	case err == nil:
		return s.ConversationByID(
			ctx,
			credential.UserID,
			conversationID,
		)
	case !errors.Is(err, sql.ErrNoRows):
		return Conversation{}, err
	}

	conversation, err := s.CreateConversationWithLimit(
		ctx,
		credential.UserID,
		title,
		credential.Model,
		credential.ReasoningEffort,
		maxActive,
	)
	if err != nil {
		return Conversation{}, err
	}
	now := time.Now().UnixMilli()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO hermes_restaurant_sessions(
			credential_id, external_session_hash, conversation_id,
			created_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(credential_id, external_session_hash) DO UPDATE SET
			conversation_id = excluded.conversation_id,
			updated_at = excluded.updated_at
	`, credential.ID, sessionHash[:], conversation.ID, now, now)
	if err != nil {
		return Conversation{}, fmt.Errorf(
			"map hermes restaurant session: %w",
			err,
		)
	}
	return conversation, nil
}

func (s *Store) HermesResponseByClientRequestID(
	ctx context.Context,
	userID string,
	clientRequestID string,
) (Message, error) {
	var assistantID string
	err := s.db.QueryRowContext(ctx, `
		SELECT a.id
		FROM messages u
		JOIN messages a ON a.parent_message_id = u.id
		WHERE u.user_id = ? AND u.client_request_id = ?
		  AND u.role = 'user' AND a.role = 'assistant'
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT 1
	`, userID, clientRequestID).Scan(&assistantID)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	return s.MessageByID(ctx, userID, assistantID)
}

func (s *Store) HermesClientRequestPrefixExists(
	ctx context.Context,
	userID string,
	clientRequestPrefix string,
) (bool, error) {
	clientRequestPrefix = strings.TrimSpace(clientRequestPrefix)
	if clientRequestPrefix == "" {
		return false, errors.New("client request prefix is empty")
	}
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM messages
			WHERE user_id = ? AND role = 'user'
			  AND substr(client_request_id, 1, length(?)) = ?
		)
	`, userID, clientRequestPrefix, clientRequestPrefix).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

func (s *Store) CreateHermesRestaurantAudio(
	ctx context.Context,
	audio HermesRestaurantAudio,
) error {
	return s.CreateHermesRestaurantAudioBatch(
		ctx,
		[]HermesRestaurantAudio{audio},
	)
}

func (s *Store) CreateHermesRestaurantAudioBatch(
	ctx context.Context,
	audioFiles []HermesRestaurantAudio,
) error {
	if len(audioFiles) == 0 {
		return errors.New("hermes restaurant audio batch is empty")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, audio := range audioFiles {
		decodedDigest, digestErr := hex.DecodeString(
			strings.TrimSpace(audio.SHA256),
		)
		cleanStoragePath := filepath.Clean(
			strings.TrimSpace(audio.StoragePath),
		)
		audioRoot := "hermes-restaurant-audio"
		audioRelative, relativeErr := filepath.Rel(
			audioRoot,
			cleanStoragePath,
		)
		if strings.TrimSpace(audio.ID) == "" ||
			strings.TrimSpace(audio.CredentialID) == "" ||
			strings.TrimSpace(audio.UserID) == "" ||
			strings.TrimSpace(audio.RequestKey) == "" ||
			audio.PartIndex < 0 ||
			strings.TrimSpace(audio.FileName) == "" ||
			filepath.Base(audio.FileName) != audio.FileName ||
			!strings.HasSuffix(strings.ToLower(audio.FileName), ".wav") ||
			cleanStoragePath == "." ||
			filepath.IsAbs(cleanStoragePath) ||
			cleanStoragePath == ".." ||
			strings.HasPrefix(
				cleanStoragePath,
				".."+string(filepath.Separator),
			) ||
			relativeErr != nil ||
			audioRelative == "." ||
			audioRelative == ".." ||
			strings.HasPrefix(
				audioRelative,
				".."+string(filepath.Separator),
			) ||
			audio.ByteSize <= 44 ||
			digestErr != nil ||
			len(decodedDigest) != sha256.Size ||
			audio.ExpiresAt <= audio.CreatedAt {
			return errors.New("hermes restaurant audio record is invalid")
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO hermes_restaurant_audio(
				id, credential_id, user_id, request_key, part_index,
				file_name, storage_path, byte_size, sha256, created_at, expires_at
			)
			SELECT ?, c.id, c.user_id, ?, ?, ?, ?, ?, ?, ?, ?
			FROM hermes_restaurant_credentials c
			WHERE c.id = ? AND c.user_id = ?
		`, audio.ID, audio.RequestKey, audio.PartIndex,
			audio.FileName, cleanStoragePath, audio.ByteSize,
			strings.ToLower(audio.SHA256), audio.CreatedAt, audio.ExpiresAt,
			audio.CredentialID, audio.UserID)
		if err != nil {
			return fmt.Errorf("create hermes restaurant audio: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return errors.New(
				"hermes restaurant audio credential ownership is invalid",
			)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) HermesRestaurantAudioByID(
	ctx context.Context,
	credentialID string,
	userID string,
	id string,
	now int64,
) (HermesRestaurantAudio, error) {
	var audio HermesRestaurantAudio
	err := s.db.QueryRowContext(ctx, `
		SELECT id, credential_id, user_id, request_key, part_index,
		       file_name, storage_path, byte_size, sha256, created_at, expires_at
		FROM hermes_restaurant_audio
		WHERE id = ? AND credential_id = ? AND user_id = ? AND expires_at > ?
	`, id, credentialID, userID, now).Scan(
		&audio.ID,
		&audio.CredentialID,
		&audio.UserID,
		&audio.RequestKey,
		&audio.PartIndex,
		&audio.FileName,
		&audio.StoragePath,
		&audio.ByteSize,
		&audio.SHA256,
		&audio.CreatedAt,
		&audio.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return HermesRestaurantAudio{}, ErrNotFound
	}
	if err != nil {
		return HermesRestaurantAudio{}, err
	}
	return audio, nil
}

func (s *Store) HermesRestaurantAudioForRequest(
	ctx context.Context,
	credentialID string,
	userID string,
	requestKey string,
	now int64,
) ([]HermesRestaurantAudio, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, credential_id, user_id, request_key, part_index,
		       file_name, storage_path, byte_size, sha256, created_at, expires_at
		FROM hermes_restaurant_audio
		WHERE credential_id = ? AND user_id = ? AND request_key = ?
		  AND expires_at > ?
		ORDER BY part_index
	`, credentialID, userID, requestKey, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	audioFiles := make([]HermesRestaurantAudio, 0)
	for rows.Next() {
		var audio HermesRestaurantAudio
		if err := rows.Scan(
			&audio.ID,
			&audio.CredentialID,
			&audio.UserID,
			&audio.RequestKey,
			&audio.PartIndex,
			&audio.FileName,
			&audio.StoragePath,
			&audio.ByteSize,
			&audio.SHA256,
			&audio.CreatedAt,
			&audio.ExpiresAt,
		); err != nil {
			return nil, err
		}
		audioFiles = append(audioFiles, audio)
	}
	return audioFiles, rows.Err()
}

func (s *Store) DeleteHermesRestaurantAudio(
	ctx context.Context,
	ids []string,
) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		var path string
		err := tx.QueryRowContext(ctx, `
			SELECT storage_path FROM hermes_restaurant_audio WHERE id = ?
		`, id).Scan(&path)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM hermes_restaurant_audio WHERE id = ?
		`, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return paths, nil
}

func validHermesRestaurantCLIText(value string, maximumRunes int) bool {
	if value == "" || !utf8.ValidString(value) ||
		utf8.RuneCountInString(value) > maximumRunes {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func (s *Store) PurgeExpiredHermesRestaurantAudio(
	ctx context.Context,
	now int64,
) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT storage_path
		FROM hermes_restaurant_audio
		WHERE expires_at <= ?
	`, now)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM hermes_restaurant_audio WHERE expires_at <= ?
	`, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return paths, nil
}
