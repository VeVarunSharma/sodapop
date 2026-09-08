package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	githubDeviceURL       = "https://github.com/login/device/code"
	githubTokenURL        = "https://github.com/login/oauth/access_token"
	githubUserURL         = "https://api.github.com/user"
	githubVerificationURL = "https://github.com/login/device"
	maxResponseBytes      = 64 << 10
	maxSeconds            = int64(math.MaxInt64) / int64(time.Second)
)

var errRequestTimeout = fmt.Errorf("GitHub request timed out: %w", ErrNetworkUnavailable)

type deviceAuthorization struct {
	code      secret
	userCode  string
	expiresAt time.Time
	interval  time.Duration
}

func (deviceAuthorization) Format(w fmt.State, _ rune) {
	_, _ = io.WriteString(w, "auth.deviceAuthorization{[redacted]}")
}

type deviceResponse struct {
	DeviceCode      secret `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int64  `json:"expires_in"`
	Interval        int64  `json:"interval"`
	Error           string `json:"error"`
}

func (deviceResponse) Format(w fmt.State, _ rune) {
	_, _ = io.WriteString(w, "auth.deviceResponse{[redacted]}")
}

type tokenResponse struct {
	AccessToken      secret `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        *int64 `json:"expires_in"`
	RefreshToken     secret `json:"refresh_token"`
	RefreshExpiresIn *int64 `json:"refresh_token_expires_in"`
	Error            string `json:"error"`
	Interval         int64  `json:"interval"`
}

func (tokenResponse) Format(w fmt.State, _ rune) {
	_, _ = io.WriteString(w, "auth.tokenResponse{[redacted]}")
}

type httpReply struct {
	status     int
	retryAfter time.Duration
}

func (s *managerState) authorizeDevice(ctx context.Context) (deviceAuthorization, error) {
	started := s.clock.Now()
	form := url.Values{"client_id": {s.clientID}}
	if len(s.scopes) != 0 {
		form.Set("scope", strings.Join(s.scopes, " "))
	}
	var response deviceResponse
	reply, err := s.request(ctx, githubDeviceURL, form, "", &response)
	if err != nil {
		return deviceAuthorization{}, err
	}
	if response.Error != "" {
		return deviceAuthorization{}, oauthFailure(response.Error)
	}
	if !successful(reply.status) {
		return deviceAuthorization{}, httpFailure("authorization", reply.status, ErrOAuthFailed)
	}
	uri := response.VerificationURI
	if uri == "" {
		uri = response.VerificationURL
	}
	if !validToken(string(response.DeviceCode)) || !validUserCode(response.UserCode) ||
		string(response.DeviceCode) == response.UserCode || uri != githubVerificationURL ||
		response.ExpiresIn <= 0 || response.ExpiresIn > maxSeconds ||
		response.Interval < 0 || response.Interval > maxSeconds {
		return deviceAuthorization{}, ErrInvalidResponse
	}
	interval := time.Duration(response.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second
	}
	expiresAt := started.Add(time.Duration(response.ExpiresIn) * time.Second).UTC()
	if !s.clock.Now().Before(expiresAt) {
		return deviceAuthorization{}, ErrDeviceCodeExpired
	}
	return deviceAuthorization{
		code: response.DeviceCode, userCode: response.UserCode,
		expiresAt: expiresAt, interval: interval,
	}, nil
}

func (s *managerState) pollDevice(ctx context.Context, device deviceAuthorization) (credential, error) {
	interval := device.interval
	form := url.Values{
		"client_id": {s.clientID}, "device_code": {string(device.code)},
		"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	for {
		if err := ctx.Err(); err != nil {
			return credential{}, err
		}
		remaining := device.expiresAt.Sub(s.clock.Now())
		if remaining <= 0 {
			return credential{}, ErrDeviceCodeExpired
		}
		if err := s.clock.Wait(ctx, min(interval, remaining)); err != nil {
			if ctx.Err() != nil {
				return credential{}, ctx.Err()
			}
			return credential{}, ErrOAuthFailed
		}
		if err := ctx.Err(); err != nil {
			return credential{}, err
		}
		remaining = device.expiresAt.Sub(s.clock.Now())
		if remaining <= 0 {
			return credential{}, ErrDeviceCodeExpired
		}
		started := s.clock.Now()
		requestCtx, cancel := context.WithTimeout(ctx, min(remaining, requestTimeout))
		var response tokenResponse
		reply, err := s.request(requestCtx, githubTokenURL, form, "", &response)
		cancel()
		if ctx.Err() != nil {
			return credential{}, ctx.Err()
		}
		if !s.clock.Now().Before(device.expiresAt) {
			return credential{}, ErrDeviceCodeExpired
		}
		if errors.Is(err, errRequestTimeout) || errors.Is(err, context.DeadlineExceeded) ||
			reply.status == http.StatusTooManyRequests || reply.status >= 500 {
			interval = max(backoff(interval), reply.retryAfter)
			continue
		}
		if err != nil {
			return credential{}, err
		}
		if response.Interval < 0 || response.Interval > maxSeconds {
			return credential{}, ErrInvalidResponse
		}
		switch response.Error {
		case "authorization_pending":
			interval = max(interval, time.Duration(response.Interval)*time.Second)
			continue
		case "slow_down":
			interval = max(slowDown(interval), time.Duration(response.Interval)*time.Second)
			continue
		case "":
		default:
			return credential{}, oauthFailure(response.Error)
		}
		if !successful(reply.status) {
			return credential{}, httpFailure("authorization", reply.status, ErrOAuthFailed)
		}
		return parseToken(response, started)
	}
}

func (s *managerState) refresh(ctx context.Context, token secret) (credential, error) {
	started := s.clock.Now()
	form := url.Values{
		"client_id": {s.clientID}, "grant_type": {"refresh_token"},
		"refresh_token": {string(token)},
	}
	var response tokenResponse
	reply, err := s.request(ctx, githubTokenURL, form, "", &response)
	if ctx.Err() != nil {
		return credential{}, ctx.Err()
	}
	if reply.status == http.StatusUnauthorized {
		return credential{}, ErrReauthenticationRequired
	}
	if err != nil {
		return credential{}, err
	}
	if response.Error != "" {
		err := oauthFailure(response.Error)
		if errors.Is(err, ErrAuthorizationDenied) || errors.Is(err, ErrDeviceCodeExpired) {
			err = ErrReauthenticationRequired
		}
		return credential{}, err
	}
	if !successful(reply.status) {
		return credential{}, httpFailure("token renewal", reply.status, ErrOAuthFailed)
	}
	return parseToken(response, started)
}

func parseToken(response tokenResponse, issuedAt time.Time) (credential, error) {
	if !validToken(string(response.AccessToken)) || !strings.EqualFold(response.TokenType, "bearer") ||
		(response.RefreshToken != "" && !validToken(string(response.RefreshToken))) {
		return credential{}, ErrInvalidResponse
	}
	c := credential{accessToken: response.AccessToken, refreshToken: response.RefreshToken}
	if response.ExpiresIn != nil {
		if *response.ExpiresIn < 0 || *response.ExpiresIn > maxSeconds {
			return credential{}, ErrInvalidResponse
		}
		c.expiresAt = issuedAt.Add(time.Duration(*response.ExpiresIn) * time.Second).UTC()
	}
	if response.RefreshExpiresIn != nil {
		if response.RefreshToken == "" || *response.RefreshExpiresIn < 0 ||
			*response.RefreshExpiresIn > maxSeconds {
			return credential{}, ErrInvalidResponse
		}
		c.refreshExpiresAt = issuedAt.Add(time.Duration(*response.RefreshExpiresIn) * time.Second).UTC()
	}
	return c, nil
}

func (s *managerState) identity(ctx context.Context, token secret) (Account, error) {
	var response struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	reply, err := s.request(ctx, githubUserURL, nil, token, &response)
	if ctx.Err() != nil {
		return Account{}, ctx.Err()
	}
	if reply.status == http.StatusUnauthorized {
		return Account{}, ErrReauthenticationRequired
	}
	if reply.status != 0 && reply.status != http.StatusOK {
		return Account{}, httpFailure("account lookup", reply.status, ErrIdentityUnavailable)
	}
	if err != nil {
		return Account{}, err
	}
	if response.ID <= 0 || !validLogin(response.Login) {
		return Account{}, ErrInvalidResponse
	}
	return Account{ID: strconv.FormatInt(response.ID, 10), Login: response.Login}, nil
}

func (s *managerState) request(ctx context.Context, endpoint string, form url.Values, token secret, target any) (httpReply, error) {
	if err := ctx.Err(); err != nil {
		return httpReply{}, err
	}
	method := http.MethodGet
	var body io.Reader
	if form != nil {
		method, body = http.MethodPost, strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return httpReply{}, ErrOAuthFailed
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "sodapop")
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+string(token))
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	response, err := s.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return httpReply{}, ctx.Err()
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return httpReply{}, errRequestTimeout
		}
		return httpReply{}, ErrNetworkUnavailable
	}
	defer response.Body.Close()
	reply := httpReply{
		status:     response.StatusCode,
		retryAfter: retryAfter(response.Header.Get("Retry-After"), s.clock.Now()),
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return reply, ctx.Err()
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return reply, errRequestTimeout
		}
		return reply, ErrNetworkUnavailable
	}
	if len(data) > maxResponseBytes || json.Unmarshal(data, target) != nil {
		return reply, ErrInvalidResponse
	}
	return reply, nil
}

func oauthFailure(code string) error {
	switch code {
	case "access_denied":
		return ErrAuthorizationDenied
	case "expired_token", "token_expired":
		return ErrDeviceCodeExpired
	case "incorrect_client_credentials", "invalid_client", "device_flow_disabled", "invalid_scope", "unsupported_grant_type":
		return ErrOAuthClientRejected
	case "bad_refresh_token", "invalid_grant", "invalid_token", "incorrect_device_code":
		return ErrReauthenticationRequired
	default:
		return ErrOAuthFailed
	}
}

func httpFailure(operation string, status int, category error) error {
	return fmt.Errorf("GitHub %s returned HTTP %d: %w", operation, status, category)
}

func successful(status int) bool { return status >= 200 && status < 300 }

func slowDown(interval time.Duration) time.Duration {
	if interval > time.Duration(math.MaxInt64)-5*time.Second {
		return time.Duration(math.MaxInt64)
	}
	return interval + 5*time.Second
}

func backoff(interval time.Duration) time.Duration {
	if interval > time.Duration(math.MaxInt64)/2 {
		return time.Duration(math.MaxInt64)
	}
	return interval * 2
}

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= maxSeconds {
		return time.Duration(seconds) * time.Second
	}
	if until, err := http.ParseTime(value); err == nil && until.After(now) {
		return until.Sub(now)
	}
	return 0
}

func validToken(value string) bool {
	if len(value) == 0 || len(value) > 1024 {
		return false
	}
	padding := false
	for _, ch := range value {
		if ch == '=' {
			padding = true
			continue
		}
		if padding || !(asciiAlphanumeric(ch) || strings.ContainsRune("-._~+/", ch)) {
			return false
		}
	}
	return value[0] != '='
}

func validUserCode(value string) bool {
	if len(value) != 9 || value[4] != '-' {
		return false
	}
	for i, ch := range value {
		if i != 4 && !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return false
		}
	}
	return true
}

func validLogin(value string) bool {
	if len(value) == 0 || len(value) > 100 {
		return false
	}
	for _, ch := range value {
		if !asciiAlphanumeric(ch) && ch != '-' && ch != '_' {
			return false
		}
	}
	return true
}

func validClientID(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, ch := range value {
		if !asciiAlphanumeric(ch) && ch != '.' && ch != '_' && ch != '-' {
			return false
		}
	}
	return true
}

func validScopes(scopes []string) bool {
	for _, scope := range scopes {
		if scope == "" {
			return false
		}
		for _, ch := range scope {
			if ch < 0x21 || ch > 0x7e || ch == '"' || ch == '\\' {
				return false
			}
		}
	}
	return true
}

func asciiAlphanumeric(ch rune) bool {
	return ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9'
}
