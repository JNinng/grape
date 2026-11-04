package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"grape/internal/agent/internal/testsupport"
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
	for _, tl := range NewUserTools(biz.NewUserUsecase(testsupport.NewFakeUserRepo(toolTestUsers()...))) {
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
	}{{GetCurrentUserToolName, `{}`}, {FindUsersByNameToolName, `{"name":"alice"}`},
		{UpdateMyNicknameToolName, `{"nickname":"neo"}`}} {
		if _, err := callTool(t, ctx, tt.tool, tt.args); err == nil {
			t.Fatalf("tool %s without identity must fail closed", tt.tool)
		}
	}
}

func TestUserTool_when_get_current_user_returns_profile_without_password(t *testing.T) {
	out, err := callTool(t, authCtx(), GetCurrentUserToolName, `{}`)
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
	repo := testsupport.NewFakeUserRepo(toolTestUsers()...)
	users := biz.NewUserUsecase(repo)
	var target tool.Tool
	for _, tl := range NewUserTools(users) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil && d.Name == FindUsersByNameToolName {
			target = tl
		}
	}
	if _, err := target.(tool.CallableTool).Call(authCtx(), []byte(`{"name":"ali","limit":5}`)); err != nil {
		t.Fatalf("find_users_by_name: %v", err)
	}
	qs := repo.Queries()
	if len(qs) != 1 {
		t.Fatalf("List called %d times, want 1", len(qs))
	}
	q := qs[0]
	if len(q.Where) != 1 || q.Where[0].Field != "name" || q.Where[0].Op != dynq.OpContains || q.Where[0].Value != "ali" {
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
		if _, err := callTool(t, authCtx(), FindUsersByNameToolName, string(args)); err == nil {
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
		if _, err := callTool(t, authCtx(), FindUsersByNameToolName, string(args)); err == nil {
			t.Fatalf("limit %d must be rejected", limit)
		}
	}
}

func TestUserTool_when_find_users_limit_zero_falls_back_to_default(t *testing.T) {
	repo := testsupport.NewFakeUserRepo(toolTestUsers()...)
	users := biz.NewUserUsecase(repo)
	var target tool.Tool
	for _, tl := range NewUserTools(users) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil && d.Name == FindUsersByNameToolName {
			target = tl
		}
	}
	if _, err := target.(tool.CallableTool).Call(authCtx(), []byte(`{"name":"ali"}`)); err != nil {
		t.Fatalf("find_users_by_name without limit: %v", err)
	}
	q := repo.Queries()[0]
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
		if _, err := callTool(t, authCtx(), UpdateMyNicknameToolName, string(args)); err == nil {
			t.Fatalf("nickname %q must be rejected", nickname)
		}
	}
}

func TestUserTool_when_update_nickname_updates_only_nickname_preserving_fields(t *testing.T) {
	repo := testsupport.NewFakeUserRepo(toolTestUsers()...)
	users := biz.NewUserUsecase(repo)
	var target tool.Tool
	for _, tl := range NewUserTools(users) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil && d.Name == UpdateMyNicknameToolName {
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
	calls := repo.Nicknames()
	if len(calls) != 1 || calls[0].ID != 7 || calls[0].Nickname != "neo" {
		t.Fatalf("nickname update calls = %+v, want one for user 7 with neo", calls)
	}
	if updates := repo.Updates(); len(updates) != 0 {
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
	repo := testsupport.NewFakeUserRepo() // 空行集：身份对应的用户不存在
	users := biz.NewUserUsecase(repo)
	var target tool.Tool
	for _, tl := range NewUserTools(users) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil && d.Name == UpdateMyNicknameToolName {
			target = tl
		}
	}
	if _, err := target.(tool.CallableTool).Call(authCtx(), []byte(`{"nickname":"neo"}`)); err != biz.ErrUserNotFound {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

// TestUserTool_declaration_input_schema_declares_parameter_constraints 锁定
// jsonschema tag 到声明面的投影：参数约束经 InputSchema 暴露给模型，
// 工具描述文本不承担参数说明职责。
func TestUserTool_declaration_input_schema_declares_parameter_constraints(t *testing.T) {
	decls := map[string]*tool.Declaration{}
	for _, tl := range NewUserTools(biz.NewUserUsecase(testsupport.NewFakeUserRepo(toolTestUsers()...))) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil {
			decls[d.Name] = d
		}
	}
	for _, tt := range []struct {
		tool      string
		required  []string
		wantDescs map[string]string
	}{
		{
			tool:     FindUsersByNameToolName,
			required: []string{"name"},
			wantDescs: map[string]string{
				"name":  "User name to fuzzy match (trimmed to at most 64 characters)",
				"limit": "Optional page size between 1 and 20 (default 10)",
			},
		},
		{
			tool:      UpdateMyNicknameToolName,
			required:  []string{"nickname"},
			wantDescs: map[string]string{"nickname": "New nickname (trimmed to 1 to 256 characters)"},
		},
	} {
		d, ok := decls[tt.tool]
		if !ok {
			t.Fatalf("tool %q not registered", tt.tool)
		}
		if len(d.InputSchema.Required) != len(tt.required) {
			t.Fatalf("%s required = %v, want %v", tt.tool, d.InputSchema.Required, tt.required)
		}
		for i, want := range tt.required {
			if d.InputSchema.Required[i] != want {
				t.Fatalf("%s required = %v, want %v", tt.tool, d.InputSchema.Required, tt.required)
			}
		}
		for field, want := range tt.wantDescs {
			p, ok := d.InputSchema.Properties[field]
			if !ok {
				t.Fatalf("%s schema missing property %q", tt.tool, field)
			}
			if p.Description != want {
				t.Fatalf("%s.%s description = %q, want %q", tt.tool, field, p.Description, want)
			}
		}
	}
}

// TestUserTool_declaration_output_schema_declares_field_descriptions 锁定
// 出参字段的 jsonschema 投影：OutputSchema 携带逐字段说明，工具描述文本
// 不承担字段清单职责。
func TestUserTool_declaration_output_schema_declares_field_descriptions(t *testing.T) {
	decls := map[string]*tool.Declaration{}
	for _, tl := range NewUserTools(biz.NewUserUsecase(testsupport.NewFakeUserRepo(toolTestUsers()...))) {
		if d := tl.(tool.CallableTool).Declaration(); d != nil {
			decls[d.Name] = d
		}
	}
	for _, tt := range []struct {
		tool      string
		wantDescs map[string]string
	}{
		{
			tool: GetCurrentUserToolName,
			wantDescs: map[string]string{
				"id":       "User id",
				"name":     "Login name",
				"nickname": "Display nickname",
				"status":   "Account status: 0 disabled / 1 enabled",
				"email":    "Email address (absent when unset)",
				"phone":    "Phone number (absent when unset)",
				"locale":   "Preferred language tag",
			},
		},
		{
			tool: FindUsersByNameToolName,
			wantDescs: map[string]string{
				"total":    "Total matching users (absent when unknown)",
				"has_more": "True when more pages remain",
				"users":    "Matching users in this page",
			},
		},
	} {
		d, ok := decls[tt.tool]
		if !ok {
			t.Fatalf("tool %q not registered", tt.tool)
		}
		for field, want := range tt.wantDescs {
			p, ok := d.OutputSchema.Properties[field]
			if !ok {
				t.Fatalf("%s output schema missing property %q", tt.tool, field)
			}
			if p.Description != want {
				t.Fatalf("%s output.%s description = %q, want %q", tt.tool, field, p.Description, want)
			}
		}
	}
}
