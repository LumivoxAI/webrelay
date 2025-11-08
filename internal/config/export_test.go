package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ExportSuite struct {
	suite.Suite
}

func (s *ExportSuite) TestExportDefaultYAMLIncludesAllSettingsAndComments() {
	cacheHome := s.T().TempDir()
	stateHome := s.T().TempDir()
	s.T().Setenv("XDG_CACHE_HOME", cacheHome)
	s.T().Setenv("XDG_STATE_HOME", stateHome)

	document, err := ExportDefaultYAML()

	s.Require().NoError(err)
	output := string(document)
	s.Contains(output, "# Incoming HTTP server settings.")
	s.Contains(output, "api_key: ${EXA_API_KEY}")
	s.Contains(output, "api_key: ${BRAVE_API_KEY}")
	s.Contains(output, "api_key: ${TINYFISH_API_KEY}")
	s.Contains(output, "api_key: ${TAVILY_API_KEY}")
	s.Contains(output, "api_key: ${FIRECRAWL_API_KEY}")
	s.Contains(output, "max_age_hours: null")
	s.Contains(output, "path: "+filepath.Join(cacheHome, APPLICATION_NAME, "cache.db"))
	s.Contains(output, "file: "+filepath.Join(stateHome, APPLICATION_NAME, "webrelay.log"))
	for _, field := range []string{"server:", "proxy:", "search:", "content:", "providers:", "cache:", "logging:"} {
		s.Contains(output, field)
	}
}

func (s *ExportSuite) TestExportedConfigurationPreservesCommentsForEveryField() {
	document, err := ExportDefaultYAML()

	s.Require().NoError(err)
	lines := strings.Split(strings.TrimSpace(string(document)), "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") {
			continue
		}
		s.Greater(index, 0, "field %q has no preceding comment", trimmed)
		s.Contains(lines[index-1], "#", "field %q has no preceding comment", trimmed)
	}
}

func (s *ExportSuite) TestExportedConfigurationLoadsAfterProvidingAPIKeys() {
	for name := range map[string]string{
		"EXA_API_KEY":       "exa-key",
		"BRAVE_API_KEY":     "brave-key",
		"TINYFISH_API_KEY":  "tinyfish-key",
		"TAVILY_API_KEY":    "tavily-key",
		"FIRECRAWL_API_KEY": "firecrawl-key",
	} {
		s.T().Setenv(name, "configured")
	}
	s.T().Setenv("XDG_CACHE_HOME", s.T().TempDir())
	s.T().Setenv("XDG_STATE_HOME", s.T().TempDir())
	path := filepath.Join(s.T().TempDir(), "config.yaml")
	document, err := ExportDefaultYAML()
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, document, 0o600))

	cfg, err := Load(path)

	s.Require().NoError(err)
	s.Equal("configured", cfg.Providers.Exa.APIKey)
	s.True(cfg.ProviderActionAvailable("firecrawl", ACTION_SCRAPE))
}

func TestExportSuite(t *testing.T) {
	suite.Run(t, new(ExportSuite))
}
