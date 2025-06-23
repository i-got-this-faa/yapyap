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

type UserPermissions struct {
	// (UserID) -> Permission

	// Defines permissions for a user across the Instance
	UserID uint64 `json:"user_id"`

	ViewAnalytics     bool `json:"view_analytics"`      // Can view instance analytics (e.g., user counts, channel counts, etc.)
	SendMessages      bool `json:"send_messages"`       // Can send messages in channels
	SendAttachments   bool `json:"send_attachments"`    // Can send attachments in messages
	JoinVoiceChannels bool `json:"join_voice_channels"` // Can join voice channels

	ManageMessages bool `json:"manage_messages"` // Can manage messages (delete, pin, etc.)
	ManageUsers    bool `json:"manage_users"`    // Can manage users (ban, unban, etc.)
	ManageChannels bool `json:"manage_channels"` // Can manage channels (create, delete, edit channels)
	ManageInstance bool `json:"manage_instance"` // Can manage instance settings (e.g., name , description, etc.)

	Admin bool `json:"admin"` // Is the user an admin? Admins have all permissions
}

const (
	TokenExpirationDuration = 30 * 24 * time.Hour // Expires the token if  it is not used for 30 days
)

// This token will be used to validate user sessions and authenticate API requests.
// Essentially, we also want to ensure that a ws connection cannot be established without a valid token.
type UserLoginToken struct {
	// (UserID) -> UserLoginToken
	UserID    uint64    `json:"user_id"`    // The ID of the user this token belongs to
	Token     string    `json:"token"`      // The JWT token for the user
	CreatedAt time.Time `json:"created_at"` // When the token was created
	LastUsed  time.Time `json:"last_used"`  // When the token was last used
}

func (t *UserLoginToken) IsValid() bool {
	return time.Since(t.LastUsed) < TokenExpirationDuration
}

func (t *UserLoginToken) UpdateLastUsed() {
	t.LastUsed = time.Now()
}
