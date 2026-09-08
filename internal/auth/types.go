package auth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotSignedIn              = errors.New("sign in to GitHub to connect to Copilot")
	ErrNoClientID               = errors.New("Sodapop OAuth client is not configured; set SODAPOP_GITHUB_CLIENT_ID to your registered device-flow client ID")
	ErrSecureStorageUnavailable = errors.New("secure credential storage is unavailable; choose session-only sign-in or cancel")
)

type Account struct {
	ID          string
	Login       string
	SessionOnly bool
}

type DeviceCode struct {
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
}

type Service interface {
	Current(context.Context) (Account, error)
	Token(context.Context) (string, error)
	Login(context.Context, bool, func(DeviceCode)) (Account, error)
	SignOut(context.Context) error
}
