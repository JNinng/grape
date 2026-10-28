package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"grape/internal/biz"

	dynq "github.com/jninng/sqlc_gen_dynamic/dynq"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// toolTestUsers 是工具单测的内存行集：当前用户（t-1/7）与同租户他人。
func toolTestUsers() []*biz.User {
	name := "alice"
	email := "alice@example.com"
	phone := "13800000007"
	remark := "keep-remark"
	return []*biz.User{
		{ID: 7, Status: biz.UserStatusEnabled, Name: "alice", Nickname: "Alice",
			Email: &email, Phone: &phone, Remark: &remark, Locale: "zh-CN",
			Password: "must-not-leak"},
		{ID: 8, Status: biz.UserStatusEnabled, Name: "bob", Nickname: "Bob", Locale: "zh-CN"},
		{ID: 9, Status: biz.UserStatusDisabled, Name: name, Nickname: "Carol", Locale: "zh-CN"},
	}
}

// callTool 以 JSON 入参直调指定名称的工具：走框架 CallableTool 转换面
// （schema 反序列化与模型入参形态一致），返回出参 JSON。
func callTool(t *testing.T, ctx context.Context, name, args string) (string, error) {
	t.Helper()
	var found tool.Tool
	for _, tl := range newUserTools(biz.NewUserUsecase(newFakeUserRepo(toolTestUsers()...))) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil && d.Name == name {
			found = tl
		}
	}
	if found == nil {
		t.Fatalf("tool %q not registered", name)
	}
	out, err := found.(tool.CallableTool).Call(ctx, []byte(args))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal tool output: %v", err)
	}
	return string(b), nil
}

// authCtx 注入当前用户身份的测试 ctx。
func authCtx() context.Context {
	return biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7})
}

func TestUserTool_when_identity_missing_fails_closed(t *testing.T) {
	ctx := context.Background() // 无身份
	for _, tt := range []struct {
		tool string
		args string
	}{{getCurrentUserToolName, `{}`}, {findUsersByNameToolName, `{"name":"alice"}`},
		{updateMyNicknameToolName, `{"nickname":"neo"}`}} {
		if _, err := callTool(t, ctx, tt.tool, tt.args); err == nil {
			t.Fatalf("tool %s without identity must fail closed", tt.tool)
		}
	}
}

func TestUserTool_when_get_current_user_returns_profile_without_password(t *testing.T) {
	out, err := callTool(t, authCtx(), getCurrentUserToolName, `{}`)
	if err != nil {
		t.Fatalf("get_current_user: %v", err)
	}
	if !strings.Contains(out, `"nickname":"Alice"`) || !strings.Contains(out, `"id":7`) {
		t.Fatalf("profile output missing own fields:\n%s", out)
	}
	if strings.Contains(out, "must-not-leak") {
		t.Fatalf("profile output leaked password material:\n%s", out)
	}
}

func TestUserTool_when_find_users_by_name_builds_fuzzy_page_query(t *testing.T) {
	repo := newFakeUserRepo(toolTestUsers()...)
	users := biz.NewUserUsecase(repo)
	var target tool.Tool
	for _, tl := range newUserTools(users) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil && d.Name == findUsersByNameToolName {
			target = tl
		}
	}
	if _, err := target.(tool.CallableTool).Call(authCtx(), []byte(`{"name":"ali","limit":5}`)); err != nil {
		t.Fatalf("find_users_by_name: %v", err)
	}
	qs := repo.queries()
	if len(qs) != 1 {
		t.Fatalf("List called %d times, want 1", len(qs))
	}
	q := qs[0]
	if len(q.Where) != 1 || q.Where[0].Field != "name" || q.Where[0].Op != string(dynq.OpContains) || q.Where[0].Value != "ali" {
		t.Fatalf("unexpected where clause: %+v", q.Where)
	}
	if q.Pagination == nil || q.Pagination.PageSize == nil || *q.Pagination.PageSize != 5 {
		t.Fatalf("unexpected pagination: %+v", q.Pagination)
	}
}

func TestUserTool_when_find_users_name_blank_or_oversize_rejected(t *testing.T) {
	for _, name := range []string{"", "   ", strings.Repeat("x", maxUserNameChars+1)} {
		args, err := json.Marshal(map[string]string{"name": name})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		if _, err := callTool(t, authCtx(), findUsersByNameToolName, string(args)); err == nil {
			t.Fatalf("name %q must be rejected", name)
		}
	}
}

func TestUserTool_when_find_users_limit_out_of_range_rejected(t *testing.T) {
	for _, limit := range []int{-1, maxFindUserLimit + 1} {
		args, err := json.Marshal(map[string]any{"name": "ali", "limit": limit})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		if _, err := callTool(t, authCtx(), findUsersByNameToolName, string(args)); err == nil {
			t.Fatalf("limit %d must be rejected", limit)
		}
	}
}

func TestUserTool_when_find_users_limit_zero_falls_back_to_default(t *testing.T) {
	repo := newFakeUserRepo(toolTestUsers()...)
	users := biz.NewUserUsecase(repo)
	var target tool.Tool
	for _, tl := range newUserTools(users) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil && d.Name == findUsersByNameToolName {
			target = tl
		}
	}
	if _, err := target.(tool.CallableTool).Call(authCtx(), []byte(`{"name":"ali"}`)); err != nil {
		t.Fatalf("find_users_by_name without limit: %v", err)
	}
	q := repo.queries()[0]
	if q.Pagination == nil || q.Pagination.PageSize == nil || *q.Pagination.PageSize != defaultFindUserLimit {
		t.Fatalf("default page size not applied: %+v", q.Pagination)
	}
}

func TestUserTool_when_update_nickname_blank_or_oversize_rejected(t *testing.T) {
	for _, nickname := range []string{"", "   ", strings.Repeat("x", maxNicknameChars+1)} {
		args, err := json.Marshal(map[string]string{"nickname": nickname})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		if _, err := callTool(t, authCtx(), updateMyNicknameToolName, string(args)); err == nil {
			t.Fatalf("nickname %q must be rejected", nickname)
		}
	}
}

func TestUserTool_when_update_nickname_updates_only_nickname_preserving_fields(t *testing.T) {
	repo := newFakeUserRepo(toolTestUsers()...)
	users := biz.NewUserUsecase(repo)
	var target tool.Tool
	for _, tl := range newUserTools(users) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil && d.Name == updateMyNicknameToolName {
			target = tl
		}
	}
	out, err := target.(tool.CallableTool).Call(authCtx(), []byte(`{"nickname":"  neo  "}`))
	if err != nil {
		t.Fatalf("update_my_nickname: %v", err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if !strings.Contains(string(b), `"nickname":"neo"`) || !strings.Contains(string(b), `"id":7`) {
		t.Fatalf("unexpected output:\n%s", b)
	}
	calls := repo.nicknames()
	if len(calls) != 1 || calls[0].id != 7 || calls[0].nickname != "neo" {
		t.Fatalf("nickname update calls = %+v, want one for user 7 with neo", calls)
	}
	if updates := repo.updates(); len(updates) != 0 {
		t.Fatalf("full-row Update must not be called, got %+v", updates)
	}
	stored, err := repo.GetByID(authCtx(), biz.Identity{TenantID: "t-1", UserID: 7}, 7)
	if err != nil {
		t.Fatalf("read stored user: %v", err)
	}
	if stored.Nickname != "neo" || stored.Name != "alice" || stored.Status != biz.UserStatusEnabled ||
		stored.Locale != "zh-CN" || stored.Password != "must-not-leak" {
		t.Fatalf("stored user drifted: %+v", stored)
	}
	if stored.Email == nil || *stored.Email != "alice@example.com" ||
		stored.Phone == nil || *stored.Phone != "13800000007" ||
		stored.Remark == nil || *stored.Remark != "keep-remark" {
		t.Fatalf("non-nickname mutable fields must be preserved: %+v", stored)
	}
}

func TestUserTool_when_update_nickname_target_missing_maps_not_found(t *testing.T) {
	repo := newFakeUserRepo() // 空行集：身份对应的用户不存在
	users := biz.NewUserUsecase(repo)
	var target tool.Tool
	for _, tl := range newUserTools(users) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil && d.Name == updateMyNicknameToolName {
			target = tl
		}
	}
	if _, err := target.(tool.CallableTool).Call(authCtx(), []byte(`{"nickname":"neo"}`)); err != biz.ErrUserNotFound {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}
