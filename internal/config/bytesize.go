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

// MarshalYAML writes byte sizes using the most readable exact binary unit.
func (s ByteSize) MarshalYAML() (any, error) {
	if s%(1<<30) == 0 && s >= 1<<30 {
		return fmt.Sprintf("%dgb", s>>30), nil
	}
	if s%(1<<20) == 0 && s >= 1<<20 {
		return fmt.Sprintf("%dmb", s>>20), nil
	}
	if s%(1<<10) == 0 && s >= 1<<10 {
		return fmt.Sprintf("%dkb", s>>10), nil
	}
	return fmt.Sprintf("%db", s), nil
}

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
