package yapyap

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID           uint64            `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	DeletedAt    gorm.DeletedAt    `json:"-" gorm:"index"`
	Username     string            `json:"username" gorm:"uniqueIndex;not null"`
	PasswordHash string            `json:"-" gorm:"not null"`
	Status       UserStatus        `json:"status"`
	LastActive   time.Time         `json:"last_active"`
	AvatarURL    string            `json:"avatar_url"`
	Bio          string            `json:"bio"`
	Permissions  []UserPermissions `json:"permissions,omitempty" gorm:"foreignKey:UserID"`
	LoginTokens  []UserLoginToken  `json:"login_tokens,omitempty" gorm:"foreignKey:UserID"`
}
