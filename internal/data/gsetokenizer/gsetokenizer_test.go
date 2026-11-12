package gsetokenizer

import (
	"reflect"
	"testing"
)

// 分词设施单元测试（纯内存，词典经 embed 装载）：确定性、空白文本、
// 中英混合归一。同文本两侧 token 序列一致是摄取与查询口径一致的
// 正确性前提，这里以同一实例重复调用断言。

// newTokenizerForTest 构造被测分词器（词典装载失败即终止测试）。
func newTokenizerForTest(t *testing.T) *Tokenizer {
	t.Helper()
	tk, cleanup, err := New()
	if err != nil {
		t.Fatalf("New = %v, want nil error", err)
	}
	t.Cleanup(cleanup)
	return tk.(*Tokenizer)
}

func TestTokenizer_same_text_returns_deterministic_sequence(t *testing.T) {
	tk := newTokenizerForTest(t)
	text := "年假申请流程是怎样的？"

	first := tk.Tokens(text)
	second := tk.Tokens(text)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated calls differ: %q vs %q", first, second)
	}
	want := []string{"年", "假", "申请", "流程"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("tokens = %q, want %q", first, want)
	}
}

func TestTokenizer_blank_text_returns_empty_slice(t *testing.T) {
	tk := newTokenizerForTest(t)
	for _, text := range []string{"", "   ", " \t\n "} {
		got := tk.Tokens(text)
		if got == nil || len(got) != 0 {
			t.Fatalf("Tokens(%q) = %#v, want empty non-nil slice", text, got)
		}
	}
}

func TestTokenizer_mixed_cjk_english_lowercases_and_drops_stops(t *testing.T) {
	tk := newTokenizerForTest(t)

	// 英文统一小写，中文按词典切分，停用词（的/是/标点）不产出；
	// 同一序列同时是写入侧建索引与查询侧分词的产物。
	got := tk.Tokens("Grape 是一个 Go 后端框架，支持 RBAC 权限。")
	want := []string{"grape", "一个", "go", "后端", "框架", "支持", "rbac", "权限"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %q, want %q", got, want)
	}

	// 单引号与反斜杠是合法词元的一部分：保留原样输出，转义是存储层
	// 构造 tsvector/tsquery 字面量的职责，分词器不改动词元内容。
	got = tk.Tokens("包含单引号'与反斜杠\\的文本")
	want = []string{"包含", "单引号", "'", "反", "斜杠", "文本"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %q, want %q", got, want)
	}
}
