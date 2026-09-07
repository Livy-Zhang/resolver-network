package database
// checks that the provided database URL is valid and points to a test database
import (
	"fmt"
	"net/url"
	"strings"
)

func ValidateTestDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse TEST_DATABASE_URL: %w", err)
	}
	name := strings.TrimPrefix(parsed.Path, "/")
	if name == "" || !strings.HasSuffix(strings.ToLower(name), "_test") {
		return fmt.Errorf("TEST_DATABASE_URL database name must end in _test")
	}
	return nil
}
