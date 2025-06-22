package yapyap

import "time"

type UserStatus uint

const (
	StatusOffline UserStatus = 0
	StatusActive  UserStatus = 1
	StatusIdle    UserStatus = 2
)

type User struct {
	ID         uint64     `json:"id"`
	Username   string     `json:"username"`
	CreatedAt  time.Time  `json:"created_at"`
	Status     UserStatus `json:"status"`
	LastActive time.Time  `json:"last_active"`
	AvatarURL  string     `json:"avatar_url"`
	Bio        string     `json:"bio"`
}
