// Package provider coordinates outbound providers without exposing upstream details.
package provider

import (
	"context"
	"fmt"
	"time"
)

// Name identifies a configured upstream provider.
type Name string

const (
	Exa         Name = "exa"
	Brave       Name = "brave"
	MarkdownNew Name = "markdown_new"
)

// State describes whether a provider can receive an outbound request.
type State string

const (
	StateAvailable     State = "available"
	StateCooldown      State = "cooldown"
	StateMisconfigured State = "misconfigured"
	StateDisabled      State = "disabled"
)

// Reason is a safe, stable category for an upstream failure.
type Reason string

const (
	ReasonMisconfigured Reason = "provider_misconfigured"
	ReasonUnauthorized  Reason = "provider_unauthorized"
	ReasonForbidden     Reason = "provider_forbidden"
	ReasonQuota         Reason = "quota_exhausted"
	ReasonRateLimited   Reason = "rate_limited"
	ReasonTimeout       Reason = "upstream_timeout"
	ReasonTemporary     Reason = "temporary_failure"
	ReasonUnavailable   Reason = "content_unavailable"
)

// Failure tells the router how an adapter failure may be retried or routed.
// Cause is retained for local logging only and is never returned to clients.
type Failure struct {
	Reason    Reason
	Retryable bool
	Cooldown  time.Duration
	Cause     error
}

func (f *Failure) Error() string {
	if f.Cause != nil {
		return fmt.Sprintf("%s: %v", f.Reason, f.Cause)
	}
	return string(f.Reason)
}

func (f *Failure) Unwrap() error {
	return f.Cause
}

// Attempt records one provider exhausted or skipped by routing.
type Attempt struct {
	Provider Name
	Reason   Reason
}

// Result identifies the provider that completed a routed operation.
type Result struct {
	Provider Name
	Attempts []Attempt
}

// Operation is a single adapter call. A nil error, including an empty search
// response, is always treated as a successful operation.
type Operation func(context.Context, Name) error

// RouteError contains safe diagnostics when no provider completed an operation.
type RouteError struct {
	Attempts []Attempt
}

func (e *RouteError) Error() string {
	return "all providers failed"
}

// Code returns the sole failure category, or all_providers_failed when routing
// exhausted a path with more than one cause.
func (e *RouteError) Code() Reason {
	if len(e.Attempts) == 0 {
		return ReasonTemporary
	}
	code := e.Attempts[0].Reason
	for _, attempt := range e.Attempts[1:] {
		if attempt.Reason != code {
			return "all_providers_failed"
		}
	}
	return code
}
