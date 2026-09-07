package database

import "testing"

func TestValidateTestDatabaseURL(t *testing.T) {
	if err := ValidateTestDatabaseURL("postgres://resolver:password@localhost:5432/resolver_network_test?sslmode=disable"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTestDatabaseURL("postgres://resolver:password@localhost:5432/resolver_network?sslmode=disable"); err == nil {
		t.Fatal("expected development database URL to be rejected")
	}
}
