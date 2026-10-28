package main

import "testing"

// migrate --reset drops the public schema, so it only runs against a database
// whose name starts with "recon", unless --force-reset is given.
func TestCheckResetTarget(t *testing.T) {
	const host = "postgres://recon:recon@localhost:55432/"
	tests := []struct {
		name    string
		url     string
		force   bool
		wantErr bool
	}{
		{"ticket database", host + "recon_t01?sslmode=disable", false, false},
		{"default database", host + "recon?sslmode=disable", false, false},
		{"personal database refused", host + "personal_app?sslmode=disable", false, true},
		{"postgres database refused", host + "postgres?sslmode=disable", false, true},
		{"personal database allowed with force", host + "personal_app?sslmode=disable", true, false},
		{"no database name refused", "postgres://recon:recon@localhost:55432/?sslmode=disable", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkResetTarget(tc.url, tc.force)
			if (err != nil) != tc.wantErr {
				t.Errorf("checkResetTarget(%q, %v) error = %v, want error %v", tc.url, tc.force, err, tc.wantErr)
			}
		})
	}
}
