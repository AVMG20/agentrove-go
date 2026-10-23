package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	AudienceSession       = "agentrove:session"
	AudienceMCP           = "agentrove:mcp"
	AudienceArtifactAgent = "agentrove:artifacts"
	AudienceArtifactView  = "agentrove:artifact-view"
)

// Keys derives every secret from SECRET_KEY: one HMAC key per token audience
// (so a scoped token can never pass as a session) and an AES-GCM key for
// secrets at rest (GitHub PATs, webhook secrets).
type Keys struct {
	secret string
	aead   cipher.AEAD
}

func NewKeys(secret string) (*Keys, error) {
	sum := sha256.Sum256([]byte(secret + ":encryption"))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Keys{secret: secret, aead: aead}, nil
}

func (k *Keys) hmacKey(audience string) []byte {
	sum := sha256.Sum256([]byte(k.secret + ":" + audience))
	return sum[:]
}

type Claims struct {
	jwt.RegisteredClaims
	// Chat binds an agent token to the chat whose session holds it, so the
	// agent can't claim another chat of the user.
	Chat string `json:"chat,omitempty"`
}

// IssueToken signs a token for subject with the given audience and lifetime.
func (k *Keys) IssueToken(subject, audience string, ttl time.Duration) (string, error) {
	return k.IssueChatToken(subject, audience, "", ttl)
}

// IssueChatToken is IssueToken bound to chatID (see Claims.Chat).
func (k *Keys) IssueChatToken(subject, audience, chatID string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{Chat: chatID, RegisteredClaims: jwt.RegisteredClaims{
		Subject:   subject,
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(k.hmacKey(audience))
}

// VerifyToken returns the subject of a valid token for audience.
func (k *Keys) VerifyToken(token, audience string) (string, error) {
	c, err := k.VerifyClaims(token, audience)
	if err != nil {
		return "", err
	}
	return c.Subject, nil
}

// VerifyClaims returns the claims of a valid token for audience.
func (k *Keys) VerifyClaims(token, audience string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return k.hmacKey(audience), nil
	}, jwt.WithAudience(audience), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	c, ok := parsed.Claims.(*Claims)
	if !ok || c.Subject == "" {
		return nil, errors.New("invalid claims")
	}
	return c, nil
}

func (k *Keys) Encrypt(plain string) (string, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := k.aead.Seal(nonce, nonce, []byte(plain), nil)
	return "enc:" + base64.RawURLEncoding.EncodeToString(out), nil
}

// Decrypt returns plaintext; values without the enc: prefix pass through.
func (k *Keys) Decrypt(value string) (string, error) {
	raw, ok := strings.CutPrefix(value, "enc:")
	if !ok {
		return value, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	ns := k.aead.NonceSize()
	if len(data) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := k.aead.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
