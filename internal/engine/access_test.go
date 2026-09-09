package engine

import (
	"errors"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestSessionAccessIssuePreservesStructuredRecoveryMetadata(t *testing.T) {
	remediation := copilot.RemediationActionSignIn
	status := int32(401)
	issue, ok := sessionAccessIssue(&copilot.SessionErrorData{
		ErrorType: "authentication", Remediation: &remediation, StatusCode: &status,
	})
	if !ok || issue.Reason != AccessAuthentication || issue.Remediation != "sign_in" || issue.StatusCode != 401 {
		t.Fatalf("access issue = %+v, %t", issue, ok)
	}
	wrapped := withAccessIssue(errors.New("authentication failed"), issue)
	if got, found := AccessIssueFor(wrapped); !found || got != issue {
		t.Fatalf("wrapped access issue = %+v, %t", got, found)
	}
	if !errors.Is(wrapped, errors.Unwrap(wrapped)) {
		t.Fatal("structured access error did not preserve its cause")
	}
}

func TestSessionAccessIssueDistinguishesBillingAndRateLimits(t *testing.T) {
	billingCode := "billing_not_configured"
	billing, ok := sessionAccessIssue(&copilot.SessionErrorData{
		ErrorType: "quota", ErrorCode: &billingCode,
	})
	if !ok || billing.Reason != AccessBilling {
		t.Fatalf("billing issue = %+v, %t", billing, ok)
	}
	status := int32(429)
	rateLimit, ok := sessionAccessIssue(&copilot.SessionErrorData{StatusCode: &status})
	if !ok || rateLimit.Reason != AccessRateLimit {
		t.Fatalf("rate-limit issue = %+v, %t", rateLimit, ok)
	}
}

func TestSessionAccessIssueClassifiesKnownRuntimeCategories(t *testing.T) {
	tests := map[string]AccessReason{
		"authentication": AccessAuthentication,
		"authorization":  AccessAuthorization,
		"policy":         AccessPolicy,
		"quota":          AccessQuota,
		"billing":        AccessBilling,
		"rate_limit":     AccessRateLimit,
		"network":        AccessNetwork,
	}
	for errorType, want := range tests {
		issue, ok := sessionAccessIssue(&copilot.SessionErrorData{ErrorType: errorType})
		if !ok || issue.Reason != want {
			t.Fatalf("%s issue = %+v, %t", errorType, issue, ok)
		}
	}
	for status, want := range map[int32]AccessReason{
		401: AccessAuthentication,
		403: AccessAuthorization,
		429: AccessRateLimit,
	} {
		issue, ok := sessionAccessIssue(&copilot.SessionErrorData{StatusCode: &status})
		if !ok || issue.Reason != want {
			t.Fatalf("status %d issue = %+v, %t", status, issue, ok)
		}
	}
	if issue, ok := sessionAccessIssue(nil); ok || issue.Reason != "" {
		t.Fatalf("nil error data was classified: %+v", issue)
	}
	if issue, ok := sessionAccessIssue(&copilot.SessionErrorData{ErrorType: "query"}); ok || issue.Reason != "" {
		t.Fatalf("ordinary query error was classified: %+v", issue)
	}
	if got, ok := AccessIssueFor(errors.New("ordinary error")); ok || got.Reason != "" {
		t.Fatalf("ordinary error was classified: %+v", got)
	}
	if got := withAccessIssue(nil, AccessIssue{Reason: AccessUnknown}); got != nil {
		t.Fatalf("nil error was wrapped: %v", got)
	}
}
