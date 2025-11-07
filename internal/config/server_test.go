package config

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type ServerConfigSuite struct {
	suite.Suite
}

func (s *ServerConfigSuite) TestWithListenOverridesReplacesOnlySpecifiedAddressParts() {
	host := "0.0.0.0"
	port := uint16(9090)

	updated, err := DefaultServerConfig().WithListenOverrides(&host, &port)

	s.Require().NoError(err)
	s.Equal("0.0.0.0:9090", updated.Listen)
}

func (s *ServerConfigSuite) TestWithListenOverridesPreservesConfiguredIPv6Host() {
	server := DefaultServerConfig()
	server.Listen = "[::1]:8090"
	port := uint16(9090)

	updated, err := server.WithListenOverrides(nil, &port)

	s.Require().NoError(err)
	s.Equal("[::1]:9090", updated.Listen)
}

func (s *ServerConfigSuite) TestWithListenOverridesRejectsPortZero() {
	port := uint16(0)

	_, err := DefaultServerConfig().WithListenOverrides(nil, &port)

	s.EqualError(err, "server.listen port must be between 1 and 65535")
}

func TestServerConfigSuite(t *testing.T) {
	suite.Run(t, new(ServerConfigSuite))
}
