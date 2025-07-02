package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ConfigSuite struct {
	suite.Suite
}

func (s *ConfigSuite) TestLoadUsesExplicitAPIKeyAndDefaultDatabasePath() {
	s.T().Setenv("XDG_DATA_HOME", s.T().TempDir())
	path := filepath.Join(s.T().TempDir(), "config.json")
	err := os.WriteFile(path, []byte(`{
		"providers": {
			"tavily": {"enabled": true, "api_key": "key"}
		}
	}`), 0o600)
	s.Require().NoError(err)

	cfg, err := Load(path)
	s.Require().NoError(err)
	s.Require().Equal("key", cfg.Providers["tavily"].APIKey)

	wantDB := filepath.Join(os.Getenv("XDG_DATA_HOME"), "webrelay", "db.sqlite")
	s.Require().Equal(wantDB, cfg.Database.Path)
}

func (s *ConfigSuite) TestLoadDatabasePathOverride() {
	path := filepath.Join(s.T().TempDir(), "config.json")
	err := os.WriteFile(path, []byte(`{"database":{"path":":memory:"}}`), 0o600)
	s.Require().NoError(err)

	cfg, err := Load(path)
	s.Require().NoError(err)
	s.Require().Equal(":memory:", cfg.Database.Path)
}

func TestConfigSuite(t *testing.T) {
	suite.Run(t, new(ConfigSuite))
}
