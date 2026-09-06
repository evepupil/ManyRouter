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

func TestCompatibilityFileConfiguration(t *testing.T) {
	t.Setenv("MANYROUTER_DATABASE_URL", "postgres://test")
	t.Setenv("MANYROUTER_COMPATIBILITY_FILE", "")
	loaded, err := Load(ModeMigrate)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CompatibilityFile != "deploy/compatibility.yaml" {
		t.Fatalf("default compatibility file = %q", loaded.CompatibilityFile)
	}
	t.Setenv("MANYROUTER_COMPATIBILITY_FILE", "release/catalog.yaml")
	loaded, err = Load(ModeMigrate)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CompatibilityFile != "release/catalog.yaml" {
		t.Fatalf("configured compatibility file = %q", loaded.CompatibilityFile)
	}
}
