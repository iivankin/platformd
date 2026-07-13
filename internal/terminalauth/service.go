package terminalauth

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/passphrase"
)

const (
	tokenPurpose      = "server_terminal"
	tokenLifetime     = 30 * time.Second
	failureDelay      = 2 * time.Second
	cooldownDuration  = 60 * time.Second
	maximumFailures   = 5
	maximumSubjectLen = 512
	maximumTokenLen   = 4096
	keyDomain         = "platformd/server-terminal-token/v1"

	WebSocketProtocol     = "platformd-terminal-v1"
	WebSocketBearerPrefix = "platformd-bearer."
)

var (
	ErrInvalidPassphrase = errors.New("invalid console passphrase")
	ErrCooldown          = errors.New("console passphrase verification is cooling down")
	ErrInvalidToken      = errors.New("invalid server terminal token")
	base64URL            = base64.RawURLEncoding
)

type Config struct {
	Master         cryptobox.MasterKey
	InstallationID string
	Verifier       string
	Now            func() time.Time
	Sleep          func(context.Context, time.Duration) error
}

type IssuedToken struct {
	Value     string
	ExpiresAt time.Time
}

type Service struct {
	verifier string
	key      [sha256.Size]byte
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error

	mutex        sync.Mutex
	failures     int
	cooldownEnds time.Time
}

type claims struct {
	Version   int    `json:"v"`
	Purpose   string `json:"purpose"`
	Subject   string `json:"sub"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

func New(config Config) (*Service, error) {
	if config.InstallationID == "" || config.Verifier == "" {
		return nil, errors.New("server terminal auth configuration is incomplete")
	}
	key, err := hkdf.Key(
		sha256.New, config.Master[:], []byte(config.InstallationID), keyDomain, sha256.Size,
	)
	if err != nil {
		return nil, fmt.Errorf("derive server terminal signing key: %w", err)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	sleep := config.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	service := &Service{verifier: config.Verifier, now: now, sleep: sleep}
	copy(service.key[:], key)
	clear(key)
	return service, nil
}

// Issue consumes passphrase: the caller-provided bytes are cleared before the
// method returns, regardless of verification outcome.
func (service *Service) Issue(ctx context.Context, subject string, value []byte) (IssuedToken, error) {
	defer clear(value)
	if ctx == nil || !validSubject(subject) || len(value) == 0 {
		return IssuedToken{}, ErrInvalidPassphrase
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()

	now := service.now()
	if !service.cooldownEnds.IsZero() {
		if now.Before(service.cooldownEnds) {
			return IssuedToken{}, ErrCooldown
		}
		service.failures = 0
		service.cooldownEnds = time.Time{}
	}
	startedAt := now
	valid, verifyErr := passphrase.Verify(service.verifier, value)
	if verifyErr != nil || !valid {
		service.failures++
		remaining := failureDelay - service.now().Sub(startedAt)
		if remaining > 0 {
			if err := service.sleep(ctx, remaining); err != nil {
				service.startCooldownIfNeeded()
				return IssuedToken{}, err
			}
		}
		service.startCooldownIfNeeded()
		return IssuedToken{}, ErrInvalidPassphrase
	}
	service.failures = 0
	service.cooldownEnds = time.Time{}
	return service.sign(subject, service.now())
}

func (service *Service) Verify(token, subject string) error {
	if !validSubject(subject) || token == "" || len(token) > maximumTokenLen {
		return ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] == "" || parts[2] == "" {
		return ErrInvalidToken
	}
	signature, err := base64URL.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size {
		return ErrInvalidToken
	}
	mac := hmac.New(sha256.New, service.key[:])
	_, _ = io.WriteString(mac, parts[0]+"."+parts[1])
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return ErrInvalidToken
	}
	payload, err := base64URL.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidToken
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var decoded claims
	if err := decoder.Decode(&decoded); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ErrInvalidToken
	}
	now := service.now().Unix()
	if decoded.Version != 1 || decoded.Purpose != tokenPurpose || decoded.Subject != subject ||
		decoded.ExpiresAt-decoded.IssuedAt != int64(tokenLifetime/time.Second) ||
		decoded.IssuedAt > now+1 || now >= decoded.ExpiresAt {
		return ErrInvalidToken
	}
	return nil
}

func (service *Service) VerifyWebSocketRequest(request *http.Request, subject string) error {
	if request == nil {
		return ErrInvalidToken
	}
	protocols := make([]string, 0, 2)
	for _, value := range request.Header.Values("Sec-WebSocket-Protocol") {
		for token := range strings.SplitSeq(value, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				return ErrInvalidToken
			}
			protocols = append(protocols, token)
		}
	}
	if len(protocols) != 2 {
		return ErrInvalidToken
	}
	fixedFound := false
	bearer := ""
	for _, protocol := range protocols {
		switch {
		case protocol == WebSocketProtocol && !fixedFound:
			fixedFound = true
		case strings.HasPrefix(protocol, WebSocketBearerPrefix) && bearer == "":
			bearer = strings.TrimPrefix(protocol, WebSocketBearerPrefix)
		default:
			return ErrInvalidToken
		}
	}
	if !fixedFound || bearer == "" {
		return ErrInvalidToken
	}
	return service.Verify(bearer, subject)
}

func (service *Service) sign(subject string, now time.Time) (IssuedToken, error) {
	issuedAt := now.Unix()
	expiresAt := time.Unix(issuedAt, 0).Add(tokenLifetime)
	payload, err := json.Marshal(claims{
		Version: 1, Purpose: tokenPurpose, Subject: subject,
		IssuedAt: issuedAt, ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		return IssuedToken{}, fmt.Errorf("encode server terminal token: %w", err)
	}
	signingInput := "v1." + base64URL.EncodeToString(payload)
	mac := hmac.New(sha256.New, service.key[:])
	_, _ = io.WriteString(mac, signingInput)
	return IssuedToken{
		Value: signingInput + "." + base64URL.EncodeToString(mac.Sum(nil)), ExpiresAt: expiresAt,
	}, nil
}

func (service *Service) startCooldownIfNeeded() {
	if service.failures >= maximumFailures {
		service.cooldownEnds = service.now().Add(cooldownDuration)
	}
}

func validSubject(subject string) bool {
	return subject != "" && len(subject) <= maximumSubjectLen && strings.TrimSpace(subject) == subject
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
