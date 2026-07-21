package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/posul/github-notifier/internal/platform/middleware"
)

func newAuthTestRouter(token string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.InternalAuth(token))
	r.POST("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func doAuthRequest(r *gin.Engine, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/protected", nil)
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	r.ServeHTTP(w, req)
	return w
}

func TestInternalAuth_ValidToken(t *testing.T) {
	r := newAuthTestRouter("secret")
	w := doAuthRequest(r, "secret")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestInternalAuth_MissingOrInvalidToken(t *testing.T) {
	r := newAuthTestRouter("secret")

	w := doAuthRequest(r, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "missing or invalid internal token")

	w = doAuthRequest(r, "wrong")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "missing or invalid internal token")
}

func TestInternalAuth_EmptyConfiguredTokenIsNoop(t *testing.T) {
	r := newAuthTestRouter("")
	w := doAuthRequest(r, "")
	assert.Equal(t, http.StatusOK, w.Code)
}
