package compatibility

import (
	"bytes"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

func LoadManifest(path string) (Manifest, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read compatibility catalog: %w", err)
	}
	var manifest Manifest
	if err := yaml.NewDecoder(bytes.NewReader(payload), yaml.DisallowUnknownField()).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode compatibility catalog: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("validate compatibility catalog: %w", err)
	}
	return manifest, nil
}
