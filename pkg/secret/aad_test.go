package secret

import (
	"bytes"
	"testing"
)

// AAD 构造的内部契约测试（同包，可直接调用 buildAAD）：AAD 必须同时绑定
// 完整键名（基础名@版本）与算法标识，任一维度不同都必须产出不同字节，
// 否则密文可跨业务键、跨版本或跨算法挪用。

func TestBuildAAD_binds_name_and_kind(t *testing.T) {
	baseline := buildAAD("group_a", "v1", KindAES256GCM)

	if again := buildAAD("group_a", "v1", KindAES256GCM); !bytes.Equal(again, baseline) {
		t.Fatalf("buildAAD is not deterministic: %q vs %q", again, baseline)
	}
	cases := map[string][]byte{
		"different algorithm":    buildAAD("group_a", "v1", Kind("AES-128-GCM")),
		"different business key": buildAAD("group_b", "v1", KindAES256GCM),
		"different version":      buildAAD("group_a", "v2", KindAES256GCM),
	}
	for name, aad := range cases {
		if bytes.Equal(aad, baseline) {
			t.Fatalf("%s: AAD %q equals baseline %q, want the name/kind binding to distinguish them", name, aad, baseline)
		}
	}
}
