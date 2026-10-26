package agent

import (
	"errors"
	"testing"
)

func TestEncodeThenDecodeUserKeyRoundTripsTenantAndUser(t *testing.T) {
	key, err := EncodeUserKey("t-super-01", 42)
	if err != nil {
		t.Fatalf("encode user key: want no error, got %v", err)
	}
	tenant, user, err := DecodeUserKey(key)
	if err != nil {
		t.Fatalf("decode user key %q: want no error, got %v", key, err)
	}
	if tenant != "t-super-01" || user != 42 {
		t.Fatalf("roundtrip = (%q, %d), want (t-super-01, 42)", tenant, user)
	}
}

func TestEncodeUserKeyRejectsEmptyTenant(t *testing.T) {
	if _, err := EncodeUserKey("", 1); !errors.Is(err, userKeyDecodeErr) {
		t.Fatalf("empty tenant: want userKeyDecodeErr, got %v", err)
	}
}

func TestEncodeUserKeyRejectsNonPositiveUser(t *testing.T) {
	if _, err := EncodeUserKey("t-1", 0); !errors.Is(err, userKeyDecodeErr) {
		t.Fatalf("zero user: want userKeyDecodeErr, got %v", err)
	}
	if _, err := EncodeUserKey("t-1", -3); !errors.Is(err, userKeyDecodeErr) {
		t.Fatalf("negative user: want userKeyDecodeErr, got %v", err)
	}
}

func TestDecodeUserKeyRejectsMissingSeparator(t *testing.T) {
	if _, _, err := DecodeUserKey("t-1-7"); !errors.Is(err, userKeyDecodeErr) {
		t.Fatalf("missing separator: want userKeyDecodeErr, got %v", err)
	}
}

func TestDecodeUserKeyRejectsEmptyTenantPart(t *testing.T) {
	if _, _, err := DecodeUserKey(":7"); !errors.Is(err, userKeyDecodeErr) {
		t.Fatalf("empty tenant part: want userKeyDecodeErr, got %v", err)
	}
}

func TestDecodeUserKeyRejectsNonNumericUser(t *testing.T) {
	if _, _, err := DecodeUserKey("t-1:user"); !errors.Is(err, userKeyDecodeErr) {
		t.Fatalf("non-numeric user: want userKeyDecodeErr, got %v", err)
	}
}

func TestDecodeUserKeyRejectsZeroUser(t *testing.T) {
	if _, _, err := DecodeUserKey("t-1:0"); !errors.Is(err, userKeyDecodeErr) {
		t.Fatalf("zero user: want userKeyDecodeErr, got %v", err)
	}
}
