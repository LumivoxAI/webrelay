package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// RotationConfig bounds retained log files.
type RotationConfig struct {
	MaxSizeMB  int  `yaml:"max_size_mb"`
	MaxBackups int  `yaml:"max_backups"`
	MaxAgeDays int  `yaml:"max_age_days"`
	Compress   bool `yaml:"compress"`
}

// DefaultRotationConfig returns bounded file retention settings.
func DefaultRotationConfig() RotationConfig {
	return RotationConfig{
		MaxSizeMB:  100,
		MaxBackups: 5,
		MaxAgeDays: 30,
		Compress:   true,
	}
}

// Validate checks file rotation limits.
func (c RotationConfig) Validate() error {
	if c.MaxSizeMB < 1 {
		return fmt.Errorf("logging.rotation.max_size_mb must be positive")
	}
	if c.MaxBackups < 1 {
		return fmt.Errorf("logging.rotation.max_backups must be positive")
	}
	if c.MaxAgeDays < 1 {
		return fmt.Errorf("logging.rotation.max_age_days must be positive")
	}
	return nil
}

// LoggingConfig controls structured logging output.
type LoggingConfig struct {
	Level    string         `yaml:"level"`
	Format   string         `yaml:"format"`
	File     string         `yaml:"file"`
	Console  bool           `yaml:"console"`
	Rotation RotationConfig `yaml:"rotation"`
}

// DefaultLoggingConfig returns file logging with bounded retention.
func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{
		Level:    "info",
		Format:   "json",
		File:     DefaultLogPath(),
		Console:  false,
		Rotation: DefaultRotationConfig(),
	}
}

// Validate checks supported logging settings and configured outputs.
func (c LoggingConfig) Validate() error {
	if !oneOf(c.Level, "debug", "info", "warn", "error") {
		return fmt.Errorf("logging.level is invalid")
	}
	if !oneOf(c.Format, "json", "console") {
		return fmt.Errorf("logging.format is invalid")
	}
	if c.File == "" && !c.Console {
		return fmt.Errorf("logging requires file or console output")
	}
	if c.File != "" {
		if err := ensureLogFile(c.File); err != nil {
			return err
		}
	}
	return c.Rotation.Validate()
}

// DefaultLogPath returns the XDG state path for the rotating log file.
func DefaultLogPath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".local", "state", APPLICATION_NAME, "webrelay.log")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, APPLICATION_NAME, "webrelay.log")
}

func ensureLogFile(path string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close log file: %w", err)
	}
	return nil
}
