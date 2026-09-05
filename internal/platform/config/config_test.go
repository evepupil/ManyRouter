package config

import "testing"

func TestSessionCookieConfiguration(t *testing.T) {
	t.Setenv("MANYROUTER_DATABASE_URL", "postgres://test")
	t.Setenv("MANYROUTER_AUTH_COOKIE_SECURE", "")
	loaded, err := Load(ModeMigrate)
	if err != nil || !loaded.AuthCookieSecure {
		t.Fatalf("secure default: %v", err)
	}
	t.Setenv("MANYROUTER_AUTH_COOKIE_SECURE", "false")
	loaded, err = Load(ModeMigrate)
	if err != nil || loaded.AuthCookieSecure {
		t.Fatalf("explicit local HTTP override: %v", err)
	}
	t.Setenv("MANYROUTER_AUTH_COOKIE_SECURE", "invalid")
	if _, err := Load(ModeMigrate); err == nil {
		t.Fatal("invalid cookie configuration accepted")
	}
}
