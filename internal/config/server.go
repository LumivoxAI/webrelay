package config

import (
	"fmt"
	"net"
	"strconv"
	"time"
)

// ServerConfig controls the incoming HTTP server.
type ServerConfig struct {
	Listen              string   `yaml:"listen"`
	RequestTimeout      Duration `yaml:"request_timeout"`
	ShutdownTimeout     Duration `yaml:"shutdown_timeout"`
	MaxRequestBodyBytes ByteSize `yaml:"max_request_body_bytes"`
}

// DefaultServerConfig returns safe loopback server settings.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Listen:              "127.0.0.1:8090",
		RequestTimeout:      Duration(35 * time.Second),
		ShutdownTimeout:     Duration(10 * time.Second),
		MaxRequestBodyBytes: ByteSize(1 << 20),
	}
}

// Validate checks server address, durations, and request size.
func (c ServerConfig) Validate() error {
	if err := validatePositive("server.request_timeout", c.RequestTimeout.Std()); err != nil {
		return err
	}
	if err := validatePositive("server.shutdown_timeout", c.ShutdownTimeout.Std()); err != nil {
		return err
	}
	if c.MaxRequestBodyBytes <= 0 {
		return fmt.Errorf("server.max_request_body_bytes must be positive")
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil || port == "" {
		return fmt.Errorf("server.listen must be a host:port address")
	}
	if parsedPort, _ := strconv.Atoi(port); parsedPort < 1 || parsedPort > 65535 {
		return fmt.Errorf("server.listen port must be between 1 and 65535")
	}
	return nil
}

// WithListenOverrides returns the server configuration with optional CLI address parts applied.
func (c ServerConfig) WithListenOverrides(host *string, port *uint16) (ServerConfig, error) {
	configuredHost, configuredPort, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("split server.listen: %w", err)
	}
	if host != nil {
		configuredHost = *host
	}
	if port != nil {
		configuredPort = strconv.Itoa(int(*port))
	}
	c.Listen = net.JoinHostPort(configuredHost, configuredPort)
	if err := c.Validate(); err != nil {
		return ServerConfig{}, err
	}
	return c, nil
}
