package handler_test

import (
	"context"
	"io"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	"github.com/posul/github-notifier/internal/handler"
	"github.com/posul/github-notifier/internal/model"
	"github.com/stretchr/testify/mock"
)

type mockService struct {
	mock.Mock
}

func (m *mockService) Subscribe(ctx context.Context, email, repo string) error {
	return m.Called(ctx, email, repo).Error(0)
}

func (m *mockService) Confirm(ctx context.Context, token string) error {
	return m.Called(ctx, token).Error(0)
}

func (m *mockService) Unsubscribe(ctx context.Context, token string) error {
	return m.Called(ctx, token).Error(0)
}

func (m *mockService) GetSubscriptions(ctx context.Context, email string) ([]*model.Subscription, error) {
	args := m.Called(ctx, email)
	subs, _ := args.Get(0).([]*model.Subscription)
	return subs, args.Error(1)
}

func newTestRouter(svc *mockService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.New(svc, svc, svc, svc)
	api := r.Group("/api")
	api.POST("/subscribe", h.Subscribe)
	api.GET("/confirm/:token", h.Confirm)
	api.GET("/unsubscribe/:token", h.Unsubscribe)
	api.GET("/subscriptions", h.GetSubscriptions)
	return r
}

func doRequest(r *gin.Engine, method, path string, body io.Reader) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(w, req)
	return w
}
