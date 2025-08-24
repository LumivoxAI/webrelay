package config

import "fmt"

// LoggingConfig controls structured logging output.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// DefaultLoggingConfig returns production-safe logging settings.
func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{
		Level:  "info",
		Format: "json",
	}
}

// Validate checks supported logging levels and encodings.
func (c LoggingConfig) Validate() error {
	if !oneOf(c.Level, "debug", "info", "warn", "error") {
		return fmt.Errorf("logging.level is invalid")
	}
	if !oneOf(c.Format, "json", "console") {
		return fmt.Errorf("logging.format is invalid")
	}
	return nil
}
