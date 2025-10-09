package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type ConfigSuite struct {
	suite.Suite
	path string
}

func (s *ConfigSuite) SetupTest() {
	s.path = filepath.Join(s.T().TempDir(), "config.yaml")
	s.T().Setenv("XDG_STATE_HOME", s.T().TempDir())
}

func (s *ConfigSuite) writeConfig(body string) {
	s.Require().NoError(os.WriteFile(s.path, []byte(body), 0o600))
}

func (s *ConfigSuite) TestLoadUsesEnvironmentAndDefaults() {
	s.T().Setenv("EXA_API_KEY", "exa-secret")
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	s.writeConfig("providers:\n  exa:\n    api_key: ${EXA_API_KEY}\n  brave:\n    api_key: ${BRAVE_API_KEY}\ncache:\n  path: ':memory:'\n")

	cfg, err := Load(s.path)

	s.Require().NoError(err)
	s.Equal("exa-secret", cfg.Providers.Exa.APIKey)
	s.Equal("brave-secret", cfg.Providers.Brave.APIKey)
	s.Equal("127.0.0.1:8090", cfg.Server.Listen)
	s.Equal(20, cfg.Search.MaxLimit)
	s.Empty(cfg.Proxy.URL)
	s.True(cfg.ProviderAvailable("exa"))
	s.True(cfg.ProviderAvailable("markdown_new"))
}

func (s *ConfigSuite) TestLoadUsesXDGConfigPath() {
	configHome := s.T().TempDir()
	s.T().Setenv("XDG_CONFIG_HOME", configHome)
	s.T().Setenv("EXA_API_KEY", "exa-secret")
	path := filepath.Join(configHome, APPLICATION_NAME, "config.yaml")
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	s.Require().NoError(os.WriteFile(path, []byte("providers:\n  exa:\n    api_key: ${EXA_API_KEY}\ncache:\n  path: ':memory:'\n"), 0o600))

	cfg, err := Load("")

	s.Require().NoError(err)
	s.Equal("exa-secret", cfg.Providers.Exa.APIKey)
}

func (s *ConfigSuite) TestLoadUsesCommonProxy() {
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	s.writeConfig("proxy:\n  url: socks5://user:proxy-password@proxy.example:1080\nproviders:\n  brave:\n    api_key: ${BRAVE_API_KEY}\ncache:\n  path: ':memory:'\n")

	cfg, err := Load(s.path)

	s.Require().NoError(err)
	s.Equal("socks5://user:proxy-password@proxy.example:1080", cfg.Proxy.URL)
}

func (s *ConfigSuite) TestRejectsInvalidCommonProxy() {
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	s.writeConfig("proxy:\n  url: ftp://user:proxy-password@proxy.example\nproviders:\n  brave:\n    api_key: ${BRAVE_API_KEY}\ncache:\n  path: ':memory:'\n")

	_, err := Load(s.path)

	s.EqualError(err, "proxy.url is invalid")
	s.NotContains(err.Error(), "proxy-password")
}

func (s *ConfigSuite) TestRejectsLegacyProviderProxy() {
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	s.writeConfig("providers:\n  brave:\n    api_key: ${BRAVE_API_KEY}\n    proxy: https://proxy.example\ncache:\n  path: ':memory:'\n")

	_, err := Load(s.path)

	s.Error(err)
	s.Contains(err.Error(), "field proxy not found")
}

func (s *ConfigSuite) TestRejectsInvalidRetryBackoff() {
	brave := DefaultBraveConfig()
	brave.APIKey = "brave-secret"
	brave.InitialBackoff = Duration(2 * time.Second)
	brave.MaxBackoff = Duration(time.Second)

	err := brave.Validate()

	s.EqualError(err, "max_backoff must be no less than initial_backoff")
}

func (s *ConfigSuite) TestInvalidExaKeyUsesBraveAndMarkdownNew() {
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	s.writeConfig("providers:\n  exa:\n    api_key: ''\n  brave:\n    api_key: ${BRAVE_API_KEY}\ncache:\n  path: ':memory:'\n")

	cfg, err := Load(s.path)

	s.Require().NoError(err)
	s.Equal("api_key is required", cfg.ProviderIssue("exa"))
	s.True(cfg.ProviderAvailable("brave"))
	s.True(cfg.ProviderAvailable("markdown_new"))
}

func (s *ConfigSuite) TestRejectsInvalidCriticalSettings() {
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	s.writeConfig("server:\n  listen: invalid\nproviders:\n  brave:\n    api_key: ${BRAVE_API_KEY}\ncache:\n  path: ':memory:'\n")

	_, err := Load(s.path)

	s.EqualError(err, "server.listen must be a host:port address")
}

func (s *ConfigSuite) TestRejectsUnknownFields() {
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	s.writeConfig("providers:\n  brave:\n    api_key: ${BRAVE_API_KEY}\ncache:\n  path: ':memory:'\nunknown: true\n")

	_, err := Load(s.path)

	s.Error(err)
	s.Contains(err.Error(), "field unknown not found")
}

func (s *ConfigSuite) TestRejectsDuplicateProviderOrder() {
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	s.writeConfig("search:\n  providers: [brave, brave]\nproviders:\n  brave:\n    api_key: ${BRAVE_API_KEY}\ncache:\n  path: ':memory:'\n")

	_, err := Load(s.path)

	s.EqualError(err, `search.providers contains duplicate provider "brave"`)
}

func (s *ConfigSuite) TestCreatesConfiguredCacheDirectory() {
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	cachePath := filepath.Join(s.T().TempDir(), "new", "cache.db")
	s.writeConfig("providers:\n  brave:\n    api_key: ${BRAVE_API_KEY}\ncache:\n  path: " + cachePath + "\n")

	_, err := Load(s.path)

	s.Require().NoError(err)
	info, err := os.Stat(cachePath)
	s.Require().NoError(err)
	s.False(info.IsDir())
}

func (s *ConfigSuite) TestDefaultLoggingUsesXDGStatePath() {
	stateHome := s.T().TempDir()
	s.T().Setenv("XDG_STATE_HOME", stateHome)

	logging := DefaultLoggingConfig()

	s.Equal(filepath.Join(stateHome, APPLICATION_NAME, "webrelay.log"), logging.File)
	s.False(logging.Console)
	s.True(logging.Rotation.Compress)
}

func (s *ConfigSuite) TestLoggingRequiresAtLeastOneOutput() {
	logging := LoggingConfig{
		Level:    "info",
		Format:   "json",
		Rotation: DefaultRotationConfig(),
	}

	err := logging.Validate()

	s.EqualError(err, "logging requires file or console output")
}

func (s *ConfigSuite) TestLoadAllowsConsoleWithoutFile() {
	s.T().Setenv("BRAVE_API_KEY", "brave-secret")
	s.writeConfig("providers:\n  brave:\n    api_key: ${BRAVE_API_KEY}\ncache:\n  path: ':memory:'\nlogging:\n  file: ''\n  console: true\n")

	cfg, err := Load(s.path)

	s.Require().NoError(err)
	s.Empty(cfg.Logging.File)
	s.True(cfg.Logging.Console)
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(ConfigSuite))
}
