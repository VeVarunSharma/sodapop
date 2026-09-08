package auth

import "errors"

var (
	ErrReauthenticationRequired = errors.New("Sodapop's GitHub credential has expired, been revoked, or changed; sign in again")
	ErrInvalidCredential        = errors.New("Sodapop's saved GitHub credential is invalid; sign out and sign in again")
	ErrAuthorizationDenied      = errors.New("GitHub authorization was denied; start sign-in again to retry")
	ErrDeviceCodeExpired        = errors.New("the GitHub sign-in code expired; start sign-in again")
	ErrOAuthClientRejected      = errors.New("GitHub rejected Sodapop's OAuth configuration; verify the Sodapop-owned client ID, device-flow setting, and requested scopes")
	ErrOAuthFailed              = errors.New("GitHub authorization failed; start sign-in again")
	ErrInvalidResponse          = errors.New("GitHub returned an invalid authentication response; try signing in again")
	ErrNetworkUnavailable       = errors.New("cannot reach GitHub; check the network connection and try again")
	ErrIdentityUnavailable      = errors.New("could not verify the GitHub account identity; try again")
)
