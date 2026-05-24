package cli

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func writeYAMLFile(path string, value interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
