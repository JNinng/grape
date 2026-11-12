// Package gsetokenizer 是 biz.Tokenizer 的纯 Go 分词实现（go-ego/gse），
// 中文为第一目标。词典与停用词表以 embed 方式进二进制（运行期不依赖外部
// 词典文件）；切分走 DAG 无 HMM 路径，同文本恒同序列（确定性是摄取与
// 查询两侧口径一致的前提）。词元统一小写、去首尾空白并过滤停用词，写入
// 侧建索引与查询侧分词以同一实例、同一归一口径产出。
//
// 本包是 data 层能力目录：构造返回 cleanup、注册进 data 的 ProviderSet，
// 经 wire 与向量仓储共用同一 biz.Tokenizer 实例。
package gsetokenizer

import (
	"fmt"
	"strings"

	"grape/internal/biz"

	"github.com/go-ego/gse"
	"github.com/google/wire"
)

// ProviderSet 是分词设施的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(New)

// Tokenizer 是 biz.Tokenizer 的 gse 实现：持有一个词典已装载的分词器
// 实例。gse.Segmenter 装载后只读（Cut/IsStop 不变异内部状态），可并发
// 复用；实例由 wire 单例供给，摄取与查询两侧共享。
type Tokenizer struct {
	seg *gse.Segmenter
}

var _ biz.Tokenizer = (*Tokenizer)(nil)

// New 构造分词设施：词典与停用词表经 embed 装载（无外部文件依赖），
// 词典装载失败在装配期显式报错，不带病启动。
func New() (biz.Tokenizer, func(), error) {
	var seg gse.Segmenter
	if err := seg.LoadDictEmbed(); err != nil {
		return nil, nil, fmt.Errorf("gsetokenizer: load embedded dictionary: %w", err)
	}
	if err := seg.LoadStopEmbed(); err != nil {
		return nil, nil, fmt.Errorf("gsetokenizer: load embedded stop words: %w", err)
	}
	return &Tokenizer{seg: &seg}, func() {}, nil
}

// Tokens 实现 biz.Tokenizer：DAG 无 HMM 切分（确定性路径），词元去首尾
// 空白、小写归一并过滤停用词；空白文本返回空切片。同文本重复调用产出
// 同一序列。
func (t *Tokenizer) Tokens(text string) []string {
	if strings.TrimSpace(text) == "" {
		return []string{}
	}
	words := t.seg.Cut(text, false)
	out := make([]string, 0, len(words))
	for _, w := range words {
		token := strings.ToLower(strings.TrimSpace(w))
		if token == "" || t.seg.IsStop(token) {
			continue
		}
		out = append(out, token)
	}
	return out
}
