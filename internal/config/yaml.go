package config

import (
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration unmarshals YAML duration strings into time.Duration values.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the standard library duration representation.
func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

func yamlDecoder(reader io.Reader) *yaml.Decoder {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	return decoder
}
