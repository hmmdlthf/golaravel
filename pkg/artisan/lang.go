package artisan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func makeLang(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing locale code. Usage: artisan make:lang <locale> (e.g. es, fr, ar, de)")
	}

	locale := strings.ToLower(strings.TrimSpace(args[0]))
	dir := filepath.Join("lang", locale)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	files := map[string]string{
		filepath.Join(dir, "auth.json"): `{
  "failed": "These credentials do not match our records.",
  "password": "The provided password is incorrect.",
  "throttle": "Too many login attempts. Please try again in :seconds seconds."
}
`,
		filepath.Join(dir, "validation.json"): `{
  "required": "The :attribute field is required.",
  "email": "The :attribute field must be a valid email address.",
  "min": {
    "numeric": "The :attribute field must be at least :min.",
    "string": "The :attribute field must be at least :min characters.",
    "array": "The :attribute field must have at least :min items.",
    "file": "The :attribute field must be at least :min kilobytes."
  },
  "max": {
    "numeric": "The :attribute field must not be greater than :max.",
    "string": "The :attribute field must not be greater than :max characters.",
    "array": "The :attribute field must not have more than :max items.",
    "file": "The :attribute field must not be greater than :max kilobytes."
  },
  "unique": "The :attribute has already been taken."
}
`,
		filepath.Join(dir, "pagination.json"): `{
  "previous": "&laquo; Previous",
  "next": "Next &raquo;"
}
`,
		filepath.Join(dir, "messages.json"): `{
  "welcome": "Welcome, :name!",
  "items": "{0} No items|{1} 1 item|[2,*] :count items"
}
`,
		filepath.Join("lang", locale+".json"): `{
  "Welcome back!": "Welcome back!",
  "Dashboard": "Dashboard",
  "Profile": "Profile",
  "Settings": "Settings",
  "Logout": "Logout"
}
`,
	}

	for path, content := range files {
		// Only create if not exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to write %s: %w", path, err)
			}
		}
	}

	fmt.Printf("\033[32mINFO\033[0m Language files for [%s] created successfully in lang/%s/ and lang/%s.json\n", locale, locale, locale)
	return nil
}
