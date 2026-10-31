package translation

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// flattenMap recursively flattens a nested map[string]any into a dot-notated map[string]string.
func flattenMap(prefix string, input map[string]any, output map[string]string) {
	for k, v := range input {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}

		switch val := v.(type) {
		case string:
			output[key] = val
		case map[string]any:
			flattenMap(key, val, output)
		default:
			output[key] = fmt.Sprintf("%v", val)
		}
	}
}

// loadJSONFile reads and parses a JSON translation file into target map.
func parseJSONTranslations(data []byte, prefix string, target map[string]string) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to parse JSON translations: %w", err)
	}

	flattenMap(prefix, raw, target)
	return nil
}

// LoadFromFS loads translation files from an fs.FS filesystem.
// It detects:
//   - <locale>/<group>.json -> keys prefixed with "<group>."
//   - <locale>.json         -> direct keys for <locale>
func LoadFromFS(fsys fs.FS, rootDir string) (map[string]map[string]string, error) {
	translations := make(map[string]map[string]string)

	ensureLocaleMap := func(loc string) map[string]string {
		if _, ok := translations[loc]; !ok {
			translations[loc] = make(map[string]string)
		}
		return translations[loc]
	}

	cleanRoot := filepath.Clean(rootDir)
	if cleanRoot == "." {
		cleanRoot = ""
	}

	err := fs.WalkDir(fsys, rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		if !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}

		relPath := path
		if cleanRoot != "" {
			var relErr error
			relPath, relErr = filepath.Rel(cleanRoot, path)
			if relErr != nil {
				relPath = path
			}
		}
		// Normalize slashes
		relPath = filepath.ToSlash(relPath)

		parts := strings.Split(relPath, "/")
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("could not read translation file %s: %w", path, err)
		}

		if len(parts) == 1 {
			// e.g. "es.json" -> direct root translations for locale "es"
			locale := strings.TrimSuffix(parts[0], ".json")
			locMap := ensureLocaleMap(locale)
			if err := parseJSONTranslations(data, "", locMap); err != nil {
				return fmt.Errorf("error parsing %s: %w", path, err)
			}
		} else if len(parts) == 2 {
			// e.g. "en/auth.json" -> group="auth", locale="en"
			locale := parts[0]
			group := strings.TrimSuffix(parts[1], ".json")
			locMap := ensureLocaleMap(locale)
			if err := parseJSONTranslations(data, group, locMap); err != nil {
				return fmt.Errorf("error parsing %s: %w", path, err)
			}
		} else if len(parts) > 2 {
			// e.g. "en/sub/auth.json" -> nested groups
			locale := parts[0]
			group := strings.Join(parts[1:len(parts)-1], ".") + "." + strings.TrimSuffix(parts[len(parts)-1], ".json")
			locMap := ensureLocaleMap(locale)
			if err := parseJSONTranslations(data, group, locMap); err != nil {
				return fmt.Errorf("error parsing %s: %w", path, err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return translations, nil
}

// LoadFromDir loads translation files from a local directory path.
func LoadFromDir(dirPath string) (map[string]map[string]string, error) {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return make(map[string]map[string]string), nil
	}
	return LoadFromFS(os.DirFS(dirPath), ".")
}
