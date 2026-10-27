package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"feedsystem/internal/utils/requestid"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestRequestID 验证全链路追踪中间件的两个核心行为：
// 1) 未携带 X-Request-Id 时生成 UUID v4，并同时写入响应头和请求 ctx；
// 2) 上游已携带时优先复用，不再重新生成。
func TestRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestID())
	r.GET("/ping", func(c *gin.Context) {
		// 模拟业务 handler：直接从请求 ctx 取 requestId
		c.String(http.StatusOK, requestid.FromContext(c.Request.Context()))
	})

	t.Run("generates uuid v4 when header absent", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		r.ServeHTTP(w, req)

		id := w.Header().Get(requestid.Header)
		if id == "" {
			t.Fatal("response header X-Request-Id is empty")
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			t.Fatalf("generated request id %q is not a valid uuid: %v", id, err)
		}
		if parsed.Version() != 4 {
			t.Fatalf("generated request id version = %d, want 4", parsed.Version())
		}
		if got := w.Body.String(); got != id {
			t.Fatalf("request ctx id = %q, want response header id %q", got, id)
		}
	})

	t.Run("reuses upstream request id", func(t *testing.T) {
		const upstreamID = "upstream-request-id-123"
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set(requestid.Header, upstreamID)
		r.ServeHTTP(w, req)

		if got := w.Header().Get(requestid.Header); got != upstreamID {
			t.Fatalf("response header X-Request-Id = %q, want upstream %q", got, upstreamID)
		}
		if got := w.Body.String(); got != upstreamID {
			t.Fatalf("request ctx id = %q, want upstream %q", got, upstreamID)
		}
	})
}
