package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	// Argon2id parameters follow the OWASP-recommended minimum (19 MiB,
	// 2 iterations, 1 lane). Combined with hashGate this keeps password
	// hashing comfortably within the container memory budget.
	argonMemory      = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32

	// maxConcurrentHashes bounds how many Argon2id computations may run at
	// once. Each one transiently allocates its parameter's worth of memory
	// (up to 64 MiB for legacy hashes), so an ungated flood of concurrent
	// logins could exhaust the container memory limit and crash the
	// process. Password hashing is rare and fast, so a small cap is free.
	maxConcurrentHashes = 2
)

// hashGate serializes Argon2id computations down to maxConcurrentHashes so a
// burst of concurrent password verifications cannot allocate unbounded memory.
var hashGate = make(chan struct{}, maxConcurrentHashes)

// deriveKey runs Argon2id under hashGate. All password hashing must go through
// it so the concurrency bound cannot be bypassed.
func deriveKey(password, salt []byte, iterations, memory uint32, parallelism uint8, keyLength uint32) []byte {
	hashGate <- struct{}{}
	defer func() { <-hashGate }()
	return argon2.IDKey(password, salt, iterations, memory, parallelism, keyLength)
}

func HashPassword(password string) (string, error) {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 6 {
		return "", errors.New("password must be at least 6 characters")
	}
	if len(password) > 1024 {
		return "", errors.New("password is too long")
	}

	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := deriveKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)

	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("invalid password hash encoding")
	}

	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, errors.New("invalid password hash parameters")
	}
	// Cap accepted memory at 128 MiB. Legacy hashes at 64 MiB still verify,
	// but a corrupted or hostile hash cannot force an allocation larger than
	// the container can afford.
	if memory < 8*1024 || memory > 128*1024 || iterations < 1 || iterations > 10 || parallelism < 1 || parallelism > 16 {
		return false, errors.New("unsafe password hash parameters")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, errors.New("invalid password hash salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false, errors.New("invalid password hash value")
	}

	actual := deriveKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func NeedsRehash(encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return true
	}
	params := strings.Split(parts[3], ",")
	values := make(map[string]int, len(params))
	for _, param := range params {
		pair := strings.SplitN(param, "=", 2)
		if len(pair) != 2 {
			return true
		}
		value, err := strconv.Atoi(pair[1])
		if err != nil {
			return true
		}
		values[pair[0]] = value
	}
	return values["m"] != argonMemory || values["t"] != argonIterations || values["p"] != argonParallelism
}
