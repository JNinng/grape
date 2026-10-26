package redact

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// mustKeyFilter 构造键名过滤器：清单或内层非法属用例失败而非错误返回。
func mustKeyFilter(t *testing.T, keys []string, inner Redactor) Redactor {
	t.Helper()
	f, err := NewKeyFilter(keys, inner)
	if err != nil {
		t.Fatalf("new key filter: %v", err)
	}
	return f
}

// --- 掩码 ---

func Test_mask_replaces_any_nonempty_value_with_fixed_replacement(t *testing.T) {
	m := NewMask("[redacted]")
	for _, value := range []string{"s3cret", "x", strings.Repeat("long", 4096)} {
		if got := m.Redact(value); got != "[redacted]" {
			t.Fatalf("mask(%d bytes) = %q, want fixed replacement", len(value), got)
		}
	}
}

func Test_mask_keeps_empty_value_empty(t *testing.T) {
	if got := NewMask("[redacted]").Redact(""); got != "" {
		t.Fatalf("mask(empty) = %q, want empty", got)
	}
}

func Test_mask_with_empty_replacement_erases_value(t *testing.T) {
	if got := NewMask("").Redact("secret"); got != "" {
		t.Fatalf("mask with empty replacement = %q, want erased", got)
	}
}

// --- keyed 哈希 ---

func Test_hash_is_deterministic_for_same_salt_and_value(t *testing.T) {
	a := NewHash("salt-1").Redact("bearer-token-abc")
	b := NewHash("salt-1").Redact("bearer-token-abc")
	if a == "" || a != b {
		t.Fatalf("hash not deterministic: %q vs %q", a, b)
	}
}

func Test_hash_differs_across_salts_and_values(t *testing.T) {
	base := NewHash("salt-1").Redact("token-1")
	if other := NewHash("salt-2").Redact("token-1"); other == base {
		t.Fatal("different salts must produce different digests")
	}
	if other := NewHash("salt-1").Redact("token-2"); other == base {
		t.Fatal("different values must produce different digests")
	}
}

func Test_hash_output_has_algorithm_prefix_and_fixed_width(t *testing.T) {
	got := NewHash("salt").Redact("value")
	if !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("hash output %q missing algorithm prefix", got)
	}
	if n := len(strings.TrimPrefix(got, "sha256:")); n != hashHexLen {
		t.Fatalf("hash digest width = %d, want %d", n, hashHexLen)
	}
}

func Test_hash_keeps_empty_value_empty(t *testing.T) {
	if got := NewHash("salt").Redact(""); got != "" {
		t.Fatalf("hash(empty) = %q, want empty", got)
	}
}

func Test_hash_never_returns_original_value(t *testing.T) {
	value := "grape-access-token-9f8e7d6c"
	if got := NewHash("salt").Redact(value); strings.Contains(got, value) {
		t.Fatalf("hash output %q leaks original value", got)
	}
}

// --- 截断 ---

func Test_truncate_keeps_value_within_limit_unchanged(t *testing.T) {
	value := "short"
	if got := NewTruncate(16, "...(truncated)").Redact(value); got != value {
		t.Fatalf("truncate(short) = %q, want unchanged", got)
	}
}

func Test_truncate_cuts_to_keep_runes_with_suffix(t *testing.T) {
	value := strings.Repeat("a", 20)
	want := strings.Repeat("a", 8) + "...(truncated)"
	if got := NewTruncate(8, "...(truncated)").Redact(value); got != want {
		t.Fatalf("truncate = %q, want %q", got, want)
	}
}

func Test_truncate_respects_multibyte_rune_boundary(t *testing.T) {
	value := strings.Repeat("好", 10) // 每字符 3 字节
	got := NewTruncate(4, "…").Redact(value)
	want := strings.Repeat("好", 4) + "…"
	if got != want {
		t.Fatalf("truncate multibyte = %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output %q is not valid utf8", got)
	}
}

func Test_truncate_with_zero_keep_returns_suffix_only(t *testing.T) {
	if got := NewTruncate(0, "[all hidden]").Redact("whatever"); got != "[all hidden]" {
		t.Fatalf("truncate(0) = %q, want suffix only", got)
	}
}

func Test_truncate_treats_negative_keep_as_zero(t *testing.T) {
	if got := NewTruncate(-3, "x").Redact("value"); got != "x" {
		t.Fatalf("truncate(negative) = %q, want suffix only", got)
	}
}

func Test_truncate_keeps_empty_value_empty(t *testing.T) {
	if got := NewTruncate(8, "...").Redact(""); got != "" {
		t.Fatalf("truncate(empty) = %q, want empty", got)
	}
}

// --- 键名过滤 ---

func Test_keyfilter_redacts_matched_key_and_keeps_others(t *testing.T) {
	in := `{"username":"alice","password":"s3cret","age":30}`
	got := mustKeyFilter(t, []string{"password"}, NewMask("[redacted]")).Redact(in)
	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("output is not json: %v\n%s", err, got)
	}
	if out["password"] != "[redacted]" {
		t.Fatalf("matched key value = %v, want masked", out["password"])
	}
	if out["username"] != "alice" || out["age"] != float64(30) {
		t.Fatalf("unmatched keys altered: %v", out)
	}
}

func Test_keyfilter_matches_keys_case_insensitively(t *testing.T) {
	in := `{"Password":"s3cret","TOKEN":"t-1"}`
	got := mustKeyFilter(t, []string{"password", "token"}, NewMask("[redacted]")).Redact(in)
	if !strings.Contains(got, "[redacted]") || strings.Contains(got, "s3cret") || strings.Contains(got, `"t-1"`) {
		t.Fatalf("case-insensitive match failed: %s", got)
	}
}

func Test_keyfilter_recurses_into_nested_objects_and_arrays(t *testing.T) {
	in := `{"a":{"b":{"secret":"deep"}},"list":[{"secret":"in-array"},{"keep":"me"}]}`
	got := mustKeyFilter(t, []string{"secret"}, NewMask("[redacted]")).Redact(in)
	for _, leaked := range []string{"deep", "in-array"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("nested value %q leaked: %s", leaked, got)
		}
	}
	if !strings.Contains(got, `"me"`) {
		t.Fatalf("unmatched array element altered: %s", got)
	}
}

func Test_keyfilter_hashes_whole_subtree_of_matched_object(t *testing.T) {
	in := `{"credentials":{"user":"u","key":"k"},"other":1}`
	inner := NewHash("salt")
	got := mustKeyFilter(t, []string{"credentials"}, inner).Redact(in)
	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("output is not json: %v", err)
	}
	if out["credentials"] != inner.Redact(`{"key":"k","user":"u"}`) {
		t.Fatalf("matched subtree = %v, want keyed hash of compact subtree", out["credentials"])
	}
}

func Test_keyfilter_converts_matched_scalar_to_string_form(t *testing.T) {
	in := `{"token":12345,"keep":true}`
	got := mustKeyFilter(t, []string{"token"}, NewMask("[redacted]")).Redact(in)
	if !strings.Contains(got, `"token":"[redacted]"`) {
		t.Fatalf("scalar matched value = %s, want string replacement", got)
	}
	if !strings.Contains(got, `"keep":true`) {
		t.Fatalf("unmatched scalar altered: %s", got)
	}
}

func Test_keyfilter_returns_non_json_input_unchanged(t *testing.T) {
	f := mustKeyFilter(t, []string{"secret"}, NewMask("[redacted]"))
	for _, in := range []string{"plain text with secret", "", "123", `"quoted scalar"`, "{not json"} {
		if got := f.Redact(in); got != in {
			t.Fatalf("non-object input %q altered to %q", in, got)
		}
	}
}

func Test_keyfilter_preserves_determinism_across_calls(t *testing.T) {
	f := mustKeyFilter(t, []string{"password"}, NewMask("[redacted]"))
	in := `{"z":1,"a":{"password":"p"},"m":[1,2]}`
	first := f.Redact(in)
	for i := 0; i < 3; i++ {
		if again := f.Redact(in); again != first {
			t.Fatalf("key filter not deterministic: %q vs %q", again, first)
		}
	}
}

func Test_keyfilter_with_empty_key_list_is_rejected(t *testing.T) {
	if _, err := NewKeyFilter(nil, NewMask("x")); err == nil {
		t.Fatal("empty key list must be rejected")
	}
}

func Test_keyfilter_with_nil_inner_redactor_is_rejected(t *testing.T) {
	if _, err := NewKeyFilter([]string{"secret"}, nil); err == nil {
		t.Fatal("nil inner redactor must be rejected")
	}
}

// --- 注册表 ---

func Test_registry_redacts_with_registered_category(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(CategoryToken, NewMask("[redacted]")); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := r.Redact(CategoryToken, "bearer-1")
	if !ok || got != "[redacted]" {
		t.Fatalf("registry redact = (%q, %v), want masked and true", got, ok)
	}
}

func Test_registry_returns_original_value_for_unregistered_category(t *testing.T) {
	r := NewRegistry()
	value := "raw-value"
	got, ok := r.Redact(CategoryRetrieval, value)
	if ok {
		t.Fatal("unregistered category must report false")
	}
	if got != value {
		t.Fatalf("unregistered category returned %q, want original", got)
	}
}

func Test_register_replaces_previous_redactor(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(CategoryToken, NewMask("[first]"))
	if err := r.Register(CategoryToken, NewMask("[second]")); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if got, _ := r.Redact(CategoryToken, "v"); got != "[second]" {
		t.Fatalf("after replace = %q, want latest registration", got)
	}
}

func Test_register_rejects_empty_category_and_nil_redactor(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("", NewMask("x")); err == nil {
		t.Fatal("empty category must be rejected")
	}
	if err := r.Register(CategoryToken, nil); err == nil {
		t.Fatal("nil redactor must be rejected")
	}
}

func Test_registry_lookup_reports_presence(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Lookup(CategoryPassword); ok {
		t.Fatal("empty registry must not report presence")
	}
	_ = r.Register(CategoryPassword, NewMask("x"))
	if red, ok := r.Lookup(CategoryPassword); !ok || red == nil {
		t.Fatal("registered category must be lookup-able")
	}
}

// Test_registry_register_and_redact_race_free 在 -race 下并发注册与读：
// 注册表写少读多，Register 与 Redact/Lookup 并发不得产生数据竞争。
func Test_registry_register_and_redact_race_free(t *testing.T) {
	r := NewStandardRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = r.Register(CategoryToken, NewMask("[mask-"+string(rune('0'+i))+"]"))
		}(i)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = r.Redact(CategoryToken, "v")
				_, _ = r.Redact(CategoryPassword, "p")
				_, _ = r.Lookup(CategoryToolParam)
			}
		}()
	}
	wg.Wait()
}

// --- 标准注册表 ---

func Test_standard_registry_covers_all_four_categories(t *testing.T) {
	r := NewStandardRegistry()
	for _, category := range []string{CategoryPassword, CategoryToken, CategoryToolParam, CategoryRetrieval} {
		if _, ok := r.Lookup(category); !ok {
			t.Fatalf("standard registry missing category %q", category)
		}
	}
}

func Test_standard_registry_masks_password_and_token(t *testing.T) {
	r := NewStandardRegistry()
	if got, _ := r.Redact(CategoryPassword, "hunter2"); got != "[redacted]" {
		t.Fatalf("password = %q, want masked", got)
	}
	if got, _ := r.Redact(CategoryToken, "bearer-xyz"); got != "[redacted]" {
		t.Fatalf("token = %q, want masked", got)
	}
}

func Test_standard_registry_filters_tool_param_keys(t *testing.T) {
	r := NewStandardRegistry()
	got, _ := r.Redact(CategoryToolParam, `{"path":"/tmp/x","api_key":"k-1"}`)
	if !strings.Contains(got, `"/tmp/x"`) || strings.Contains(got, "k-1") {
		t.Fatalf("tool param filter output = %s", got)
	}
}

func Test_standard_registry_truncates_long_retrieval_content(t *testing.T) {
	r := NewStandardRegistry()
	got, _ := r.Redact(CategoryRetrieval, strings.Repeat("字", defaultRetrievalKeep+10))
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Fatalf("retrieval output missing truncation marker: %q", got[len(got)-30:])
	}
	if n := utf8.RuneCountInString(strings.TrimSuffix(got, "...(truncated)")); n != defaultRetrievalKeep {
		t.Fatalf("retrieval kept %d runes, want %d", n, defaultRetrievalKeep)
	}
}
