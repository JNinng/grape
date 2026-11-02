package testsupport

import (
	"testing"

	"grape/internal/agent/identity"
)

// OwnerUserKey 是归属者身份的 userKey（决定人断言基准）。
func OwnerUserKey(t *testing.T) string {
	t.Helper()
	key, err := identity.EncodeUserKey(Tenant, OwnerID)
	if err != nil {
		t.Fatalf("encode owner user key: %v", err)
	}
	return key
}
