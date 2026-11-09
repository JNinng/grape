package server

import (
	"net/http"
	"strings"
	"testing"
)

// 知识检索路由守卫：端点经生成路由入链，认证先于权限判定；码表内端点
// 无权限时不得触达业务 handler（本装配的 knowledge 服务为 nil，触达即
// panic 经 recovery 变 500，以状态码暴露码表漏配）。

func TestKnowledgeSearchRoute_without_token_returns_401_before_permission(t *testing.T) {
	repo := &stubPermissionRepo{}
	srv := newPermissionTestServer(t, repo, nil, nil)

	rec := permRoutePostDo(t, srv, "/v1/knowledge/documents/search", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
	if repo.hasPermCalls != 0 {
		t.Fatalf("has-permission calls = %d, want 0", repo.hasPermCalls)
	}
}

func TestKnowledgeSearchRoute_without_permission_returns_403(t *testing.T) {
	repo := &stubPermissionRepo{granted: false}
	srv := newPermissionTestServer(t, repo, nil, nil)

	rec := permRoutePostDo(t, srv, "/v1/knowledge/documents/search", "any-token")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "PERM_ACCESS_DENIED") {
		t.Fatalf("status = %d, want 403 PERM_ACCESS_DENIED\n%s", rec.Code, rec.Body.String())
	}
	if repo.hasPermCalls != 1 {
		t.Fatalf("has-permission calls = %d, want 1 (gated operation)", repo.hasPermCalls)
	}
}
