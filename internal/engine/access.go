package engine

import (
	"errors"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
)

type accessIssueError struct {
	err   error
	issue AccessIssue
}

func (e *accessIssueError) Error() string { return e.err.Error() }
func (e *accessIssueError) Unwrap() error { return e.err }
func (e *accessIssueError) AccessIssue() AccessIssue {
	return e.issue
}

func AccessIssueFor(err error) (AccessIssue, bool) {
	var classified interface{ AccessIssue() AccessIssue }
	if !errors.As(err, &classified) {
		return AccessIssue{}, false
	}
	issue := classified.AccessIssue()
	return issue, issue.Reason != ""
}

func sessionAccessIssue(data *copilot.SessionErrorData) (AccessIssue, bool) {
	if data == nil {
		return AccessIssue{}, false
	}
	issue := AccessIssue{}
	switch strings.ToLower(strings.TrimSpace(data.ErrorType)) {
	case "authentication":
		issue.Reason = AccessAuthentication
	case "authorization":
		issue.Reason = AccessAuthorization
	case "policy":
		issue.Reason = AccessPolicy
	case "quota":
		issue.Reason = AccessQuota
	case "billing":
		issue.Reason = AccessBilling
	case "rate_limit":
		issue.Reason = AccessRateLimit
	case "network":
		issue.Reason = AccessNetwork
	}
	if data.ErrorCode != nil {
		issue.Code = strings.TrimSpace(*data.ErrorCode)
		if issue.Reason == AccessQuota && issue.Code == "billing_not_configured" {
			issue.Reason = AccessBilling
		}
	}
	if data.Remediation != nil {
		issue.Remediation = string(*data.Remediation)
	}
	if data.StatusCode != nil {
		issue.StatusCode = int(*data.StatusCode)
		switch {
		case issue.Reason == "" && issue.StatusCode == 401:
			issue.Reason = AccessAuthentication
		case issue.Reason == "" && issue.StatusCode == 403:
			issue.Reason = AccessAuthorization
		case issue.Reason == "" && issue.StatusCode == 429:
			issue.Reason = AccessRateLimit
		}
	}
	return issue, issue.Reason != ""
}

func withAccessIssue(err error, issue AccessIssue) error {
	if err == nil || issue.Reason == "" {
		return err
	}
	return &accessIssueError{err: err, issue: issue}
}
