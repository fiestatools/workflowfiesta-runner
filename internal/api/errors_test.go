package api_test

import (
	"errors"
	"testing"

	"workflowfiesta-runner/internal/api"
)

func TestAuthRevokedStatus(t *testing.T) {
	for _, code := range []int{401, 403} {
		if !api.AuthRevokedStatus(code) {
			t.Errorf("expected %d to be auth revoked", code)
		}
	}
	for _, code := range []int{404, 500} {
		if api.AuthRevokedStatus(code) {
			t.Errorf("expected %d to NOT be auth revoked", code)
		}
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

func TestShouldRevokeAuth(t *testing.T) {
	// 401 and 403 always revoke regardless of body
	for _, code := range []int{401, 403} {
		if !api.ShouldRevokeAuth(code, nil) {
			t.Errorf("expected %d to revoke auth", code)
		}
	}
	// 404 with runner_not_found body → revoke
	if !api.ShouldRevokeAuth(404, []byte(`{"error":"runner_not_found"}`)) {
		t.Error("404 with runner_not_found body should revoke auth")
	}
	// 404 with generic body → do not revoke
	if api.ShouldRevokeAuth(404, []byte(`{"error":"not found"}`)) {
		t.Error("404 with generic body should NOT revoke auth")
	}
	// 404 with empty body → do not revoke
	if api.ShouldRevokeAuth(404, nil) {
		t.Error("404 with empty body should NOT revoke auth")
	}
	// 500 never revokes
	if api.ShouldRevokeAuth(500, []byte(`{"error":"runner_not_found"}`)) {
		t.Error("500 should NOT revoke auth even with runner_not_found body")
	}
}
