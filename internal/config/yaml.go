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

// MarshalYAML writes durations in the same portable syntax accepted by UnmarshalYAML.
func (d Duration) MarshalYAML() (any, error) {
	value := d.Std()
	if value%time.Hour == 0 {
		return fmt.Sprintf("%dh", value/time.Hour), nil
	}
	if value%time.Minute == 0 {
		return fmt.Sprintf("%dm", value/time.Minute), nil
	}
	if value%time.Second == 0 {
		return fmt.Sprintf("%ds", value/time.Second), nil
	}
	return value.String(), nil
}

func yamlDecoder(reader io.Reader) *yaml.Decoder {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	return decoder
}
