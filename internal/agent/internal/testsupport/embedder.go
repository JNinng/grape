package testsupport

import "context"

// SearchTestEmbedder 是工具与骨架单测的替身 embedder：固定维度、记录
// 调用，可注入错误（失败路径用例）。实现 biz.Embedder 契约。
type SearchTestEmbedder struct {
	Dims  int
	Err   error
	Calls []string
}

func (s *SearchTestEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	s.Calls = append(s.Calls, text)
	vec := make([]float32, s.Dims)
	vec[0] = 1
	return vec, nil
}

func (s *SearchTestEmbedder) Dimensions() int { return s.Dims }
