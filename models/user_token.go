package yapyap

import (
	"time"

	"gorm.io/gorm"
)

const (
	TokenExpirationDuration = 30 * 24 * time.Hour
)

type UserLoginToken struct {
	gorm.Model
	UserID   uint      `json:"user_id" gorm:"not null;index"`
	Token    string    `json:"token" gorm:"uniqueIndex;not null"`
	LastUsed time.Time `json:"last_used"`
}

func (t *UserLoginToken) IsValid() bool {
	return time.Since(t.LastUsed) < TokenExpirationDuration
}

func (t *UserLoginToken) UpdateLastUsed() {
	t.LastUsed = time.Now()
}
