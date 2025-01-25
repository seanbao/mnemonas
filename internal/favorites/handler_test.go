package favorites

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seanbao/mnemonas/internal/auth"
)

func TestGetUserIDFromClaims(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	claims := &auth.TokenClaims{UserID: "user-123"}
	req = req.WithContext(auth.WithClaimsContext(req.Context(), claims))

	if got := getUserID(req); got != "user-123" {
		t.Fatalf("expected user-123, got %s", got)
	}
}

func TestGetUserIDAnonymous(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if got := getUserID(req); got != "anonymous" {
		t.Fatalf("expected anonymous, got %s", got)
	}
}
