package biz

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 检索留痕查询用例单测：分页归一（缺省/上限/offset 非负）、来源取值
// 校验与身份门禁。仓储以替身透传过滤器，校验是否发生在用例侧由此断言。
type retrievalLogFakeRepo struct {
	gotFilter *RetrievalLogFilter
	listErr   error
}

func (r *retrievalLogFakeRepo) Append(context.Context, *RetrievalLogRecord) error {
	return errors.New("not implemented")
}

func (r *retrievalLogFakeRepo) List(_ context.Context, f *RetrievalLogFilter) (*RetrievalLogList, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	r.gotFilter = f
	return &RetrievalLogList{Items: []*RetrievalLog{}, Total: 0}, nil
}

func TestRetrievalLogUsecase_pagination_defaults_and_bounds(t *testing.T) {
	uc := NewRetrievalLogUsecase(&retrievalLogFakeRepo{})
	ctx := WithIdentity(context.Background(), Identity{TenantID: "t-1", UserID: 1})

	// 缺省页大小补齐 + 过滤器原样透传。
	if _, err := uc.List(ctx, nil); err != nil {
		t.Fatalf("list(nil): %v", err)
	}

	repo := &retrievalLogFakeRepo{}
	uc2 := NewRetrievalLogUsecase(repo)
	if _, err := uc2.List(ctx, &RetrievalLogFilter{SessionID: "sess-1", Source: RetrievalSourceTool, Limit: 5, Offset: 10}); err != nil {
		t.Fatalf("list: %v", err)
	}
	f := repo.gotFilter
	switch {
	case f.Limit != 5 || f.Offset != 10:
		t.Fatalf("filter = %+v, want explicit pagination preserved", f)
	case f.SessionID != "sess-1" || f.Source != RetrievalSourceTool:
		t.Fatalf("filter = %+v, want session/source preserved", f)
	}

	// 上限 200 与来源/会话/offset 校验 fail-closed，不静默截断。
	for name, filter := range map[string]*RetrievalLogFilter{
		"limit over max":   {Limit: 201},
		"limit negative":   {Limit: -1},
		"offset negative":  {Offset: -1},
		"source unknown":   {Source: "management"},
		"session too long": {SessionID: strings.Repeat("s", 256)},
	} {
		if _, err := uc.List(ctx, filter); !errors.Is(err, ErrRetrievalLogInvalidArgument) {
			t.Fatalf("%s: err = %v, want ErrRetrievalLogInvalidArgument", name, err)
		}
	}
}

func TestRetrievalLogUsecase_requires_identity(t *testing.T) {
	uc := NewRetrievalLogUsecase(&retrievalLogFakeRepo{})
	if _, err := uc.List(context.Background(), nil); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid (read side is identity-bound too)", err)
	}
}
