package value

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var codePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func NormalizeCode(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if len(code) < 2 || len(code) > 63 || !codePattern.MatchString(code) {
		return "", errors.New("code must contain 2-63 lowercase letters, digits, or hyphens")
	}
	return code, nil
}

func NormalizeName(raw string, maxLength int) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("name is required")
	}
	if len([]rune(name)) > maxLength {
		return "", fmt.Errorf("name must not exceed %d characters", maxLength)
	}
	return name, nil
}

func NormalizeHTTPBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) > 2048 {
		return "", errors.New("base URL must not exceed 2048 bytes")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", errors.New("base URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("base URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("base URL must include a host")
	}
	if parsed.User != nil {
		return "", errors.New("base URL must not include user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("base URL must not include a query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}
