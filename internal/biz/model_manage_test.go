package biz

import (
	"context"
	"errors"
	"testing"
)

// 模型目录用例的入参校验单测：嵌入维度本期固定 1024、model_key 白名单
// 允许冒号、文本模型不接受维度、更新路径必填状态。更新路径另以手写假
// 仓储覆盖「类别以锁定行为准」的防御纵深。

// fakeModelRepo 是 ModelRepo 的手写假实现：只记录更新路径的入参与写库
// 对象，其余方法按未使用返回零值。
type fakeModelRepo struct {
	locked     *ModelCfg
	lockCipher string
	lockErr    error

	lockedID   int64
	lockedAuth Identity

	updated      *ModelCfg
	updateCipher string
	updateErr    error

	platformUpdated      *ModelCfg
	platformUpdateCipher string
	platformUpdateErr    error
}

func (r *fakeModelRepo) GetModel(context.Context, Identity, int64) (*ModelCfg, error) {
	return nil, ErrModelNotFound
}

func (r *fakeModelRepo) ListModels(context.Context, Identity, string) ([]*ModelCfg, error) {
	return nil, nil
}

func (r *fakeModelRepo) ListModelOptions(context.Context, Identity, string) ([]*ModelCfg, error) {
	return nil, nil
}

func (r *fakeModelRepo) LockModel(_ context.Context, auth Identity, id int64) (*ModelCfg, string, error) {
	r.lockedID = id
	r.lockedAuth = auth
	if r.lockErr != nil {
		return nil, "", r.lockErr
	}
	return r.locked, r.lockCipher, nil
}

func (r *fakeModelRepo) CreateModel(_ context.Context, _ Identity, m *ModelCfg, _ string) (*ModelCfg, error) {
	return m, nil
}

func (r *fakeModelRepo) UpdateModel(_ context.Context, _ Identity, m *ModelCfg, cipher string) (*ModelCfg, error) {
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	r.updated = m
	r.updateCipher = cipher
	return m, nil
}

func (r *fakeModelRepo) DeleteModel(context.Context, Identity, int64) error {
	return ErrModelNotFound
}

func (r *fakeModelRepo) CreateModelPlatform(_ context.Context, _ Identity, m *ModelCfg, _ string) (*ModelCfg, error) {
	return m, nil
}

func (r *fakeModelRepo) UpdateModelPlatform(_ context.Context, _ Identity, m *ModelCfg, cipher string) (*ModelCfg, error) {
	if r.platformUpdateErr != nil {
		return nil, r.platformUpdateErr
	}
	r.platformUpdated = m
	r.platformUpdateCipher = cipher
	return m, nil
}

func (r *fakeModelRepo) DeleteModelPlatform(context.Context, Identity, int64) error {
	return ErrModelNotFound
}

// fakeSecretCipher 是 SecretCipher 的确定性假实现：Seal 结果可预期，
// 并记录入参供断言路径是否执行了加密。
type fakeSecretCipher struct {
	sealed []string
}

func (c *fakeSecretCipher) Seal(plaintext string) (string, error) {
	c.sealed = append(c.sealed, plaintext)
	return "sealed:" + plaintext, nil
}

func (c *fakeSecretCipher) Open(ciphertext string) (string, error) { return ciphertext, nil }

func ptrInt32(v int32) *int32 { return &v }

func validEmbeddingModel() *ModelCfg {
	return &ModelCfg{
		Status:    ModelStatusUnspecified,
		Kind:      ModelKindEmbedding,
		Name:      "Embedding",
		ModelKey:  "qwen3-embedding:0.6b",
		Provider:  "openai",
		Model:     "qwen3-embedding:0.6b",
		Dimension: ptrInt32(ModelEmbeddingDimension),
	}
}

func TestValidateModelCfg_embedding_dimension_is_1024_only(t *testing.T) {
	if err := validateModelCfg(validEmbeddingModel(), false); err != nil {
		t.Fatalf("valid embedding model rejected: %v", err)
	}
	for _, dim := range []int32{768, 1536, 0} {
		m := validEmbeddingModel()
		m.Dimension = ptrInt32(dim)
		if err := validateModelCfg(m, false); !errors.Is(err, ErrModelInvalidArgument) {
			t.Fatalf("embedding dimension %d = %v, want ErrModelInvalidArgument", dim, err)
		}
	}
	missing := validEmbeddingModel()
	missing.Dimension = nil
	if err := validateModelCfg(missing, false); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("embedding without dimension = %v, want ErrModelInvalidArgument", err)
	}
}

func TestValidateModelCfg_model_key_whitelist_and_text_dimension(t *testing.T) {
	// 冒号承载 Ollama 标签，属合法键字符。
	m := validEmbeddingModel()
	m.ModelKey = "qwen3-embedding:0.6b"
	if err := validateModelCfg(m, false); err != nil {
		t.Fatalf("colon in model key rejected: %v", err)
	}
	// 斜杠与空格不在白名单内。
	for _, key := range []string{"bad/key", "bad key", ""} {
		m := validEmbeddingModel()
		m.ModelKey = key
		if err := validateModelCfg(m, false); !errors.Is(err, ErrModelInvalidArgument) {
			t.Fatalf("model key %q = %v, want ErrModelInvalidArgument", key, err)
		}
	}
	// 文本模型不接受维度。
	text := &ModelCfg{Kind: ModelKindText, Name: "Text", Model: "qwen3:8b", ModelKey: "qwen3:8b", Dimension: ptrInt32(ModelEmbeddingDimension)}
	if err := validateModelCfg(text, false); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("text model with dimension = %v, want ErrModelInvalidArgument", err)
	}
	// 更新路径状态必填（0/1），创建路径接受未指定哨兵。
	text.Dimension = nil
	text.Status = ModelStatusUnspecified
	if err := validateModelCfg(text, true); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("update with unspecified status = %v, want ErrModelInvalidArgument", err)
	}
	if err := validateModelCfg(text, false); err != nil {
		t.Fatalf("create with unspecified status rejected: %v", err)
	}
}

// updateOnlyMutableFields 模拟协议收窄后的更新入参：只填可变字段与 ID，
// Kind / ModelKey 为空（由业务层以锁定行回填）。
func updateOnlyMutableFields() *ModelCfg {
	return &ModelCfg{
		ID:       7,
		Status:   1,
		Name:     "Qwen3 8B",
		Provider: "openai",
		Model:    "qwen3:8b",
	}
}

func TestUpdateModel_backfills_immutable_identity_columns(t *testing.T) {
	repo := &fakeModelRepo{locked: &ModelCfg{ID: 7, Kind: ModelKindText, ModelKey: "qwen3:8b"}}
	uc := NewModelUsecase(repo, &fakeSecretCipher{})

	in := updateOnlyMutableFields()
	if in.Kind != "" || in.ModelKey != "" {
		t.Fatalf("test input carries identity columns: kind=%q model_key=%q", in.Kind, in.ModelKey)
	}
	auth := Identity{TenantID: "t-1", UserID: 7}
	out, err := uc.UpdateModel(context.Background(), auth, in, nil, false)
	if err != nil {
		t.Fatalf("UpdateModel returned error: %v", err)
	}
	if out == nil || repo.updated == nil {
		t.Fatal("UpdateModel did not reach the repo")
	}
	// 锁定必须按请求 ID 进行，且透传调用方身份。
	if repo.lockedID != in.ID {
		t.Fatalf("LockModel id = %d, want request id %d", repo.lockedID, in.ID)
	}
	if repo.lockedAuth != auth {
		t.Fatalf("LockModel auth = %+v, want %+v", repo.lockedAuth, auth)
	}
	// 写库对象必须携带锁定行的存量身份列。
	if repo.updated.Kind != ModelKindText {
		t.Fatalf("written kind = %q, want %q", repo.updated.Kind, ModelKindText)
	}
	if repo.updated.ModelKey != "qwen3:8b" {
		t.Fatalf("written model_key = %q, want %q", repo.updated.ModelKey, "qwen3:8b")
	}
}

func TestUpdateModel_ignores_forged_identity_columns(t *testing.T) {
	// 即使调用方绕过协议伪造 kind / model_key，写库对象也必须是锁定行的
	// 存量值：更新路径不信任任何入参身份列。
	repo := &fakeModelRepo{locked: &ModelCfg{ID: 7, Kind: ModelKindText, ModelKey: "qwen3:8b"}}
	uc := NewModelUsecase(repo, &fakeSecretCipher{})
	in := updateOnlyMutableFields()
	in.Kind = ModelKindEmbedding
	in.ModelKey = "forged-key"
	// 以文本行身份回填后不携带维度即可通过校验，便于观察写库值。
	if _, err := uc.UpdateModel(context.Background(), Identity{TenantID: "t-1", UserID: 7}, in, nil, false); err != nil {
		t.Fatalf("UpdateModel returned error: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("UpdateModel did not reach the repo")
	}
	if repo.updated.Kind != ModelKindText {
		t.Fatalf("written kind = %q, want locked kind %q", repo.updated.Kind, ModelKindText)
	}
	if repo.updated.ModelKey != "qwen3:8b" {
		t.Fatalf("written model_key = %q, want locked model_key %q", repo.updated.ModelKey, "qwen3:8b")
	}
}

func TestUpdateModel_rejects_platform_row(t *testing.T) {
	// LockModel 会回退命中平台行；租户路径必须以 NotFound 拒绝，且不进入
	// 校验/加密/写库。
	repo := &fakeModelRepo{locked: &ModelCfg{ID: 7, Platform: true, Kind: ModelKindText, ModelKey: "qwen3:8b"}}
	cipher := &fakeSecretCipher{}
	uc := NewModelUsecase(repo, cipher)
	key := "sk-forged"
	_, err := uc.UpdateModel(context.Background(), Identity{TenantID: "t-1", UserID: 7}, updateOnlyMutableFields(), &key, false)
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("UpdateModel error = %v, want ErrModelNotFound", err)
	}
	if repo.updated != nil {
		t.Fatal("platform row reached the tenant update repo path")
	}
	if len(cipher.sealed) != 0 {
		t.Fatalf("cipher sealed %d values, want no encryption", len(cipher.sealed))
	}
}

func TestUpdateModel_dimension_discipline_follows_locked_kind(t *testing.T) {
	cases := []struct {
		name         string
		lockedKind   string
		lockedKey    string
		incomingKind string
		dimension    *int32
		wantErr      bool
	}{
		{
			name:         "text row rejects forged embedding kind with dimension",
			lockedKind:   ModelKindText,
			lockedKey:    "qwen3:8b",
			incomingKind: ModelKindEmbedding,
			dimension:    ptrInt32(ModelEmbeddingDimension),
			wantErr:      true,
		},
		{
			name:         "text row rejects dimension",
			lockedKind:   ModelKindText,
			lockedKey:    "qwen3:8b",
			incomingKind: ModelKindText,
			dimension:    ptrInt32(ModelEmbeddingDimension),
			wantErr:      true,
		},
		{
			name:         "embedding row accepts forged text kind with 1024 dimension",
			lockedKind:   ModelKindEmbedding,
			lockedKey:    "qwen3-embedding:0.6b",
			incomingKind: ModelKindText,
			dimension:    ptrInt32(ModelEmbeddingDimension),
		},
		{
			name:         "embedding row rejects missing dimension",
			lockedKind:   ModelKindEmbedding,
			lockedKey:    "qwen3-embedding:0.6b",
			incomingKind: ModelKindEmbedding,
			dimension:    nil,
			wantErr:      true,
		},
		{
			name:         "embedding row rejects forged text kind without dimension",
			lockedKind:   ModelKindEmbedding,
			lockedKey:    "qwen3-embedding:0.6b",
			incomingKind: ModelKindText,
			dimension:    nil,
			wantErr:      true,
		},
		{
			name:         "embedding row rejects non-1024 dimension",
			lockedKind:   ModelKindEmbedding,
			lockedKey:    "qwen3-embedding:0.6b",
			incomingKind: ModelKindEmbedding,
			dimension:    ptrInt32(512),
			wantErr:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeModelRepo{locked: &ModelCfg{ID: 7, Kind: tc.lockedKind, ModelKey: tc.lockedKey}}
			uc := NewModelUsecase(repo, &fakeSecretCipher{})
			in := updateOnlyMutableFields()
			in.Kind = tc.incomingKind
			in.Model = tc.lockedKey
			in.Dimension = tc.dimension

			_, err := uc.UpdateModel(context.Background(), Identity{TenantID: "t-1", UserID: 7}, in, nil, false)
			if tc.wantErr {
				if !errors.Is(err, ErrModelInvalidArgument) {
					t.Fatalf("UpdateModel error = %v, want ErrModelInvalidArgument", err)
				}
				if repo.updated != nil {
					t.Fatal("invalid update reached the repo")
				}
				return
			}
			if err != nil {
				t.Fatalf("UpdateModel returned error: %v", err)
			}
			if repo.updated == nil || repo.updated.Kind != tc.lockedKind {
				t.Fatalf("written kind = %v, want locked kind %q", repo.updated, tc.lockedKind)
			}
		})
	}
}

func TestUpdateModel_passes_through_lock_error(t *testing.T) {
	other := errors.New("lock failed")
	for _, sentinel := range []error{ErrModelNotFound, other} {
		repo := &fakeModelRepo{lockErr: sentinel}
		uc := NewModelUsecase(repo, &fakeSecretCipher{})
		_, err := uc.UpdateModel(context.Background(), Identity{TenantID: "t-1", UserID: 7}, updateOnlyMutableFields(), nil, false)
		// 锁定错误必须原样透传（同一错误值，不包装），便于上层按哨兵映射。
		if err != sentinel {
			t.Fatalf("UpdateModel error = %v, want identical sentinel %v", err, sentinel)
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("errors.Is(%v, %v) = false, want true", err, sentinel)
		}
		if repo.updated != nil {
			t.Fatal("update reached the repo after lock error")
		}
	}
}

func TestUpdateModel_api_key_passthrough(t *testing.T) {
	const current = "stored-cipher"
	cases := []struct {
		name       string
		apiKey     *string
		clear      bool
		wantCipher string
		wantSealed bool
	}{
		{
			name:       "provided api key is sealed",
			apiKey:     func() *string { v := "sk-new"; return &v }(),
			wantCipher: "sealed:sk-new",
			wantSealed: true,
		},
		{
			name:       "clear api key wipes cipher",
			clear:      true,
			wantCipher: "",
		},
		{
			name:       "omitted api key keeps locked cipher",
			wantCipher: current,
		},
		{
			name:       "blank api key keeps locked cipher",
			apiKey:     func() *string { v := "  "; return &v }(),
			wantCipher: current,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeModelRepo{
				locked:     &ModelCfg{ID: 7, Kind: ModelKindText, ModelKey: "qwen3:8b"},
				lockCipher: current,
			}
			cipher := &fakeSecretCipher{}
			uc := NewModelUsecase(repo, cipher)
			if _, err := uc.UpdateModel(context.Background(), Identity{TenantID: "t-1", UserID: 7}, updateOnlyMutableFields(), tc.apiKey, tc.clear); err != nil {
				t.Fatalf("UpdateModel returned error: %v", err)
			}
			if repo.updated == nil {
				t.Fatal("UpdateModel did not reach the repo")
			}
			if repo.updateCipher != tc.wantCipher {
				t.Fatalf("written cipher = %q, want %q", repo.updateCipher, tc.wantCipher)
			}
			if got := len(cipher.sealed) > 0; got != tc.wantSealed {
				t.Fatalf("sealed = %v, want %v", got, tc.wantSealed)
			}
		})
	}
}

func TestUpdateModelPlatform_rejects_tenant_row(t *testing.T) {
	repo := &fakeModelRepo{locked: &ModelCfg{ID: 7, Platform: false, Kind: ModelKindText, ModelKey: "qwen3:8b"}}
	uc := NewModelUsecase(repo, &fakeSecretCipher{})
	if _, err := uc.UpdateModelPlatform(context.Background(), Identity{UserID: 7}, updateOnlyMutableFields(), nil, false); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("UpdateModelPlatform error = %v, want ErrModelNotFound", err)
	}
	if repo.platformUpdated != nil {
		t.Fatal("non-platform row reached the platform update repo path")
	}
}

func TestUpdateModelPlatform_backfills_platform_kind(t *testing.T) {
	repo := &fakeModelRepo{locked: &ModelCfg{
		ID:       7,
		Platform: true,
		Kind:     ModelKindEmbedding,
		ModelKey: "qwen3-embedding:0.6b",
	}}
	uc := NewModelUsecase(repo, &fakeSecretCipher{})
	in := &ModelCfg{
		ID:        7,
		Status:    1,
		Name:      "Embedding 0.6B",
		Provider:  "openai",
		Model:     "qwen3-embedding:0.6b",
		Dimension: ptrInt32(ModelEmbeddingDimension),
	}
	if _, err := uc.UpdateModelPlatform(context.Background(), Identity{UserID: 7}, in, nil, false); err != nil {
		t.Fatalf("UpdateModelPlatform returned error: %v", err)
	}
	if repo.platformUpdated == nil {
		t.Fatal("UpdateModelPlatform did not reach the repo")
	}
	if repo.platformUpdated.Kind != ModelKindEmbedding || repo.platformUpdated.ModelKey != "qwen3-embedding:0.6b" {
		t.Fatalf("written row = %+v, want locked embedding kind and model_key", repo.platformUpdated)
	}
}

func TestValidateModelCfg_rerank_kind_accepted_without_dimension(t *testing.T) {
	// rerank 类别合法且与文本同形不携带维度（维度是嵌入专属列）。
	m := validEmbeddingModel()
	m.Kind = ModelKindRerank
	m.Name = "Rerank"
	m.ModelKey = "bge-reranker:0.6b"
	m.Model = "bge-reranker:0.6b"
	m.Dimension = nil
	if err := validateModelCfg(m, false); err != nil {
		t.Fatalf("rerank model without dimension rejected: %v", err)
	}
	// rerank 携带维度按非法拒绝。
	m.Dimension = ptrInt32(ModelEmbeddingDimension)
	if err := validateModelCfg(m, false); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("rerank model with dimension = %v, want ErrModelInvalidArgument", err)
	}
	// 类别过滤校验：空串不过滤，三类枚举命中，其余拒绝。
	if !validModelKindFilter("") || !validModelKindFilter(ModelKindText) || !validModelKindFilter(ModelKindEmbedding) || !validModelKindFilter(ModelKindRerank) {
		t.Fatal("valid model kind filter rejected, want accepted")
	}
	if validModelKindFilter("audio") {
		t.Fatal("unknown kind filter accepted, want ErrModelInvalidArgument semantics")
	}
}

// textModelWithBudget 构造携带生成预算的合法文本模型入参。
func textModelWithBudget(contextWindow, maxOutput *int32) *ModelCfg {
	return &ModelCfg{
		Kind:            ModelKindText,
		Name:            "DeepSeek Flash",
		ModelKey:        "deepseek-flash",
		Provider:        "deepseek",
		Model:           "deepseek-flash",
		ContextWindow:   contextWindow,
		MaxOutputTokens: maxOutput,
	}
}

func TestValidateModelCfg_text_generation_budget(t *testing.T) {
	// 1M 上下文、384K 输出是合法声明。
	if err := validateModelCfg(textModelWithBudget(ptrInt32(1048576), ptrInt32(393216)), false); err != nil {
		t.Fatalf("valid generation budget rejected: %v", err)
	}
	// 只声明其中一项也合法。
	if err := validateModelCfg(textModelWithBudget(ptrInt32(1048576), nil), false); err != nil {
		t.Fatalf("context window without max output rejected: %v", err)
	}
	if err := validateModelCfg(textModelWithBudget(nil, ptrInt32(4096)), false); err != nil {
		t.Fatalf("max output without context window rejected: %v", err)
	}
	// 输出上限不得大于上下文窗口。
	if err := validateModelCfg(textModelWithBudget(ptrInt32(1024), ptrInt32(2048)), false); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("max output exceeding context window = %v, want ErrModelInvalidArgument", err)
	}
	// 两列均为正数约束。
	for _, m := range []*ModelCfg{
		textModelWithBudget(ptrInt32(0), nil),
		textModelWithBudget(ptrInt32(-1), nil),
		textModelWithBudget(nil, ptrInt32(0)),
		textModelWithBudget(nil, ptrInt32(-1)),
	} {
		if err := validateModelCfg(m, false); !errors.Is(err, ErrModelInvalidArgument) {
			t.Fatalf("non-positive generation budget = %v, want ErrModelInvalidArgument", err)
		}
	}
	// 生成预算只属于文本模型：嵌入与重排携带即拒绝。
	embedding := validEmbeddingModel()
	embedding.ContextWindow = ptrInt32(1048576)
	if err := validateModelCfg(embedding, false); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("embedding with context window = %v, want ErrModelInvalidArgument", err)
	}
	rerank := validEmbeddingModel()
	rerank.Kind = ModelKindRerank
	rerank.Dimension = nil
	rerank.MaxOutputTokens = ptrInt32(4096)
	if err := validateModelCfg(rerank, false); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("rerank with max output = %v, want ErrModelInvalidArgument", err)
	}
}
