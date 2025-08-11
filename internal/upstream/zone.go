package upstream

import (
	"crypto/md5"
	"fmt"
	"regexp"
)

// ValidateZoneID 验证ZoneID格式是否有效 (001-006)
func ValidateZoneID(zoneID string) bool {
	matched, _ := regexp.MatchString(`^00[1-6]$`, zoneID)
	return matched
}

// GetZoneByOpenID 根据OpenID计算对应的ZoneID
func GetZoneByOpenID(openID string) (string, error) {
	if openID == "" {
		return "", fmt.Errorf("openID cannot be empty")
	}

	// 使用MD5哈希OpenID
	hash := md5.Sum([]byte(openID))

	// 取哈希值的前4个字节，转换为整数
	hashInt := uint32(hash[0])<<24 + uint32(hash[1])<<16 + uint32(hash[2])<<8 + uint32(hash[3])

	// 模6取余，映射到001-006
	zoneNum := (hashInt % 6) + 1

	return fmt.Sprintf("%03d", zoneNum), nil
}
