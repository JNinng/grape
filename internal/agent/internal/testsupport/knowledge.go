package testsupport

import (
	"context"

	"grape/internal/biz"
)

// 知识检索的骨架级替身：完整栈的装配测试（确认流、审计、并发门控等）
// 不需要真实向量设施，但 NewStackWithOptions 要求检索用例非 nil——本文件
// 提供最小可装配形态，检索恒无命中、可见库集合为空，不触达数据库。

// StackVectorStore 是骨架测试用的向量存储替身：只实现 Search（恒无命中），
// 其余方法由嵌入的 nil 契约承担（用例不调用即不 panic）。
type StackVectorStore struct {
	biz.VectorStore
}

func (StackVectorStore) Search(context.Context, []float32, *biz.VectorFilter) ([]*biz.ScoredVectorDocument, error) {
	return nil, nil
}

// EmptyLibraryDirectory 是骨架测试用的可见库集合替身：恒无可见库。
type EmptyLibraryDirectory struct{}

func (EmptyLibraryDirectory) VisibleLibraries(context.Context, biz.Identity) ([]*biz.KnowledgeLibrary, error) {
	return nil, nil
}

// NewStackKnowledgeSearch 构造可挂进测试栈的检索用例（替身向量存储 +
// 测试 embedder/resolver + 空可见库集合）。
func NewStackKnowledgeSearch() *biz.KnowledgeSearchUsecase {
	uc, err := biz.NewKnowledgeSearchUsecase(
		StackVectorStore{},
		&SearchTestEmbedder{Dims: 4},
		&SearchTestResolver{Dims: 4},
		nil, // 无重排层：测试栈不装配 reranker
		EmptyLibraryDirectory{},
	)
	if err != nil {
		panic("testsupport: build stack knowledge search usecase: " + err.Error())
	}
	return uc
}
