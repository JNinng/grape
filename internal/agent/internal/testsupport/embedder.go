package testsupport

import (
	"context"

	"grape/internal/biz"
)

// SearchTestSpace 是工具与骨架单测的固定嵌入空间键（SearchTestResolver
// 解析产出）。
const SearchTestSpace = "bge-m3@v1"

// SearchTestEmbedder 是工具与骨架单测的替身 embedder：固定维度、记录
// 调用（含空间键），可注入错误（失败路径用例）。实现 biz.Embedder 契约。
type SearchTestEmbedder struct {
	Dims   int
	Err    error
	Calls  []string
	Spaces []string
}

func (s *SearchTestEmbedder) Embed(_ context.Context, embedder string, text string) ([]float32, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	s.Calls = append(s.Calls, text)
	s.Spaces = append(s.Spaces, embedder)
	vec := make([]float32, s.Dims)
	vec[0] = 1
	return vec, nil
}

func (s *SearchTestEmbedder) Dimension(embedder string) (int, error) {
	if embedder == "" {
		return 0, biz.ErrEmbedderUnknown
	}
	return s.Dims, nil
}

// SearchTestResolver 是工具与骨架单测的替身默认空间解析器：固定解析
// SearchTestSpace/Dims，可注入错误。实现 biz.EmbedderResolver 契约。
type SearchTestResolver struct {
	Dims int
	Err  error
}

func (r *SearchTestResolver) ResolveDefaultEmbedder(context.Context) (string, int, error) {
	if r.Err != nil {
		return "", 0, r.Err
	}
	return SearchTestSpace, r.Dims, nil
}
