package common

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// This is a minimal HS256 JWT implementation built on the standard library.
// The header is fixed, so tokens signed with any other algorithm are rejected.
const jwtHeader = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" // {"alg":"HS256","typ":"JWT"}

// ErrInvalidToken is returned for missing, malformed, forged or expired tokens.
var ErrInvalidToken = errors.New("invalid or expired token")

// Claims is the payload carried inside a token.
type Claims struct {
	Sub   int    `json:"sub"`
	Email string `json:"email"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
}

// IssueToken signs a token for userID that expires after ttl.
func IssueToken(secret []byte, userID int, email string, ttl time.Duration) (string, error) {
	now := time.Now()
	payload, err := json.Marshal(Claims{Sub: userID, Email: email, Iat: now.Unix(), Exp: now.Add(ttl).Unix()})
	if err != nil {
		return "", err
	}
	body := jwtHeader + "." + base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + sign(secret, body), nil
}

// VerifyToken checks the signature and expiry, and returns the claims.
func VerifyToken(secret []byte, token string) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != jwtHeader {
		return c, ErrInvalidToken
	}
	body := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(sign(secret, body)), []byte(parts[2])) {
		return c, ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, ErrInvalidToken
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, ErrInvalidToken
	}
	if time.Now().Unix() >= c.Exp {
		return c, ErrInvalidToken
	}
	return c, nil
}

func sign(secret []byte, data string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// BearerToken returns the token from an "Authorization: Bearer <token>" header.
func BearerToken(r *http.Request) string {
	after, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return after
}

type claimsKey struct{}

// Require rejects requests without a valid token and stores the claims in the request context.
func Require(secret []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := VerifyToken(secret, BearerToken(r))
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
	})
}

// CurrentUser returns the claims set by Require.
func CurrentUser(r *http.Request) (Claims, bool) {
	c, ok := r.Context().Value(claimsKey{}).(Claims)
	return c, ok
}

// Password hashing uses PBKDF2-HMAC-SHA256 from the standard library (Go 1.24+).
const pbkdf2Iterations = 600000

// HashPassword returns "pbkdf2_sha256$<iterations>$<salt>$<hash>".
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", pbkdf2Iterations, enc(salt), enc(key)), nil
}

// CheckPassword compares a plaintext password with a stored hash in constant time.
func CheckPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	salt, errSalt := base64.RawURLEncoding.DecodeString(parts[2])
	want, errHash := base64.RawURLEncoding.DecodeString(parts[3])
	if errSalt != nil || errHash != nil {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return hmac.Equal(key, want)
}
