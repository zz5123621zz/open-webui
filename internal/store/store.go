package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrUsernameExists      = errors.New("username already exists")
	ErrDuplicateRequest    = errors.New("duplicate request")
	ErrNotLatestMessage    = errors.New("message is not the latest response")
	ErrConversationChanged = errors.New("conversation changed while preparing context")
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	DisplayName    string `json:"displayName"`
	PreferredModel string `json:"preferredModel,omitempty"`
	Status         string `json:"-"`
	PasswordHash   string `json:"-"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

type Session struct {
	ID        string
	User      User
	ExpiresAt time.Time
}

func Open(ctx context.Context, databasePath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	dsn := "file:" + url.PathEscape(databasePath) +
		"?_txlock=immediate" +
		"&_pragma=journal_mode%28WAL%29" +
		"&_pragma=foreign_keys%281%29" +
		"&_pragma=busy_timeout%285000%29" +
		"&_pragma=synchronous%28NORMAL%29" +
		"&_pragma=temp_store%28MEMORY%29" +
		"&_pragma=cache_size%28-4096%29" +
		"&_pragma=mmap_size%280%29"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(5 * time.Minute)

	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	for _, filePath := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		if err := os.Chmod(filePath, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = db.Close()
			return nil, fmt.Errorf("secure sqlite file %s: %w", filepath.Base(filePath), err)
		}
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) Ready(ctx context.Context) error {
	connection, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, "ROLLBACK"); err != nil {
		return err
	}
	var result string
	if err := connection.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("sqlite quick check returned %q", result)
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
		return fmt.Errorf("apply schema v1: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, applied_at)
		VALUES(1, ?)
		ON CONFLICT(version) DO NOTHING
	`, time.Now().Unix()); err != nil {
		return fmt.Errorf("record schema v1: %w", err)
	}
	var hasV2 int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 2`).Scan(&hasV2); err != nil {
		return fmt.Errorf("inspect schema v2: %w", err)
	}
	if hasV2 == 0 {
		if _, err := tx.ExecContext(ctx, schemaV2); err != nil {
			return fmt.Errorf("apply schema v2: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO schema_migrations(version, applied_at) VALUES(2, ?)
		`, time.Now().Unix()); err != nil {
			return fmt.Errorf("record schema v2: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func (s *Store) CreateUser(ctx context.Context, username, displayName, passwordHash string) (User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if username == "" || len(username) > 64 {
		return User{}, errors.New("username must contain 1 to 64 characters")
	}
	if displayName == "" || len(displayName) > 100 {
		return User{}, errors.New("display name must contain 1 to 100 characters")
	}
	id, err := ids.New()
	if err != nil {
		return User{}, err
	}
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users(id, username, password_hash, display_name, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, 'active', ?, ?)
	`, id, username, passwordHash, displayName, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrUsernameExists
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return User{
		ID: id, Username: username, DisplayName: displayName, Status: "active",
		PasswordHash: passwordHash, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, display_name, COALESCE(preferred_model, ''), status, created_at, updated_at
		FROM users
		WHERE username = ? COLLATE NOCASE
	`, strings.TrimSpace(username)).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
		&user.PreferredModel, &user.Status, &user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup user: %w", err)
	}
	return user, nil
}

func (s *Store) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, time.Now().Unix(), userID)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return tx.Commit()
}

func (s *Store) SetUserStatusByUsername(ctx context.Context, username, status string) error {
	if status != "active" && status != "disabled" {
		return errors.New("invalid user status")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET status = ?, updated_at = ?
		WHERE username = ? COLLATE NOCASE
	`, status, time.Now().Unix(), strings.TrimSpace(username))
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrNotFound
	}
	if status == "disabled" {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM sessions
			WHERE user_id = (SELECT id FROM users WHERE username = ? COLLATE NOCASE)
		`, strings.TrimSpace(username)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateSession(ctx context.Context, userID, rawToken, userAgent string, ttl time.Duration) (Session, error) {
	id, err := ids.New()
	if err != nil {
		return Session{}, err
	}
	tokenHash := sha256.Sum256([]byte(rawToken))
	var userAgentHash []byte
	if userAgent != "" {
		sum := sha256.Sum256([]byte(userAgent))
		userAgentHash = sum[:]
	}
	now := time.Now()
	expiresAt := now.Add(ttl)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions(id, user_id, token_hash, expires_at, last_seen_at, created_at, user_agent_hash)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, id, userID, tokenHash[:], expiresAt.Unix(), now.Unix(), now.Unix(), userAgentHash)
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return Session{}, err
	}
	return Session{ID: id, User: user, ExpiresAt: expiresAt}, nil
}

func (s *Store) SessionByToken(ctx context.Context, rawToken string) (Session, error) {
	tokenHash := sha256.Sum256([]byte(rawToken))
	var session Session
	var expiresUnix int64
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.expires_at,
		       u.id, u.username, u.password_hash, u.display_name, COALESCE(u.preferred_model, ''), u.status, u.created_at, u.updated_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ? AND u.status = 'active'
	`, tokenHash[:], time.Now().Unix()).Scan(
		&session.ID, &expiresUnix,
		&session.User.ID, &session.User.Username, &session.User.PasswordHash,
		&session.User.DisplayName, &session.User.PreferredModel, &session.User.Status,
		&session.User.CreatedAt, &session.User.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("lookup session: %w", err)
	}
	session.ExpiresAt = time.Unix(expiresUnix, 0)
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ? AND last_seen_at < ?`,
		time.Now().Unix(), session.ID, time.Now().Add(-5*time.Minute).Unix())
	return session, nil
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, display_name, COALESCE(preferred_model, ''), status, created_at, updated_at
		FROM users WHERE id = ?
	`, id).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
		&user.PreferredModel, &user.Status, &user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup user by id: %w", err)
	}
	return user, nil
}

func (s *Store) DeleteSession(ctx context.Context, sessionID, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND user_id = ?`, sessionID, userID)
	return err
}

func (s *Store) DeleteSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}
