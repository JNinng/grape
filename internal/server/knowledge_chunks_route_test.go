package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/encoding/jsoncodec"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/durationpb"
)

// 条目分片路由：/v1/knowledge/documents/{id}/chunks 与 /v1/knowledge/documents/{id}
// 同前缀共存，验证路径不被点查路由吞并、分片按序渲染。

type chunksRouteStore struct {
	biz.VectorStore
	docs  []*biz.VectorDocument
	chunk *biz.VectorDocument
}

func (s *chunksRouteStore) List(context.Context, *biz.VectorFilter) ([]*biz.VectorDocument, error) {
	return s.docs, nil
}

func (s *chunksRouteStore) Get(context.Context, string) (*biz.VectorDocument, error) {
	return s.chunk, nil
}

type chunksRouteRepo struct {
	biz.KnowledgeRepo
}

func (chunksRouteRepo) GetDocument(context.Context, biz.Identity, int64) (*biz.KnowledgeDocument, error) {
	content := "refund policy"
	return &biz.KnowledgeDocument{ID: 2, LibraryID: 1, Kind: "text", Title: "refund", Content: &content, Status: biz.KnowledgeStatusEnabled}, nil
}

// GetLibrary 返回条目所属库：点查与分片查询先判库可见（库级可见性），
// 路由用例按可见库装配。
func (chunksRouteRepo) GetLibrary(context.Context, biz.Identity, int64) (*biz.KnowledgeLibrary, error) {
	return &biz.KnowledgeLibrary{ID: 1, Status: biz.KnowledgeStatusEnabled}, nil
}

// newChunksRouteTestServer 装配含知识库路由的服务器。jsoncodec 与生产
// main 同款安装，断言才是真实线上字段形态（小驼峰），而不是默认 codec 的
// snake_case。
func newChunksRouteTestServer(t *testing.T, store biz.VectorStore) *kratoshttp.Server {
	t.Helper()
	jsoncodec.Install()
	confServer := &conf.Server{Http: &conf.Server_HTTP{RequestTimeout: durationpb.New(ingestRouteRequestTimeout)}}
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	knowledge := service.NewKnowledgeService(biz.NewKnowledgeUsecase(chunksRouteRepo{}, nil, store, nil, nil, nil, nil), nil)
	srv, err := NewHTTPServer(
		confServer,
		nil,
		traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, nil),
		entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{})), nil,
		permStubMiddleware(),
		ops.NewMetricsHandler(nil, nil),
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, // AgentModelCallService（本文件用例不触达，传 nil）,
		knowledge,
		nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	return srv
}

func TestKnowledgeChunksRoute_renders_chunks_in_index_order(t *testing.T) {
	store := &chunksRouteStore{docs: []*biz.VectorDocument{
		{ID: "2#1", Embedder: "space@v1", Content: "second", Metadata: map[string]any{"anchor": "runes 10-16"}},
		{ID: "2#0", Embedder: "space@v1", Content: "first", Metadata: map[string]any{"anchor": "runes 0-6"}},
	}}
	srv := newChunksRouteTestServer(t, store)

	rec := agentDo(t, srv, http.MethodGet, "/v1/knowledge/documents/2/chunks", "any-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"chunkId":"2#0"`, `"content":"first"`, `"chunkId":"2#1"`, `"content":"second"`, `"total":2`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s:\n%s", want, body)
		}
	}
	if strings.Index(body, `"2#0"`) > strings.Index(body, `"2#1"`) {
		t.Fatalf("chunks out of order:\n%s", body)
	}
}

func TestKnowledgeChunksRoute_get_document_chunk_by_encoded_chunk_id(t *testing.T) {
	// 切片 ID 含 '#'，前端 URL 编码为 %23：路由须解码路径参数后按 ID 点查，
	// 且 /documents/chunks/{chunk_id} 不被 /documents/{id} 吞并。
	store := &chunksRouteStore{chunk: &biz.VectorDocument{
		ID:       "2#0#a1b2c3d4",
		Embedder: "space@v1",
		Content:  "chunk body",
		Metadata: map[string]any{
			"document_id":   "2",
			"anchor":        "runes 0-10",
			"chunk_index":   float64(0),
			"superseded_at": "2026-02-03T04:05:06Z",
		},
	}}
	srv := newChunksRouteTestServer(t, store)

	rec := agentDo(t, srv, http.MethodGet, "/v1/knowledge/documents/chunks/2%230%23a1b2c3d4", "any-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"chunkId":"2#0#a1b2c3d4"`, `"content":"chunk body"`, `"anchor":"runes 0-10"`,
		`"supersededAt":"2026-02-03T04:05:06Z"`, `"title":"refund"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s:\n%s", want, body)
		}
	}
	// 所属条目只回摘要，不回正文，避免大字段重复。
	if strings.Contains(body, `"content":"refund policy"`) {
		t.Fatalf("document content leaked into chunk detail:\n%s", body)
	}
}

func TestKnowledgeChunksRoute_point_query_path_still_serves_document(t *testing.T) {
	srv := newChunksRouteTestServer(t, &chunksRouteStore{})

	rec := agentDo(t, srv, http.MethodGet, "/v1/knowledge/documents/2", "any-token", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"title":"refund"`) {
		t.Fatalf("status = %d, want 200 with document body\n%s", rec.Code, rec.Body.String())
	}
}
