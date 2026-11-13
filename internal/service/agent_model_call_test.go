package service

import (
	"context"
	"errors"
	"testing"
	"time"

	agentv1 "grape/api/agent/v1"
	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// fakeModelCallLogRepo 捕获用例转发的查询，供断言身份与查询协议透传。
type fakeModelCallLogRepo struct {
	biz.ModelCallLogRepo

	gotAuth  biz.Identity
	gotQuery *dynq.Query
	list     *biz.ModelCallLogList
	listErr  error
}

func (f *fakeModelCallLogRepo) List(_ context.Context, auth biz.Identity, q *dynq.Query) (*biz.ModelCallLogList, error) {
	f.gotAuth = auth
	f.gotQuery = q
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.list, nil
}

// modelCallLogRowOf 是落库已脱敏形态的样板行：按用例逐字段覆盖。
func modelCallLogRowOf() *biz.ModelCallLog {
	return &biz.ModelCallLog{
		ID:            9,
		UserID:        7,
		TraceID:       "0123456789abcdef0123456789abcdef",
		Scenario:      biz.ModelCallScenarioIngestSummary,
		ModelKey:      "glm-4",
		Endpoint:      "http://llm.example/v1/chat/completions",
		DocumentID:    42,
		InputSummary:  "summarize prompt",
		OutputSummary: "document summary",
		InputTokens:   128,
		OutputTokens:  64,
		DurationMS:    350,
		Status:        biz.ModelCallStatusSuccess,
		CreatedBy:     7,
		CreatedAt:     time.Unix(1700000000, 0).UTC(),
	}
}

func TestAgentModelCallListReturnsEnvelopeAndDTO(t *testing.T) {
	total := int64(1)
	repo := &fakeModelCallLogRepo{list: &biz.ModelCallLogList{
		Items: []*biz.ModelCallLog{modelCallLogRowOf()},
		Total: &total, HasMore: false, NextCursor: "",
	}}
	svc := NewAgentModelCallService(biz.NewModelCallLogUsecase(repo, nil))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(2)

	out, err := svc.ListModelCalls(biz.WithIdentity(context.Background(), auth), &agentv1.ListModelCallsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page}},
	})
	if err != nil {
		t.Fatalf("list model calls: %v", err)
	}
	row := out.GetItems()[0]
	switch {
	case row.GetId() != 9 || row.GetUserId() != 7 || row.GetCreatedBy() != 7:
		t.Fatalf("row ids = (%d, %d, %d), want (9, 7, 7)", row.GetId(), row.GetUserId(), row.GetCreatedBy())
	case row.GetScenario() != biz.ModelCallScenarioIngestSummary || row.GetStatus() != biz.ModelCallStatusSuccess:
		t.Fatalf("row = (%s/%s), want ingest_summary/success", row.GetScenario(), row.GetStatus())
	case row.GetModelKey() != "glm-4" || row.GetEndpoint() == "" || row.GetTraceId() == "":
		t.Fatalf("row facts = (%q, %q, %q), want model key/endpoint/trace present", row.GetModelKey(), row.GetEndpoint(), row.GetTraceId())
	case row.GetDocumentId() != 42 || row.GetInputTokens() != 128 || row.GetOutputTokens() != 64 || row.GetDurationMs() != 350:
		t.Fatalf("row metrics = (%d, %d, %d, %d), want (42, 128, 64, 350)", row.GetDocumentId(), row.GetInputTokens(), row.GetOutputTokens(), row.GetDurationMs())
	case row.GetCreatedAt().AsTime() != time.Unix(1700000000, 0).UTC():
		t.Fatalf("created_at = %v, want UTC RFC3339 of the stored fact", row.GetCreatedAt().AsTime())
	case out.GetTotal() != 1 || out.GetPage() != 2 || out.GetHasMore():
		t.Fatalf("envelope = (%d, %d, %v), want (1, 2, false)", out.GetTotal(), out.GetPage(), out.GetHasMore())
	}
	if repo.gotAuth != auth {
		t.Fatalf("repo auth = %+v, want %+v", repo.gotAuth, auth)
	}
}

func TestAgentModelCallListRendersUnreportedAsUnset(t *testing.T) {
	row := modelCallLogRowOf()
	row.TraceID = ""
	row.ModelKey = ""
	row.Endpoint = ""
	row.DocumentID = 0
	row.InputSummary = ""
	row.OutputSummary = ""
	row.InputTokens = 0
	row.OutputTokens = 0
	row.DurationMS = 0
	row.Error = ""
	repo := &fakeModelCallLogRepo{list: &biz.ModelCallLogList{Items: []*biz.ModelCallLog{row}}}
	svc := NewAgentModelCallService(biz.NewModelCallLogUsecase(repo, nil))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.ListModelCalls(biz.WithIdentity(context.Background(), auth), &agentv1.ListModelCallsRequest{})
	if err != nil {
		t.Fatalf("list model calls: %v", err)
	}
	got := out.GetItems()[0]
	if got.TraceId != nil || got.ModelKey != nil || got.Endpoint != nil || got.DocumentId != nil ||
		got.InputSummary != nil || got.OutputSummary != nil || got.InputTokens != nil ||
		got.OutputTokens != nil || got.DurationMs != nil || got.Error != nil {
		t.Fatalf("optional fields = %+v, want all unset (0/empty means NULL: unreported facts must not masquerade as 0)", got)
	}
	if got.GetScenario() == "" || got.GetStatus() == "" {
		t.Fatalf("row = (%q/%q), want scenario and status present", got.GetScenario(), got.GetStatus())
	}
}

func TestAgentModelCallListFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeModelCallLogRepo{}
	svc := NewAgentModelCallService(biz.NewModelCallLogUsecase(repo, nil))

	_, err := svc.ListModelCalls(context.Background(), &agentv1.ListModelCallsRequest{})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
}

func TestAgentModelCallListRejectsInvalidQuery(t *testing.T) {
	repo := &fakeModelCallLogRepo{}
	svc := NewAgentModelCallService(biz.NewModelCallLogUsecase(repo, nil))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListModelCalls(biz.WithIdentity(context.Background(), auth), &agentv1.ListModelCallsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrModelCallInvalidArgument) {
		t.Fatalf("err = %v, want ErrModelCallInvalidArgument", err)
	}
	if repo.gotQuery != nil {
		t.Fatalf("repo got query %+v, want none", repo.gotQuery)
	}
}

func TestAgentModelCallListPropagatesRepoError(t *testing.T) {
	repoErr := errors.New("agent model call log storage down")
	repo := &fakeModelCallLogRepo{listErr: repoErr}
	svc := NewAgentModelCallService(biz.NewModelCallLogUsecase(repo, nil))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.ListModelCalls(biz.WithIdentity(context.Background(), auth), &agentv1.ListModelCallsRequest{})
	if !errors.Is(err, repoErr) {
		t.Fatalf("err = %v, want repo error propagated", err)
	}
}
