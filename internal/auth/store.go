package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	credentialService = "sodapop"
	credentialAccount = "github.com/oauth-user"
	credentialVersion = 1
	// Leave room for go-keyring's base64 encoding and macOS security command.
	maxCredentialBytes = 2048
)

// Store is the secure credential backend. Missing entries must return
// keyring.ErrNotFound. Implementations must not persist values in plaintext.
// Manager serializes calls to its Store.
type Store interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

type systemStore struct{}

func (systemStore) Get(service, account string) (string, error) {
	if !secureStoreSupported(runtime.GOOS) {
		return "", keyring.ErrUnsupportedPlatform
	}
	return keyring.Get(service, account)
}

func (systemStore) Set(service, account, value string) error {
	if !secureStoreSupported(runtime.GOOS) {
		return keyring.ErrUnsupportedPlatform
	}
	return keyring.Set(service, account, value)
}

func (systemStore) Delete(service, account string) error {
	if !secureStoreSupported(runtime.GOOS) {
		return keyring.ErrUnsupportedPlatform
	}
	return keyring.Delete(service, account)
}

func secureStoreSupported(goos string) bool {
	return goos == "darwin" || goos == "linux" || goos == "windows"
}

type secret string

func (secret) Format(w fmt.State, _ rune) { _, _ = io.WriteString(w, "[redacted]") }
func (secret) MarshalJSON() ([]byte, error) {
	return []byte(`"[redacted]"`), nil
}

type credential struct {
	account          Account
	accessToken      secret
	refreshToken     secret
	expiresAt        time.Time
	refreshExpiresAt time.Time
}

func (credential) Format(w fmt.State, _ rune) {
	_, _ = io.WriteString(w, "auth.credential{[redacted]}")
}

func (c credential) expired(now time.Time) bool {
	return !c.expiresAt.IsZero() && !now.Before(c.expiresAt)
}

func (c credential) canRefresh(now time.Time) bool {
	return c.refreshToken != "" && (c.refreshExpiresAt.IsZero() || now.Before(c.refreshExpiresAt))
}

// This is the only serialization containing raw credentials. It is used only
// at the secure-store boundary, never as an application model or UI message.
type persistenceRecord struct {
	Version          int       `json:"version"`
	Host             string    `json:"host"`
	ClientID         string    `json:"client_id"`
	AccountID        string    `json:"account_id"`
	Login            string    `json:"login"`
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func (persistenceRecord) Format(w fmt.State, _ rune) {
	_, _ = io.WriteString(w, "auth.persistenceRecord{[redacted]}")
}

func encodeCredential(c credential, clientID string) (string, error) {
	if clientID == "" {
		return "", ErrNoClientID
	}
	if c.account.SessionOnly || !validClientID(clientID) {
		return "", ErrInvalidCredential
	}
	record := persistenceRecord{
		Version: credentialVersion, Host: "github.com", ClientID: clientID,
		AccountID: c.account.ID, Login: c.account.Login,
		AccessToken: string(c.accessToken), RefreshToken: string(c.refreshToken),
		ExpiresAt: c.expiresAt, RefreshExpiresAt: c.refreshExpiresAt,
	}
	data, err := json.Marshal(record)
	if err != nil || len(data) > maxCredentialBytes {
		return "", ErrInvalidCredential
	}
	return string(data), nil
}

func decodeCredential(data, clientID string) (*credential, error) {
	if len(data) == 0 || len(data) > maxCredentialBytes {
		return nil, ErrInvalidCredential
	}
	var record persistenceRecord
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, ErrInvalidCredential
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrInvalidCredential
	}
	id, err := strconv.ParseInt(record.AccountID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != record.AccountID ||
		record.Version != credentialVersion || record.Host != "github.com" ||
		!validClientID(record.ClientID) || !validLogin(record.Login) ||
		!validToken(record.AccessToken) ||
		(record.RefreshToken != "" && !validToken(record.RefreshToken)) ||
		(record.RefreshToken == "" && !record.RefreshExpiresAt.IsZero()) {
		return nil, ErrInvalidCredential
	}
	if clientID == "" {
		return nil, ErrNoClientID
	}
	if record.ClientID != clientID {
		return nil, ErrReauthenticationRequired
	}
	return &credential{
		account:     Account{ID: record.AccountID, Login: record.Login},
		accessToken: secret(record.AccessToken), refreshToken: secret(record.RefreshToken),
		expiresAt: record.ExpiresAt, refreshExpiresAt: record.RefreshExpiresAt,
	}, nil
}

func storageFailure(operation string) error {
	return fmt.Errorf("could not %s Sodapop's saved credential: %w", operation, ErrSecureStorageUnavailable)
}
