package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var byteSizePattern = regexp.MustCompile(`^(\d+)(b|kb|mb|gb)?$`)

// ByteSize unmarshals byte sizes such as 1mb from YAML.
type ByteSize int64

func (s *ByteSize) UnmarshalYAML(value *yaml.Node) error {
	text := strings.ToLower(strings.TrimSpace(value.Value))
	matches := byteSizePattern.FindStringSubmatch(text)
	if matches == nil {
		return fmt.Errorf("invalid byte size %q", value.Value)
	}
	amount, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid byte size %q: %w", value.Value, err)
	}
	multipliers := map[string]int64{"": 1, "b": 1, "kb": 1 << 10, "mb": 1 << 20, "gb": 1 << 30}
	if amount > (1<<63-1)/multipliers[matches[2]] {
		return fmt.Errorf("byte size %q is too large", value.Value)
	}
	*s = ByteSize(amount * multipliers[matches[2]])
	return nil
}
