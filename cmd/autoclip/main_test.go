package main

import "testing"

func TestConfigurationValidation(t *testing.T) {
	t.Setenv("TEST_NUMBER", "")
	if v, e := positiveEnv("TEST_NUMBER", 42); e != nil || v != 42 {
		t.Fatal(v, e)
	}
	for _, s := range []string{"-1", "0", "no", "99999999999999999999999"} {
		t.Setenv("TEST_NUMBER", s)
		if _, e := positiveEnv("TEST_NUMBER", 42); e == nil {
			t.Fatal("invalid accepted", s)
		}
	}
	t.Setenv("TEST_NUMBER", "1024")
	if v, e := positiveEnv("TEST_NUMBER", 42); e != nil || v != 1024 {
		t.Fatal(v, e)
	}
}

func TestHealthCheckFollowsConfiguredPort(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		{"0.0.0.0:8123", "http://127.0.0.1:8123/api/v1/health"},
		{"[::]:8080", "http://[::1]:8080/api/v1/health"},
		{"127.0.0.1:9000", "http://127.0.0.1:9000/api/v1/health"},
	} {
		got, err := healthURL(tc.addr)
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	if _, err := healthURL("invalid"); err == nil {
		t.Fatal("invalid address accepted")
	}
}
