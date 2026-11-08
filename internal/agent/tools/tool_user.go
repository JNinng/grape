package tools

import (
	"context"
	"fmt"
	"strings"

	"grape/internal/agent/identity"
	"grape/internal/biz"

	dynq "github.com/jninng/sqlc_gen_dynamic/dynq"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// 业务工具首批：两个只读 + 一个 Confirm 写，全部走 biz 用户用例完成
// 数据访问（RLS 与 @scope 由存储层强制，工具层不直连 SQL）；执行身份
// 来自认证 ctx。
const (
	// GetCurrentUserToolName 查询当前登录用户自己的资料。
	GetCurrentUserToolName = "get_current_user"
	// FindUsersByNameToolName 在调用者数据范围内按用户名模糊查找。
	FindUsersByNameToolName = "find_users_by_name"
	// UpdateMyNicknameToolName 修改当前登录用户自己的昵称（Confirm 写路径）。
	UpdateMyNicknameToolName = "update_my_nickname"
)

// 工具入参边界：昵称上限与 sys_user.nickname 列宽（varchar(256)）对齐；
// 查询名限短（模糊匹配无必要过长）；单页条数上限防御模型发起超大分页
// （存储层 @page 上限 400 仍是最终防线，这里是更早的拒绝点）。
const (
	maxNicknameChars     = 256
	maxUserNameChars     = 64
	defaultFindUserLimit = 10
	maxFindUserLimit     = 20
)

// NewUserTools 构造业务工具集合：框架 function 工具注册，入参 schema 由
// 框架按 Go 类型推导。users 为 biz 用户用例，nil 由装配方在 Stack 构造期
// 拒绝（见根包 stack.go 的 NewStackWithOptions 必填校验）。
func NewUserTools(users *biz.UserUsecase) []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(usersCurrentUser(users),
			function.WithName(GetCurrentUserToolName),
			function.WithDescription("Get the signed-in user's own profile. "+
				"Takes no arguments."),
		),
		function.NewFunctionTool(usersFindByName(users),
			function.WithName(FindUsersByNameToolName),
			function.WithDescription("Search users by name (fuzzy match) within "+
				"the caller's data scope."),
		),
		function.NewFunctionTool(usersUpdateNickname(users),
			function.WithName(UpdateMyNicknameToolName),
			function.WithDescription("Update the signed-in user's own nickname."),
		),
	}
}

// userSummary 是工具出参的用户摘要投影：刻意收窄字段面，口令、审计列、
// 归属三元组不出工具层。字段说明经 jsonschema tag 进入 OutputSchema
// （tag 值内不可含逗号——框架按逗号切分 tag 项）。
type userSummary struct {
	ID       int64   `json:"id" jsonschema:"description=User id"`
	Name     string  `json:"name" jsonschema:"description=Login name"`
	Nickname string  `json:"nickname" jsonschema:"description=Display nickname"`
	Status   int32   `json:"status" jsonschema:"description=Account status: 0 disabled / 1 enabled"`
	Email    *string `json:"email,omitempty" jsonschema:"description=Email address (absent when unset)"`
	Phone    *string `json:"phone,omitempty" jsonschema:"description=Phone number (absent when unset)"`
	Locale   string  `json:"locale" jsonschema:"description=Preferred language tag"`
}

func summarizeUser(u *biz.User) userSummary {
	return userSummary{
		ID:       u.ID,
		Name:     u.Name,
		Nickname: u.Nickname,
		Status:   u.Status,
		Email:    u.Email,
		Phone:    u.Phone,
		Locale:   u.Locale,
	}
}

// identityOf 从工具执行 ctx 读取认证身份：live 路径的运行 ctx 与恢复路径
// 的决定请求 ctx 都经认证中间件注入；缺失即 fail-closed（正常挂载下
// 不可能到达——AG-UI UserIDResolver/ack WithUserFunc 已在同一 ctx 上
// 先行校验，这里是工具入参面独立的最后防线）。
func identityOf(ctx context.Context) (biz.Identity, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return biz.Identity{}, identity.ErrMissingIdentity
	}
	return id, nil
}

// usersCurrentUser 返回查询当前用户信息的工具处理函数：身份即查询键，
// 无入参；目标不可见（含自己被删）按业务语义返回 NOT_FOUND。
func usersCurrentUser(users *biz.UserUsecase) func(context.Context, struct{}) (userSummary, error) {
	return func(ctx context.Context, _ struct{}) (userSummary, error) {
		auth, err := identityOf(ctx)
		if err != nil {
			return userSummary{}, err
		}
		u, err := users.GetUser(ctx, auth, auth.UserID)
		if err != nil {
			return userSummary{}, err
		}
		return summarizeUser(u), nil
	}
}

// findUsersByNameInput 是按名查用户的工具入参。
type findUsersByNameInput struct {
	Name string `json:"name" jsonschema:"description=User name to fuzzy match (trimmed to at most 64 characters),required"`
	// omitempty 使框架不把 limit 标为 required：省略即由处理函数回落
	// defaultFindUserLimit。
	Limit int `json:"limit,omitempty" jsonschema:"description=Optional page size between 1 and 20 (default 10)"`
}

// findUsersByNameOutput 是按名查用户的工具出参。
type findUsersByNameOutput struct {
	Total   *int64        `json:"total,omitempty" jsonschema:"description=Total matching users (absent when unknown)"`
	HasMore bool          `json:"has_more" jsonschema:"description=True when more pages remain"`
	Users   []userSummary `json:"users" jsonschema:"description=Matching users in this page"`
}

// usersFindByName 返回按用户名模糊查找的工具处理函数：查询协议在本层
// 组装（与 service 层 convertQuery 同角色——biz 用例只收 dynq.Query），
// 字段/操作符合法性最终由仓储生成代码校验，协议错误映射 INVALID_ARGUMENT。
func usersFindByName(users *biz.UserUsecase) func(context.Context, findUsersByNameInput) (findUsersByNameOutput, error) {
	return func(ctx context.Context, in findUsersByNameInput) (findUsersByNameOutput, error) {
		auth, err := identityOf(ctx)
		if err != nil {
			return findUsersByNameOutput{}, err
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return findUsersByNameOutput{}, fmt.Errorf("name is required")
		}
		if len(name) > maxUserNameChars {
			return findUsersByNameOutput{}, fmt.Errorf("name must be at most %d characters", maxUserNameChars)
		}
		limit := in.Limit
		if limit == 0 {
			limit = defaultFindUserLimit
		}
		if limit < 0 || limit > maxFindUserLimit {
			return findUsersByNameOutput{}, fmt.Errorf("limit must be between 1 and %d", maxFindUserLimit)
		}
		// 模糊匹配在运行时对应 contains：@dynamic: fuzzy 在生成期展开为
		// contains/prefix/suffix，协议门不接受 fuzzy 字面量。
		list, err := users.ListUsers(ctx, auth, &dynq.Query{
			Where: []dynq.Condition{{Field: "name", Op: dynq.OpContains, Value: name}},
			Pagination: &dynq.Pagination{
				Page:     ptr(1),
				PageSize: ptr(limit),
			},
		})
		if err != nil {
			return findUsersByNameOutput{}, err
		}
		out := findUsersByNameOutput{
			Total:   list.Total,
			HasMore: list.HasMore,
			Users:   make([]userSummary, 0, len(list.Items)),
		}
		for _, u := range list.Items {
			out.Users = append(out.Users, summarizeUser(u))
		}
		return out, nil
	}
}

// updateNicknameInput 是改昵称的工具入参，约束声明方式同 findUsersByNameInput。
type updateNicknameInput struct {
	Nickname string `json:"nickname" jsonschema:"description=New nickname (trimmed to 1 to 256 characters),required"`
}

// updateNicknameOutput 是改昵称的工具出参。
type updateNicknameOutput struct {
	ID       int64  `json:"id" jsonschema:"description=Updated user id"`
	Nickname string `json:"nickname" jsonschema:"description=Nickname after trim"`
}

// usersUpdateNickname 返回改昵称的工具处理函数（首批唯一 Confirm 写路径）：
// 定向更新只触碰 nickname/updated_by，其余可变字段由存储层 SET 白名单保证
// 不动；Tier=Confirm 由分级表声明，未确认前工具不会执行。
func usersUpdateNickname(users *biz.UserUsecase) func(context.Context, updateNicknameInput) (updateNicknameOutput, error) {
	return func(ctx context.Context, in updateNicknameInput) (updateNicknameOutput, error) {
		auth, err := identityOf(ctx)
		if err != nil {
			return updateNicknameOutput{}, err
		}
		nickname := strings.TrimSpace(in.Nickname)
		if nickname == "" {
			return updateNicknameOutput{}, fmt.Errorf("nickname is required")
		}
		if len(nickname) > maxNicknameChars {
			return updateNicknameOutput{}, fmt.Errorf("nickname must be at most %d characters", maxNicknameChars)
		}
		// 定向更新昵称：其余可变字段（email/phone/remark/locale）保持原值。
		updated, err := users.UpdateNickname(ctx, auth, auth.UserID, nickname)
		if err != nil {
			return updateNicknameOutput{}, err
		}
		return updateNicknameOutput{ID: updated.ID, Nickname: updated.Nickname}, nil
	}
}

// ptr 是标量取指针的小工具（dynq Pagination 字段为指针形态）。
func ptr[T any](v T) *T { return &v }
