package config

import "testing"

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		secret  string
		wantErr bool
	}{
		{"prod default secret fails", "production", defaultJWTSecret, true},
		{"prod empty secret fails", "prod", "", true},
		{"prod real secret ok", "production", "s3cr3t", false},
		{"dev default secret ok", "development", defaultJWTSecret, false},
		{"dev empty secret ok", "development", "", false},
		{"unknown env treated as non-prod", "staging", defaultJWTSecret, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Environment: tc.env, JWTSecret: tc.secret}
			err := cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestIsProduction(t *testing.T) {
	for _, env := range []string{"production", "prod"} {
		if !(Config{Environment: env}).IsProduction() {
			t.Errorf("%q should be production", env)
		}
	}
	for _, env := range []string{"development", "staging", ""} {
		if (Config{Environment: env}).IsProduction() {
			t.Errorf("%q should not be production", env)
		}
	}
}

func TestLoadNormalizesEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "  PRODUCTION ")
	cfg := Load("DISPATCH", "3005")
	if cfg.Environment != "production" {
		t.Fatalf("expected normalized environment 'production', got %q", cfg.Environment)
	}
	if !cfg.IsProduction() {
		t.Fatalf("expected IsProduction true")
	}
}
