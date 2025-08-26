package config

import (
	"fmt"
	"time"
)

func validateProviderOrder(field string, providers []string, allowed map[string]bool) error {
	if len(providers) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	seen := make(map[string]bool, len(providers))
	for _, provider := range providers {
		if !allowed[provider] {
			return fmt.Errorf("%s contains unknown provider %q", field, provider)
		}
		if seen[provider] {
			return fmt.Errorf("%s contains duplicate provider %q", field, provider)
		}
		seen[provider] = true
	}
	return nil
}

func validatePositive(field string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
