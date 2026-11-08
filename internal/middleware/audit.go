package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"grape/internal/biz"
	"grape/pkg/redact"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// OperationAuditMiddleware 是管理操作写审计的注入类型：对受控管理端点
// 的状态变更请求追加 sys_operation_log 行。与权限中间件同为命名类型，
// 供 Wire 按类型装配。
type OperationAuditMiddleware middleware.Middleware

// AuditUsernameResolver 解析认证用户的冗余用户名（写操作日志的 username
// 列）。身份事实只含租户与用户 ID，用户名由装配层注入的查询补齐；解析
// 失败不阻断操作，审计行回落空串。
type AuditUsernameResolver func(ctx context.Context, auth biz.Identity) (string, error)

// NewOperationAuditMiddleware 构造操作审计中间件。必须在认证中间件之后、
// 权限中间件之前入链：身份已成立，权限拒绝（403）也会被记录。读请求
// （List/Get/Query 方法）与 agent 业务面不入账；其余管理写操作按方法名
// 推导 operation_type 与 target_table。审计写入是 best-effort：失败只记
// 日志，不改变业务响应。
func NewOperationAuditMiddleware(
	audit *biz.AuditLogUsecase,
	resolveUsername AuditUsernameResolver,
) OperationAuditMiddleware {
	return OperationAuditMiddleware(func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if audit == nil {
				return handler(ctx, req)
			}
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}
			meta, gated := operationAuditMetaOf(tr.Operation())
			if !gated {
				return handler(ctx, req)
			}
			auth, ok := biz.IdentityFromContext(ctx)
			if !ok {
				// 公开端点（登录/刷新/登出）不经过认证中间件注入身份；
				// 登录日志由登录用例单点写入。
				return handler(ctx, req)
			}

			username := ""
			if resolveUsername != nil {
				if name, err := resolveUsername(ctx, auth); err == nil {
					username = name
				}
			}

			start := time.Now()
			reply, err := handler(ctx, req)
			durationMs := int32(time.Since(start).Milliseconds())
			recordOperationAudit(ctx, audit, auth, username, meta, tr, durationMs, err, req, reply)
			return reply, err
		}
	})
}

// operationAuditMeta 是操作日志的类型与目标表推导结果。
type operationAuditMeta struct {
	OperationType string
	TargetTable   string
}

// operationAuditMetaOf 从 Kratos operation（"/pkg.Service/Method"）推导
// 审计元数据。返回 false 表示该操作不入操作日志：agent 业务面、读方法
// （List/Get/Query 前缀或包含）以及无法映射到管理资源的端点。
func operationAuditMetaOf(operation string) (operationAuditMeta, bool) {
	service, method := splitAuditOperation(operation)
	if service == "" || method == "" {
		return operationAuditMeta{}, false
	}
	if strings.Contains(service, "Agent") {
		return operationAuditMeta{}, false
	}
	if strings.HasPrefix(method, "List") || strings.HasPrefix(method, "Get") || strings.Contains(method, "Query") {
		return operationAuditMeta{}, false
	}
	table := operationTargetTable(service, method)
	if table == "" {
		return operationAuditMeta{}, false
	}
	return operationAuditMeta{OperationType: operationTypeOf(method), TargetTable: table}, true
}

// splitAuditOperation 拆分 operation 为服务与 RPC 方法名；无斜杠时原样
// 作为方法名（兼容手写 operation）。
func splitAuditOperation(operation string) (service, method string) {
	if i := strings.LastIndex(operation, "/"); i >= 0 {
		return operation[:i], operation[i+1:]
	}
	return "", operation
}

// operationTypeOf 按方法名归类操作类型：Create→CREATE，Delete/Revoke→
// DELETE，其余写操作（Update/Set/Move/Assign/Transfer/Reset/Disable/
// Restore/Add/Remove）→UPDATE。
func operationTypeOf(method string) string {
	switch {
	case strings.HasPrefix(method, "Create"):
		return "CREATE"
	case strings.HasPrefix(method, "Delete"), strings.HasPrefix(method, "Revoke"):
		return "DELETE"
	default:
		return "UPDATE"
	}
}

// operationTargetTable 按服务名映射目标表；字典按方法名区分类型/数据两级。
// 无法映射的服务（权限查询、查询协议、会话认证等）返回空串，不入账。
func operationTargetTable(service, method string) string {
	switch {
	case strings.Contains(service, "UserGroupService"):
		return "sys_user_group"
	case strings.Contains(service, "UserService"):
		return "sys_user"
	case strings.Contains(service, "DeptService"):
		return "sys_dept"
	case strings.Contains(service, "RoleService"):
		return "sys_role"
	case strings.Contains(service, "MenuService"):
		return "sys_menu"
	case strings.Contains(service, "DictService"):
		if strings.Contains(method, "DictData") {
			return "sys_dict_data"
		}
		return "sys_dict_type"
	case strings.Contains(service, "TenantConfigService"):
		return "sys_tenant_config"
	case strings.Contains(service, "ConfigService"):
		return "sys_config"
	case strings.Contains(service, "FeatureFlagService"):
		return "sys_feature_flag"
	case strings.Contains(service, "TranslationService"):
		return "sys_translation"
	case strings.Contains(service, "TenantService"):
		return "sys_tenant"
	case strings.Contains(service, "SessionAdminService"):
		return "sys_session"
	default:
		return ""
	}
}

// recordOperationAudit 组装并追加一条操作日志；错误只记日志，不影响响应。
func recordOperationAudit(
	ctx context.Context,
	audit *biz.AuditLogUsecase,
	auth biz.Identity,
	username string,
	meta operationAuditMeta,
	tr transport.Transporter,
	durationMs int32,
	handlerErr error,
	req, reply any,
) {
	apiPath, method, ip, userAgent, requestID := operationAuditRequestFacts(ctx, tr)
	snapshot := operationAuditSnapshot(meta, req, reply)
	resultCode := "200"
	resultMsg := ""
	if handlerErr != nil {
		e := kratoserrors.FromError(handlerErr)
		resultCode = strconv.FormatInt(int64(e.Code), 10)
		resultMsg = truncateAuditRunes(e.Message, 512)
		if resultMsg == "" {
			resultMsg = truncateAuditRunes(handlerErr.Error(), 512)
		}
	}
	in := &biz.OperationLogInput{
		Username:      truncateAuditRunes(username, 256),
		OperationType: meta.OperationType,
		TargetTable:   auditStringPtr(meta.TargetTable),
		TargetID:      auditStringPtr(snapshot.TargetID),
		OldValue:      nil,
		NewValue:      snapshot.NewValue,
		IPAddress:     auditStringPtr(truncateAuditRunes(ip, 64)),
		UserAgent:     auditStringPtr(truncateAuditRunes(userAgent, 512)),
		APIPath:       auditStringPtr(truncateAuditRunes(apiPath, 512)),
		HTTPMethod:    auditStringPtr(truncateAuditRunes(method, 16)),
		DurationMs:    auditInt32Ptr(durationMs),
		ResultCode:    auditStringPtr(truncateAuditRunes(resultCode, 32)),
		ResultMsg:     auditStringPtr(resultMsg),
		RequestID:     auditStringPtr(truncateAuditRunes(requestID, 64)),
	}
	if err := audit.RecordOperation(ctx, auth, in); err != nil {
		slog.WarnContext(ctx, "operation audit write failed",
			"tenant_id", auth.TenantID, "user_id", auth.UserID, "operation", meta.OperationType,
			"target_table", meta.TargetTable, "err", err)
	}
}

// operationAuditRequestFacts 提取请求侧审计事实：HTTP 取真实路径/方法/
// 对端地址/User-Agent；gRPC 取服务端点/GRPC/peer 地址/user-agent 元数据。
// request_id 优先上游头，缺省回落链路 trace-id（TraceMiddleware 为每个
// 请求生成）。
func operationAuditRequestFacts(ctx context.Context, tr transport.Transporter) (apiPath, method, ip, userAgent, requestID string) {
	if htr, ok := tr.(interface{ Request() *nethttp.Request }); ok && htr.Request() != nil {
		r := htr.Request()
		apiPath = r.URL.Path
		method = r.Method
		ip = auditHost(r.RemoteAddr)
		userAgent = r.UserAgent()
		requestID = r.Header.Get("x-request-id")
	} else {
		apiPath = tr.Endpoint()
		method = "GRPC"
		userAgent = tr.RequestHeader().Get("user-agent")
		if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
			ip = auditHost(p.Addr.String())
		}
	}
	if requestID == "" {
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			requestID = sc.TraceID().String()
		}
	}
	return apiPath, method, ip, userAgent, requestID
}

// auditHost 去掉 host:port 的端口；解析失败原样返回。
func auditHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// truncateAuditRunes 按字符截断到列宽上限；空串返回空。
func truncateAuditRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func auditStringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func auditInt32Ptr(v int32) *int32 {
	return &v
}

// operationAuditData 是请求/响应 proto 的审计快照：target_id 取标识字段，
// new_value 取响应实体（失败或无响应时回落请求载荷）；old_value 不在通用
// 层采集，保持 NULL。敏感键落库前统一脱敏（口令固定掩码、其余无盐哈希）。
type operationAuditData struct {
	TargetID string
	NewValue json.RawMessage
}

// auditSnapshotRedactor 对 protojson 文本做键名脱敏；键名比较大小写
// 不敏感，覆盖 protojson 的 lowerCamelCase 与数据库列名两种形态。
var auditSnapshotRedactor = mustAuditSnapshotRedactor()

// auditConfigSnapshotRedactor 额外脱敏配置族的 value 列：sys_config /
// sys_tenant_config 的取值可能是 SMTP 口令、JWT 密钥等敏感配置。
var auditConfigSnapshotRedactor = mustAuditConfigSnapshotRedactor()

func mustAuditSnapshotRedactor() redact.Redactor {
	return newAuditKeyFilter(false)
}

func mustAuditConfigSnapshotRedactor() redact.Redactor {
	return newAuditKeyFilter(true)
}

// auditPasswordMask 是密码类字段的固定掩码：口令与口令哈希不做哈希，
// 一律以固定星号替换，避免任何派生值进入审计快照。
const auditPasswordMask = "***"

func newAuditKeyFilter(redactValue bool) redact.Redactor {
	passwordFilter, err := redact.NewKeyFilter([]string{
		"password", "oldpassword", "newpassword", "passwordhash", "passwd",
	}, redact.NewMask(auditPasswordMask))
	if err != nil {
		panic("middleware: build audit password redactor: " + err.Error())
	}
	hashKeys := []string{
		"token", "tokenhash", "refreshtokenhash", "accesstokenhash",
		"secret", "clientsecret", "privatekey",
	}
	if redactValue {
		hashKeys = append(hashKeys, "value")
	}
	hashFilter, err := redact.NewKeyFilter(hashKeys, redact.NewHash(""))
	if err != nil {
		panic("middleware: build audit hash redactor: " + err.Error())
	}
	return auditCompositeRedactor{first: passwordFilter, second: hashFilter}
}

// auditCompositeRedactor 先执行密码固定掩码，再执行其他敏感键的无盐
// 确定性哈希。两轮键集互斥，第二轮的 JSON 重编码不会改动已掩码的值。
type auditCompositeRedactor struct {
	first  redact.Redactor
	second redact.Redactor
}

func (r auditCompositeRedactor) Redact(value string) string {
	return r.second.Redact(r.first.Redact(value))
}

func operationAuditSnapshot(meta operationAuditMeta, req, reply any) operationAuditData {
	snapshot := operationAuditData{TargetID: operationAuditTargetID(meta.TargetTable, reply, req)}
	if meta.OperationType == "DELETE" {
		return snapshot
	}
	if value := auditMessageJSON(meta.TargetTable, reply); value != nil {
		snapshot.NewValue = value
		return snapshot
	}
	snapshot.NewValue = auditMessageJSON(meta.TargetTable, req)
	return snapshot
}

// operationAuditTargetID 先查响应再查请求：创建操作的 id 在响应实体里，
// 更新/删除操作在请求里。
func operationAuditTargetID(table string, reply, req any) string {
	for _, candidate := range []any{reply, req} {
		msg, ok := candidate.(proto.Message)
		if !ok || msg == nil {
			continue
		}
		if id := auditMessageID(msg, table); id != "" {
			return id
		}
	}
	return ""
}

// auditMessageID 按目标表的标识字段候选取值；顶层未命中时向下查找内嵌
// 实体（如 UpdateUserRequest.user.id、CreateUserReply 的实体）的 id。
func auditMessageID(msg proto.Message, table string) string {
	pm := msg.ProtoReflect()
	if !pm.IsValid() {
		return ""
	}
	for _, name := range auditIDFieldNames(table) {
		if id := auditFieldID(pm, protoreflect.Name(name)); id != "" {
			return id
		}
	}
	return auditNestedID(pm, 0)
}

func auditIDFieldNames(table string) []string {
	switch table {
	case "sys_user":
		return []string{"user_id", "id"}
	case "sys_dept":
		return []string{"dept_id", "id"}
	case "sys_user_group":
		return []string{"group_id", "id"}
	case "sys_role":
		return []string{"role_id", "id"}
	case "sys_menu":
		return []string{"menu_id", "id"}
	case "sys_dict_type":
		return []string{"type_id", "dict_type_id", "id"}
	case "sys_dict_data":
		return []string{"data_id", "dict_data_id", "id"}
	case "sys_config":
		return []string{"config_id", "id"}
	case "sys_tenant_config":
		return []string{"config_id", "id"}
	case "sys_feature_flag":
		return []string{"flag_id", "id"}
	case "sys_translation":
		return []string{"translation_id", "id"}
	case "sys_tenant":
		return []string{"tenant_id", "id"}
	case "sys_session":
		return []string{"session_id", "id"}
	default:
		return []string{"id"}
	}
}

func auditFieldID(pm protoreflect.Message, name protoreflect.Name) string {
	fields := pm.Descriptor().Fields()
	fd := fields.ByName(name)
	if fd == nil {
		fd = fields.ByJSONName(string(name))
	}
	if fd == nil || fd.IsMap() || fd.Kind() == protoreflect.MessageKind {
		return ""
	}
	if fd.IsList() {
		list := pm.Get(fd).List()
		if list.Len() == 0 {
			return ""
		}
		return auditScalarID(list.Get(0))
	}
	if !pm.Has(fd) {
		return ""
	}
	return auditScalarID(pm.Get(fd))
}

func auditScalarID(value protoreflect.Value) string {
	switch v := value.Interface().(type) {
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case string:
		return v
	default:
		return ""
	}
}

func auditNestedID(pm protoreflect.Message, depth int) string {
	const maxDepth = 3
	if depth >= maxDepth {
		return ""
	}
	fields := pm.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() {
			continue
		}
		if !pm.Has(fd) {
			continue
		}
		child := pm.Get(fd).Message()
		if !child.IsValid() {
			continue
		}
		if id := auditFieldID(child, "id"); id != "" {
			return id
		}
		if id := auditNestedID(child, depth+1); id != "" {
			return id
		}
	}
	return ""
}

func auditMessageJSON(table string, value any) json.RawMessage {
	msg, ok := value.(proto.Message)
	if !ok || msg == nil {
		return nil
	}
	raw, err := protojson.Marshal(msg)
	if err != nil || len(raw) == 0 || string(raw) == "{}" {
		return nil
	}
	redactor := auditSnapshotRedactor
	if table == "sys_config" || table == "sys_tenant_config" {
		redactor = auditConfigSnapshotRedactor
	}
	redacted := redactor.Redact(string(raw))
	if redacted == "" || redacted == "{}" {
		return nil
	}
	return json.RawMessage(redacted)
}
