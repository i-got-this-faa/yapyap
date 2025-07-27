package yapyap

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type PermissionState string

const (
	PermissionUnset PermissionState = "unset"
	PermissionAllow PermissionState = "allow"
	PermissionDeny  PermissionState = "deny"
)

// RolePermissions is a map of permission name to PermissionState
// e.g. {"ViewChannel": "allow", "SendMessage": "deny"}
type RolePermissions map[string]PermissionState

// Value implements the driver.Valuer interface for GORM (for saving to DB)
func (rp RolePermissions) Value() (driver.Value, error) {
	return json.Marshal(rp)
}

// Scan implements the sql.Scanner interface for GORM (for reading from DB)
func (rp *RolePermissions) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("RolePermissions: failed to type assert value to []byte")
	}
	return json.Unmarshal(b, rp)
}

type Role struct {
	gorm.Model
	ID          uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string          `json:"name" gorm:"uniqueIndex;not null"`
	Permissions RolePermissions `json:"permissions" gorm:"type:jsonb"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	UserRoles   []UserRole      `json:"user_roles" gorm:"foreignKey:RoleID"`
}

type UserRole struct {
	gorm.Model
	UserID uint64 `json:"user_id" gorm:"index;not null"`
	RoleID uint64 `json:"role_id" gorm:"index;not null"`

	// Relationships
	Role Role `json:"role" gorm:"foreignKey:RoleID"`
}

// Helper for permission resolution
func ResolvePermission(userPerm, rolePerm PermissionState) bool {
	switch userPerm {
	case PermissionAllow:
		return true
	case PermissionDeny:
		return false
	case PermissionUnset:
		return rolePerm == PermissionAllow
	default:
		return false
	}
}

// GetPermission returns the PermissionState for a given permission name
func (r *Role) GetPermission(perm string) PermissionState {
	if r.Permissions == nil {
		return PermissionUnset
	}
	if val, ok := r.Permissions[perm]; ok {
		return val
	}
	return PermissionUnset
}
