# -*- coding: utf-8 -*-
# C2 合并冲突解决第二轮：agent 层 stack/provider（一次性脚本）

def resolve(path, repls):
    s = open(path, encoding='utf-8').read()
    for old, new in repls:
        assert old in s, (path, old[:80])
        s = s.replace(old, new)
    assert '<<<<<<<' not in s, path
    open(path, 'w', encoding='utf-8', newline='').write(s)
    print(path, 'OK')

# internal/agent/stack.go：NewStackWithOptions 签名 = users 之后同时接 grants 与 embedder。
# 统一顺序：(c, logger, sessions, vectors, users, grants, embedder, opts...)
resolve('internal/agent/stack.go', [
 ('''<<<<<<< HEAD
// embedder 供检索工具做查询向量化（向量设施未启用时为禁用占位实现，
// 必填校验只拒 nil——那是装配错误）。
func NewStackWithOptions(c *conf.Agent, logger *slog.Logger, sessions session.Service, vectors vectorstore.VectorStore, users *biz.UserUsecase, embedder biz.Embedder, opts ...StackOption) (*Stack, error) {
=======
// grants 是工具授权用例（C2 的许可存储：非 nil 时 ack 网关接入
// agent_tool_grant 表，remember 生效、吊销后重新确认；nil 仅供不触达
// 授权面的测试装配，维持 C1 的无许可源形态）。
func NewStackWithOptions(c *conf.Agent, logger *slog.Logger, sessions session.Service, vectors vectorstore.VectorStore, users *biz.UserUsecase, grants *biz.GrantUsecase, opts ...StackOption) (*Stack, error) {
>>>>>>> feat/grants-c2''',
  '''// grants 是工具授权用例（C2 的许可存储：非 nil 时 ack 网关接入
// agent_tool_grant 表，remember 生效、吊销后重新确认；nil 仅供不触达
// 授权面的测试装配，维持 C1 的无许可源形态）。embedder 供检索工具做
// 查询向量化（向量设施未启用时为禁用占位实现，必填校验只拒 nil——
// 那是装配错误）。
func NewStackWithOptions(c *conf.Agent, logger *slog.Logger, sessions session.Service, vectors vectorstore.VectorStore, users *biz.UserUsecase, grants *biz.GrantUsecase, embedder biz.Embedder, opts ...StackOption) (*Stack, error) {'''),
 ('''<<<<<<< HEAD
// 用户用例访问数据（TODO 决策 4），检索工具经向量适配器与 embedder
// 访问向量（TODO 决策 6/D2）；测试经 NewStackWithOptions 注入替身。
func NewStack(c *conf.Agent, logger *slog.Logger, sessions session.Service, vectors vectorstore.VectorStore, users *biz.UserUsecase, embedder biz.Embedder) (*Stack, func(), error) {
	st, err := NewStackWithOptions(c, logger, sessions, vectors, users, embedder)
=======
// 用户用例访问数据（TODO 决策 4），许可源经注入的授权用例落
// agent_tool_grant 表（C2）；测试经 NewStackWithOptions 注入替身。
func NewStack(c *conf.Agent, logger *slog.Logger, sessions session.Service, vectors vectorstore.VectorStore, users *biz.UserUsecase, grants *biz.GrantUsecase) (*Stack, func(), error) {
	st, err := NewStackWithOptions(c, logger, sessions, vectors, users, grants)
>>>>>>> feat/grants-c2''',
  '''// 用户用例访问数据（TODO 决策 4），许可源经注入的授权用例落
// agent_tool_grant 表（C2），检索工具经向量适配器与 embedder 访问向量
// （TODO 决策 6/D2）；测试经 NewStackWithOptions 注入替身。
func NewStack(c *conf.Agent, logger *slog.Logger, sessions session.Service, vectors vectorstore.VectorStore, users *biz.UserUsecase, grants *biz.GrantUsecase, embedder biz.Embedder) (*Stack, func(), error) {
	st, err := NewStackWithOptions(c, logger, sessions, vectors, users, grants, embedder)'''),
])
print('STACK SIGNATURE DONE — body 内 grants/embedder 装配段冲突下一轮看')
