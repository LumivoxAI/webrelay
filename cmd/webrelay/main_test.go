package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/suite"
)

type MainSuite struct {
	suite.Suite
}

func (s *MainSuite) TestCLIParsesAddressOverrides() {
	var parsed cli
	parser, err := kong.New(&parsed, kong.Name("webrelay"))
	s.Require().NoError(err)

	_, err = parser.Parse([]string{"--host", "0.0.0.0", "--port", "9090"})

	s.Require().NoError(err)
	s.Require().NotNil(parsed.Host)
	s.Require().NotNil(parsed.Port)
	s.Equal("0.0.0.0", *parsed.Host)
	s.Equal(uint16(9090), *parsed.Port)
}

func (s *MainSuite) TestCLIParsesExportConfigOutput() {
	path := filepath.Join(s.T().TempDir(), "config.yaml")
	var parsed cli
	parser, err := kong.New(&parsed, kong.Name("webrelay"))
	s.Require().NoError(err)

	_, err = parser.Parse([]string{"export", "config", "--output", path})

	s.Require().NoError(err)
	s.Equal(path, parsed.Export.Config.Output)
}

func (s *MainSuite) TestCLIRejectsLegacyConfigExportOrder() {
	var parsed cli
	parser, err := kong.New(&parsed, kong.Name("webrelay"))
	s.Require().NoError(err)

	_, err = parser.Parse([]string{"config", "export"})

	s.Error(err)
}

func (s *MainSuite) TestExportDefaultConfigCreatesPrivateFile() {
	path := filepath.Join(s.T().TempDir(), "config.yaml")

	err := exportDefaultConfig(path)

	s.Require().NoError(err)
	info, err := os.Stat(path)
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o600), info.Mode().Perm())
	content, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Contains(string(content), "api_key: ${EXA_API_KEY}")
}

func (s *MainSuite) TestExportDefaultConfigDoesNotOverwriteExistingFile() {
	path := filepath.Join(s.T().TempDir(), "config.yaml")
	s.Require().NoError(os.WriteFile(path, []byte("existing"), 0o600))

	err := exportDefaultConfig(path)

	s.ErrorIs(err, os.ErrExist)
	content, readErr := os.ReadFile(path)
	s.Require().NoError(readErr)
	s.Equal("existing", string(content))
}

func TestMainSuite(t *testing.T) {
	suite.Run(t, new(MainSuite))
}
