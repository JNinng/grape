package agent

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// userKeySeparator 分隔租户与用户：userKey = tenant_id:user_id。
const userKeySeparator = ":"

// userKeyPartCount 是 userKey 的段数：租户在前、数字用户在后。
const userKeyPartCount = 2

// userKeyDecodeErr 是 userKey 解码失败的统一错误：不向调用方区分
// 具体字段，避免把内部格式暴露成攻击面。
var userKeyDecodeErr = errors.New("agent: invalid user key")

// EncodeUserKey 把认证身份编码为框架会话存储的 userKey（tenant_id:user_id，
// 数字 user_id）。它是 AG-UI UserIDResolver、ack WithUserFunc 与后续审计
// 写入共用的唯一起点：会话归属与许可归属都锚定在这个编码上，禁止任何
// 调用方自行拼接或回落框架默认的 "user"。
func EncodeUserKey(tenantID string, userID int64) (string, error) {
	if strings.TrimSpace(tenantID) == "" {
		return "", fmt.Errorf("%w: empty tenant", userKeyDecodeErr)
	}
	if userID <= 0 {
		return "", fmt.Errorf("%w: non-positive user id", userKeyDecodeErr)
	}
	return tenantID + userKeySeparator + strconv.FormatInt(userID, 10), nil
}

// DecodeUserKey 解码 userKey 为租户与用户 ID。从右侧按唯一分隔符切分，
// 租户段本身允许含分隔符；任一段缺失或用户段非正整数即拒绝（fail-closed）。
func DecodeUserKey(key string) (string, int64, error) {
	tenant, rawUser, ok := strings.Cut(key, userKeySeparator)
	if !ok || strings.TrimSpace(tenant) == "" {
		return "", 0, userKeyDecodeErr
	}
	userID, err := strconv.ParseInt(rawUser, 10, 64)
	if err != nil || userID <= 0 {
		return "", 0, userKeyDecodeErr
	}
	return tenant, userID, nil
}
