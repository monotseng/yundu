package config

import "testing"

func TestHardOfficeLimits(t *testing.T) {
	c := Config{Environment: "test", Database: Database{Host: "db", Name: "x", Username: "x", Password: "x"}, Limits: Limits{OfficeFileBytes: 30<<20 + 1}}
	applyDefaults(&c)
	if err := c.Validate(); err == nil {
		t.Fatal("expected hard limit rejection")
	}
}

func TestProductionFailsClosed(t *testing.T) {
	c := Config{Environment: "production", Database: Database{Host: "db", Name: "x", Username: "x", Password: "x"}}
	applyDefaults(&c)
	if err := c.Validate(); err == nil {
		t.Fatal("expected unsafe production config rejection")
	}
}
