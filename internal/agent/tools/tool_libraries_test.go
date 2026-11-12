package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"grape/internal/agent/internal/testsupport"
	"grape/internal/biz"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// 列库工具单测（纯内存）：无入参、身份 fail-closed、可见集合映射与上限。
// 可见库集合与检索共用检索用例的同一解析实现（由 biz 层测试覆盖口径）。

func newLibrariesToolForTest(dir *fakeLibraryDirectory) tool.Tool {
	uc, err := biz.NewKnowledgeSearchUsecase(
		&fakeVectorStore{}, &testsupport.SearchTestEmbedder{Dims: 4}, &testsupport.SearchTestResolver{Dims: 4}, nil, dir)
	if err != nil {
		panic("tools test: build knowledge search usecase: " + err.Error())
	}
	return NewKnowledgeLibrariesTool(uc)
}

func callLibrariesTool(t *testing.T, tl tool.Tool, ctx context.Context) (listLibrariesOutput, error) {
	t.Helper()
	out, err := tl.(tool.CallableTool).Call(ctx, []byte(`{}`))
	if err != nil {
		return listLibrariesOutput{}, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal tool output: %v", err)
	}
	var parsed listLibrariesOutput
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("decode tool output: %v", err)
	}
	return parsed, nil
}

func librariesToolCtx() context.Context {
	return biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7})
}

func TestLibrariesTool_when_identity_missing_fails_closed(t *testing.T) {
	tl := newLibrariesToolForTest(defaultToolLibraries())
	if _, err := callLibrariesTool(t, tl, context.Background()); err == nil {
		t.Fatal("list libraries without identity = nil error, want fail-closed rejection")
	}
}

func TestLibrariesTool_lists_visible_libraries_with_notice(t *testing.T) {
	tl := newLibrariesToolForTest(&fakeLibraryDirectory{libraries: []*biz.KnowledgeLibrary{
		{ID: 4, Name: "员工手册", Status: biz.KnowledgeStatusEnabled},
		{ID: 9, Name: "停用库", Status: biz.KnowledgeStatusDisabled},
	}})

	out, err := callLibrariesTool(t, tl, librariesToolCtx())
	if err != nil {
		t.Fatalf("list libraries: %v", err)
	}
	if out.Count != 2 || len(out.Libraries) != 2 {
		t.Fatalf("libraries = %d, want 2", out.Count)
	}
	if out.Libraries[0].ID != "4" || out.Libraries[0].Name != "员工手册" || out.Libraries[0].Status != biz.KnowledgeStatusEnabled {
		t.Fatalf("first library = %+v, want id string \"4\" enabled", out.Libraries[0])
	}
	if out.Libraries[1].ID != "9" || out.Libraries[1].Status != biz.KnowledgeStatusDisabled {
		t.Fatalf("second library = %+v, want id string \"9\" disabled", out.Libraries[1])
	}
	if out.Truncated {
		t.Fatal("truncated = true, want false for a short list")
	}
	if out.Notice != libraryListNotice {
		t.Fatalf("notice = %q, want library list notice", out.Notice)
	}
}

func TestLibrariesTool_empty_visible_set_returns_empty_list(t *testing.T) {
	tl := newLibrariesToolForTest(&fakeLibraryDirectory{})

	out, err := callLibrariesTool(t, tl, librariesToolCtx())
	if err != nil {
		t.Fatalf("list libraries: %v", err)
	}
	if out.Libraries == nil || len(out.Libraries) != 0 || out.Count != 0 {
		t.Fatalf("libraries = %#v, want empty non-nil slice", out.Libraries)
	}
}

func TestLibrariesTool_caps_oversized_lists(t *testing.T) {
	libraries := make([]*biz.KnowledgeLibrary, 0, maxListedLibraries+1)
	for i := 0; i <= maxListedLibraries; i++ {
		libraries = append(libraries, &biz.KnowledgeLibrary{ID: int64(i + 1), Name: "lib"})
	}
	tl := newLibrariesToolForTest(&fakeLibraryDirectory{libraries: libraries})

	out, err := callLibrariesTool(t, tl, librariesToolCtx())
	if err != nil {
		t.Fatalf("list libraries: %v", err)
	}
	if out.Count != maxListedLibraries || !out.Truncated {
		t.Fatalf("count/truncated = %d/%v, want %d/true", out.Count, out.Truncated, maxListedLibraries)
	}
	if last := out.Libraries[len(out.Libraries)-1].ID; last != strconv.Itoa(maxListedLibraries) {
		t.Fatalf("last kept library id = %q, want %q (cap keeps the first N)", last, strconv.Itoa(maxListedLibraries))
	}
}

func TestLibrariesTool_when_exactly_limit_returns_not_truncated(t *testing.T) {
	libraries := make([]*biz.KnowledgeLibrary, 0, maxListedLibraries)
	for i := 0; i < maxListedLibraries; i++ {
		libraries = append(libraries, &biz.KnowledgeLibrary{ID: int64(i + 1), Name: "lib"})
	}
	tl := newLibrariesToolForTest(&fakeLibraryDirectory{libraries: libraries})

	out, err := callLibrariesTool(t, tl, librariesToolCtx())
	if err != nil {
		t.Fatalf("list libraries: %v", err)
	}
	if out.Count != maxListedLibraries || out.Truncated {
		t.Fatalf("count/truncated = %d/%v, want %d/false at the exact boundary",
			out.Count, out.Truncated, maxListedLibraries)
	}
}

func TestLibrariesTool_directory_error_propagates(t *testing.T) {
	tl := newLibrariesToolForTest(&fakeLibraryDirectory{err: errors.New("scope lookup failed")})

	if _, err := callLibrariesTool(t, tl, librariesToolCtx()); err == nil {
		t.Fatal("list libraries with failing directory = nil error, want error")
	}
}
