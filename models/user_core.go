package yapyap

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	Username     string            `json:"username" gorm:"uniqueIndex;not null"`
	PasswordHash string            `json:"-" gorm:"not null"`
	Status       UserStatus        `json:"status"`
	LastActive   time.Time         `json:"last_active"`
	AvatarURL    string            `json:"avatar_url"`
	Bio          string            `json:"bio"`
	Permissions  []UserPermissions `json:"permissions" gorm:"foreignKey:UserID"`
	LoginTokens  []UserLoginToken  `json:"login_tokens" gorm:"foreignKey:UserID"`
}
