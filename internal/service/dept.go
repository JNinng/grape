package service

import (
	"context"

	orgv1 "grape/api/org/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DeptService 是部门资源的传输层适配器：DTO ↔ DO 转换、动态查询协议
// 转换，委托给 biz 用例，不实现业务规则。组织树是租户内资源，端点全部
// 受认证保护，租户边界与可见性由存储层 RLS 承担。
type DeptService struct {
	orgv1.UnimplementedDeptServiceServer
	uc *biz.DeptUsecase
}

// NewDeptService 构造部门服务。
func NewDeptService(uc *biz.DeptUsecase) *DeptService {
	return &DeptService{uc: uc}
}

// GetDeptTree 返回租户内全部部门的树形结构。
func (s *DeptService) GetDeptTree(ctx context.Context, req *orgv1.GetDeptTreeRequest) (*orgv1.DeptTreeReply, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	roots, total, err := s.uc.GetDeptTree(ctx, auth)
	if err != nil {
		return nil, err
	}
	return &orgv1.DeptTreeReply{
		Roots: toProtoDeptNodes(roots),
		Total: int64(total),
	}, nil
}

// CreateDept 创建部门。
func (s *DeptService) CreateDept(ctx context.Context, req *orgv1.CreateDeptRequest) (*orgv1.Dept, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetDept()
	if in == nil {
		return nil, biz.ErrDeptInvalidArgument
	}
	out, err := s.uc.CreateDept(ctx, auth, &biz.Dept{
		Status:           normalizeDeptStatus(in.Status),
		Name:             in.GetName(),
		ParentID:         in.ParentId,
		OwnerUserID:      in.GetOwnerUserId(),
		OwnerUserGroupID: in.OwnerUserGroupId,
	})
	if err != nil {
		return nil, err
	}
	return toProtoDept(out), nil
}

// GetDept 按 id 查询部门。
func (s *DeptService) GetDept(ctx context.Context, req *orgv1.GetDeptRequest) (*orgv1.Dept, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetDept(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoDept(out), nil
}

// ListDepts 按动态查询协议返回一页部门；协议非法映射为 INVALID_ARGUMENT。
func (s *DeptService) ListDepts(ctx context.Context, req *orgv1.ListDeptsRequest) (*orgv1.DeptSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrDeptInvalidArgument, err)
	}
	out, err := s.uc.ListDepts(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*orgv1.Dept, 0, len(out.Items))
	for _, d := range out.Items {
		items = append(items, toProtoDept(d))
	}
	return &orgv1.DeptSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateDept 覆盖可变字段（name、status、parent_id、owner_user_id、
// owner_user_group_id）；status 必填（optional 传达禁用语义），parent_id
// 未设置表示移为根部门，owner_user_group_id 未设置表示清空。
func (s *DeptService) UpdateDept(ctx context.Context, req *orgv1.UpdateDeptRequest) (*orgv1.Dept, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetDept()
	if in == nil || in.Status == nil {
		return nil, biz.ErrDeptInvalidArgument
	}
	out, err := s.uc.UpdateDept(ctx, auth, &biz.Dept{
		ID:               in.GetId(),
		Status:           *in.Status,
		Name:             in.GetName(),
		ParentID:         in.ParentId,
		OwnerUserID:      in.GetOwnerUserId(),
		OwnerUserGroupID: in.OwnerUserGroupId,
	})
	if err != nil {
		return nil, err
	}
	return toProtoDept(out), nil
}

// MoveDept 移动部门到新父级；parent_id 未设置表示移为根部门。
func (s *DeptService) MoveDept(ctx context.Context, req *orgv1.MoveDeptRequest) (*orgv1.Dept, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.MoveDept(ctx, auth, req.GetId(), req.ParentId)
	if err != nil {
		return nil, err
	}
	return toProtoDept(out), nil
}

// DeleteDept 软删除部门。
func (s *DeptService) DeleteDept(ctx context.Context, req *orgv1.DeleteDeptRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteDept(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// toProtoDept 将领域模型转换为 DTO。
func toProtoDept(d *biz.Dept) *orgv1.Dept {
	return &orgv1.Dept{
		Id:               d.ID,
		Status:           &d.Status,
		Name:             d.Name,
		ParentId:         d.ParentID,
		OwnerUserId:      d.OwnerUserID,
		OwnerUserGroupId: d.OwnerUserGroupID,
		CreatedBy:        d.CreatedBy,
		UpdatedBy:        d.UpdatedBy,
		CreatedAt:        timestamppb.New(d.CreatedAt),
		UpdatedAt:        tsPBPtr(d.UpdatedAt),
		DeletedAt:        tsPBPtr(d.DeletedAt),
	}
}

// toProtoDeptNodes 将领域树节点转换为 DTO 树节点（保持既有顺序）。
func toProtoDeptNodes(roots []*biz.DeptNode) []*orgv1.DeptTreeNode {
	out := make([]*orgv1.DeptTreeNode, 0, len(roots))
	for _, node := range roots {
		out = append(out, &orgv1.DeptTreeNode{
			Dept:     toProtoDept(node.Dept),
			Children: toProtoDeptNodes(node.Children),
		})
	}
	return out
}

// normalizeDeptStatus 归一化创建入参的状态：optional 的缺失映射为 biz 的
// 未指定哨兵（用例落缺省启用）；显式取值原样透传——含 0=禁用，不得并入
// 缺省分支。
func normalizeDeptStatus(status *int32) int32 {
	if status == nil {
		return biz.DeptStatusUnspecified
	}
	return *status
}
