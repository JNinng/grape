package service

import (
	"context"
	"io"
	nethttp "net/http"
	"path"
	"strconv"
	"strings"
	"time"

	knowledgev1 "grape/api/knowledge/v1"
	"grape/internal/biz"

	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
)

// 文件上传端点（POST /v1/knowledge/documents/upload）的边界：
//   - maxUploadRequestBytes 是整个请求体上限（单文件上限 + multipart
//     编码开销余量），由 MaxBytesReader 在解析期强制，超限请求不落盘、
//     不进内存；
//   - maxUploadMemoryBytes 是 multipart 解析的内存缓冲上限（超出部分
//     spool 到临时文件，读取即限流，不放大驻留）。
const (
	knowledgeUploadPath   = "/v1/knowledge/documents/upload"
	maxUploadRequestBytes = biz.MaxFileBytes + 1<<20
	maxUploadMemoryBytes  = 8 << 20
)

// RegisterHTTP 把文件上传路由注册到 Kratos HTTP 服务器。multipart 上传
// 无 proto 契约（生成路由的 JSON body 编解码不承载表单），按业务手写
// 端点落地：操作名即路径模板，认证与功能权限（sys:knowledge:create）
// 中间件按它判定，缺令牌 fail-closed 401；路由级超时按向量化路由预算
// 计（解析与逐块向量化同步串联，server 的 embeddingOperations 登记）。
// 响应正文以 protojson 渲染 Document（与生成端点的 protojson 序列化
// 口径一致），错误沿 Kratos 错误编码出口（与生成端点同形）。
func (s *KnowledgeService) RegisterHTTP(srv *kratoshttp.Server) {
	route := srv.Route("/")
	route.Handle(nethttp.MethodPost, knowledgeUploadPath, s.routeUpload)
}

// routeUpload 把上传 handler 包成 Kratos 的 HandlerFunc：先经
// ctx.Middleware 入链（身份注入认证 ctx、路由级超时生效），再把链上
// 产出的 ctx 回绑到请求上交给上传实现；成功手写 protojson 响应。
func (s *KnowledgeService) routeUpload(ctx kratoshttp.Context) error {
	inner := func(mctx context.Context, _ any) (any, error) {
		return s.uploadDocument(ctx.Request().WithContext(mctx))
	}
	out, err := ctx.Middleware(middleware.Handler(inner))(ctx, nil)
	if err != nil {
		return err
	}
	doc, ok := out.(*knowledgev1.Document)
	if !ok || doc == nil {
		return nil
	}
	payload, err := protojson.Marshal(doc)
	if err != nil {
		return err
	}
	w := ctx.Response()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, err = w.Write(payload)
	return err
}

// uploadDocument 处理 multipart 上传：解析表单与文件字节、委托用例、
// 转换响应。表单形态错误统一 DOCUMENT_INVALID_ARGUMENT（400）；业务
// 错误由用例成型（FILE_TYPE_UNSUPPORTED / DOCUMENT_INDEX_FAILED）。
func (s *KnowledgeService) uploadDocument(r *nethttp.Request) (*knowledgev1.Document, error) {
	auth, err := authOf(r.Context())
	if err != nil {
		return nil, err
	}
	in, err := knowledgeUploadInputOf(r)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.UploadDocument(r.Context(), auth, in)
	if err != nil {
		return nil, err
	}
	return toProtoDocument(out), nil
}

// knowledgeUploadInputOf 解析 multipart 表单为上传入参：file 字段携带
// 文件字节（文件名保留原名，识别不信任其声明），libraryId/title/status/
// sourceName/validFrom/validTo 字段名与 protojson 命名一致；表单值形态
// 错误与必填缺失统一按入参非法拒绝。multipart 临时文件在返回前清理。
func knowledgeUploadInputOf(r *nethttp.Request) (*biz.KnowledgeFileUpload, error) {
	invalid := func() (*biz.KnowledgeFileUpload, error) {
		return nil, biz.ErrDocumentInvalidArgument
	}
	r.Body = nethttp.MaxBytesReader(nil, r.Body, maxUploadRequestBytes)
	if err := r.ParseMultipartForm(maxUploadMemoryBytes); err != nil {
		return invalid()
	}
	defer func() {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
	}()
	file, header, err := r.FormFile("file")
	if err != nil {
		return invalid()
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, biz.MaxFileBytes+1))
	if err != nil {
		return invalid()
	}
	if len(payload) == 0 || int64(len(payload)) > biz.MaxFileBytes {
		return invalid()
	}
	libraryID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("libraryId")), 10, 64)
	if err != nil || libraryID <= 0 {
		return invalid()
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = knowledgeUploadTitleOf(header.Filename)
	}
	if title == "" {
		return invalid()
	}
	in := &biz.KnowledgeFileUpload{
		LibraryID: libraryID,
		Title:     title,
		FileName:  header.Filename,
		Body:      payload,
		Status:    biz.KnowledgeStatusUnspecified,
	}
	if v := r.FormValue("sourceName"); v != "" {
		in.SourceName = &v
	}
	if v := strings.TrimSpace(r.FormValue("status")); v != "" {
		status, err := strconv.Atoi(v)
		if err != nil || (status != int(biz.KnowledgeStatusDisabled) && status != int(biz.KnowledgeStatusEnabled)) {
			return invalid()
		}
		in.Status = int32(status)
	}
	if in.ValidFrom, err = knowledgeUploadTimeOf(r, "validFrom"); err != nil {
		return invalid()
	}
	if in.ValidTo, err = knowledgeUploadTimeOf(r, "validTo"); err != nil {
		return invalid()
	}
	return in, nil
}

// knowledgeUploadTitleOf 推导缺省标题：显式 title 缺失时取原件文件名去
// 扩展名的形态（隐藏文件等去后为空时保留原名）。
func knowledgeUploadTitleOf(name string) string {
	name = strings.TrimSpace(name)
	ext := path.Ext(name)
	if title := strings.TrimSpace(strings.TrimSuffix(name, ext)); title != "" {
		return title
	}
	return name
}

// knowledgeUploadTimeOf 解析可选的 RFC3339 时间表单值；缺失返回 nil。
func knowledgeUploadTimeOf(r *nethttp.Request, key string) (*time.Time, error) {
	v := strings.TrimSpace(r.FormValue(key))
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
