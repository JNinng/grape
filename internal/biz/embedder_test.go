package biz

import (
	"context"
	"errors"
	"testing"
)

func TestEmbedderSpaceCarrier_when_injected_roundtrips_pair(t *testing.T) {
	ctx := WithEmbedderSpace(context.Background(), "bge-m3@v2", 1024)
	embedder, dimension, ok := EmbedderSpaceFromContext(ctx)
	if !ok {
		t.Fatal("EmbedderSpaceFromContext = missing, want injected pair")
	}
	if embedder != "bge-m3@v2" || dimension != 1024 {
		t.Fatalf("space = %s/%d, want bge-m3@v2/1024", embedder, dimension)
	}
}

func TestEmbedderSpaceCarrier_when_blank_or_nonpositive_not_injected(t *testing.T) {
	// 空白键或非正维度不注入：消费点 fail-closed，不伪造空间事实。
	for name, fn := range map[string]func(context.Context) context.Context{
		"blank embedder":     func(ctx context.Context) context.Context { return WithEmbedderSpace(ctx, "  ", 1024) },
		"zero dimension":     func(ctx context.Context) context.Context { return WithEmbedderSpace(ctx, "bge-m3", 0) },
		"negative dimension": func(ctx context.Context) context.Context { return WithEmbedderSpace(ctx, "bge-m3", -1) },
	} {
		if _, _, ok := EmbedderSpaceFromContext(fn(context.Background())); ok {
			t.Fatalf("carrier(%s) injected, want missing", name)
		}
	}
	if _, _, ok := EmbedderSpaceFromContext(context.Background()); ok {
		t.Fatal("carrier on bare context injected, want missing")
	}
}

func TestStubEmbedderBehavior_when_dimension_unknown_space_rejected(t *testing.T) {
	// 替身行为本身也是契约面：空空间键拒绝，未知键不可给出维度。
	embedder := &stubEmbedder{dims: 4}
	if _, err := embedder.Dimension(""); !errors.Is(err, ErrEmbedderUnknown) {
		t.Fatalf("Dimension(\"\") = %v, want ErrEmbedderUnknown", err)
	}
	dim, err := embedder.Dimension(testEmbedderSpace)
	if err != nil || dim != 4 {
		t.Fatalf("Dimension(%s) = %d/%v, want 4/nil", testEmbedderSpace, dim, err)
	}
}
