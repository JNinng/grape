package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/durationpb"
)

// 知识条目摄取的路由超时回归：嵌入端点首次调用慢于 request_timeout（本地
// 模型冷启动加载）时，新建条目经完整 HTTP 链（真实路由超时 + 认证）仍须
// 完成摄取，而不是在 chunk 0 的嵌入调用上 context deadline exceeded。

const (
	ingestRouteRequestTimeout = 40 * time.Millisecond
	ingestRouteEmbedLatency   = 3 * ingestRouteRequestTimeout
)

// ingestRouteSlowEmbedder 模拟冷启动的嵌入端点：每次调用耗时固定，且像真实
// HTTP 调用一样在 ctx 截止时提前失败。
type ingestRouteSlowEmbedder struct{}

func (ingestRouteSlowEmbedder) Embed(ctx context.Context, _ string, _ string) ([]float32, error) {
	select {
	case <-time.After(ingestRouteEmbedLatency):
		return []float32{1, 0}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (ingestRouteSlowEmbedder) Dimension(string) (int, error) { return 2, nil }

type ingestRouteResolver struct{}

func (ingestRouteResolver) ResolveDefaultEmbedder(context.Context) (string, int, error) {
	return "space@v1", 2, nil
}

func (ingestRouteResolver) ResolveEmbeddingSpace(context.Context, string) (biz.EmbeddingSpace, error) {
	return biz.EmbeddingSpace{Key: "space@v1", Dimension: 2}, nil
}

// ingestRouteStore 只承接摄取写路径（覆盖清除 + 逐块写入）。
type ingestRouteStore struct {
	biz.VectorStore
	added int
}

func (s *ingestRouteStore) DeleteByFilter(context.Context, *biz.VectorFilter) (int64, error) {
	return 0, nil
}

func (s *ingestRouteStore) Add(context.Context, *biz.VectorDocument) error {
	s.added++
	return nil
}

// ingestRouteRepo 承接新建条目的目录读写。
type ingestRouteRepo struct {
	biz.KnowledgeRepo
}

func (ingestRouteRepo) GetLibrary(context.Context, biz.Identity, int64) (*biz.KnowledgeLibrary, error) {
	delimiter := "\n\n"
	return &biz.KnowledgeLibrary{ID: 1, Status: biz.KnowledgeStatusEnabled, ChunkSize: 800, ChunkOverlap: 100, ChunkDelimiter: &delimiter}, nil
}

func (ingestRouteRepo) CreateDocument(_ context.Context, _ biz.Identity, d *biz.KnowledgeDocument) (*biz.KnowledgeDocument, error) {
	d.ID = 2
	return d, nil
}

func (ingestRouteRepo) MarkDocumentIndexed(_ context.Context, _ biz.Identity, id int64, _ string) (*biz.KnowledgeDocument, error) {
	content := "refund policy"
	return &biz.KnowledgeDocument{ID: id, LibraryID: 1, Kind: "text", Title: "refund", Content: &content, Status: biz.KnowledgeStatusEnabled}, nil
}

// newIngestRouteTestServer 装配含知识库路由的 HTTP 服务器：真实 trace / auth /
// 路由级超时中间件链，request_timeout 取小值、embedding_request_timeout 由
// 用例给定（0 表示未配置）。
func newIngestRouteTestServer(t *testing.T, embeddingTimeout time.Duration, store biz.VectorStore) *kratoshttp.Server {
	t.Helper()
	confServer := &conf.Server{Http: &conf.Server_HTTP{RequestTimeout: durationpb.New(ingestRouteRequestTimeout)}}
	if embeddingTimeout > 0 {
		confServer.Http.EmbeddingRequestTimeout = durationpb.New(embeddingTimeout)
	}
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	ingest, err := biz.NewDocumentUsecase(store, ingestRouteSlowEmbedder{}, ingestRouteResolver{}, nil)
	if err != nil {
		t.Fatalf("build ingest usecase: %v", err)
	}
	knowledge := service.NewKnowledgeService(biz.NewKnowledgeUsecase(ingestRouteRepo{}, nil, store, ingest), nil)
	srv, err := NewHTTPServer(
		confServer,
		nil,
		traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, nil),
		entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{})), nil,
		permStubMiddleware(),
		ops.NewMetricsHandler(nil, nil),
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		knowledge,
		nil, nil,
	)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	return srv
}

const ingestRouteCreateBody = `{"document":{"libraryId":"1","kind":"text","title":"refund","content":"refund policy"}}`

func TestCreateDocumentRoute_embedder_slower_than_request_timeout_completes_within_embedding_timeout(t *testing.T) {
	store := &ingestRouteStore{}
	srv := newIngestRouteTestServer(t, time.Minute, store)

	rec := agentDo(t, srv, http.MethodPost, "/v1/knowledge/documents/create", "any-token", ingestRouteCreateBody)
	if rec.Code != http.StatusOK || store.added != 1 {
		t.Fatalf("status = %d, chunks added = %d; want 200 with one chunk\n%s", rec.Code, store.added, rec.Body.String())
	}
}

func TestCreateDocumentRoute_embedder_slower_without_embedding_timeout_fails_indexing(t *testing.T) {
	store := &ingestRouteStore{}
	srv := newIngestRouteTestServer(t, 0, store)

	rec := agentDo(t, srv, http.MethodPost, "/v1/knowledge/documents/create", "any-token", ingestRouteCreateBody)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "DOCUMENT_INDEX_FAILED") {
		t.Fatalf("status = %d, want 500 DOCUMENT_INDEX_FAILED (fallback to request_timeout)\n%s", rec.Code, rec.Body.String())
	}
}
