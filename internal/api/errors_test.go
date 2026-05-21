package api_test

import (
	"errors"
	"testing"

	"workflowfiesta-runner/internal/api"
)

func TestAuthRevokedStatus(t *testing.T) {
	for _, code := range []int{401, 403, 404} {
		if !api.AuthRevokedStatus(code) {
			t.Errorf("expected %d to be auth revoked", code)
		}
	}
	if api.AuthRevokedStatus(500) {
		t.Error("500 should not be auth revoked")
	}
}

func TestIsAuthRevoked(t *testing.T) {
	err := api.AuthRevokedError(401, "/api/runner/jobs/next", []byte(`{"error":"unauthorized"}`))
	if !api.IsAuthRevoked(err) {
		t.Fatalf("expected IsAuthRevoked true, got %v", err)
	}
	if !errors.Is(err, api.ErrAuthRevoked) {
		t.Fatal("expected errors.Is ErrAuthRevoked")
	}
}
