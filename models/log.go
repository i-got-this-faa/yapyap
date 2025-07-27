package yapyap

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// LogLevel represents the severity level of a log entry
type LogLevel uint

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
	LogLevelFatal
)

func (l LogLevel) String() string {
	switch l {
	case LogLevelDebug:
		return "DEBUG"
	case LogLevelInfo:
		return "INFO"
	case LogLevelWarn:
		return "WARN"
	case LogLevelError:
		return "ERROR"
	case LogLevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// LogAction represents the type of action being logged
type LogAction string

const (
	// User actions
	LogActionUserRegister LogAction = "user.register"
	LogActionUserLogin    LogAction = "user.login"
	LogActionUserLogout   LogAction = "user.logout"
	LogActionUserUpdate   LogAction = "user.update"
	LogActionUserDelete   LogAction = "user.delete"

	// Channel actions
	LogActionChannelCreate LogAction = "channel.create"
	LogActionChannelUpdate LogAction = "channel.update"
	LogActionChannelDelete LogAction = "channel.delete"

	// Message actions
	LogActionMessageSend   LogAction = "message.send"
	LogActionMessageUpdate LogAction = "message.update"
	LogActionMessageDelete LogAction = "message.delete"

	// Role actions
	LogActionRoleCreate LogAction = "role.create"
	LogActionRoleUpdate LogAction = "role.update"
	LogActionRoleDelete LogAction = "role.delete"
	LogActionRoleAssign LogAction = "role.assign"
	LogActionRoleRevoke LogAction = "role.revoke"

	// Permission actions
	LogActionPermissionGrant  LogAction = "permission.grant"
	LogActionPermissionRevoke LogAction = "permission.revoke"

	// System actions
	LogActionSystemStartup  LogAction = "system.startup"
	LogActionSystemShutdown LogAction = "system.shutdown"
	LogActionSystemError    LogAction = "system.error"

	// Authentication actions
	LogActionAuthSuccess LogAction = "auth.success"
	LogActionAuthFailure LogAction = "auth.failure"
	LogActionAuthBlocked LogAction = "auth.blocked"
)

// Log represents a database log entry
type Log struct {
	gorm.Model
	ID         uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	Level      LogLevel        `json:"level" gorm:"index;not null"`
	Action     LogAction       `json:"action" gorm:"index;not null"`
	Message    string          `json:"message" gorm:"not null"`
	UserID     *uint64         `json:"user_id,omitempty" gorm:"index"`       // Optional: user who performed the action
	TargetID   *uint64         `json:"target_id,omitempty" gorm:"index"`     // Optional: ID of the target resource
	TargetType string          `json:"target_type,omitempty" gorm:"index"`   // Optional: type of target (user, channel, message, etc.)
	IPAddress  string          `json:"ip_address,omitempty" gorm:"index"`    // Optional: IP address of the client
	UserAgent  string          `json:"user_agent,omitempty"`                 // Optional: User agent string
	Metadata   json.RawMessage `json:"metadata,omitempty" gorm:"type:jsonb"` // Optional: additional structured data
	CreatedAt  time.Time       `json:"created_at" gorm:"index"`

	// Relationship to User (optional)
	User *User `json:"user,omitempty" gorm:"foreignKey:UserID"`
}

// LogMetadata is a helper struct for structured metadata
type LogMetadata map[string]interface{}

// ToJSON converts LogMetadata to json.RawMessage for storage
func (m LogMetadata) ToJSON() json.RawMessage {
	if m == nil {
		return nil
	}
	data, _ := json.Marshal(m)
	return json.RawMessage(data)
}

// FromJSON converts json.RawMessage to LogMetadata
func (m *LogMetadata) FromJSON(data json.RawMessage) error {
	if data == nil {
		*m = nil
		return nil
	}
	return json.Unmarshal(data, m)
}

// LogFilter represents filters for querying logs
type LogFilter struct {
	Level      *LogLevel  `json:"level,omitempty"`
	Action     *LogAction `json:"action,omitempty"`
	UserID     *uint64    `json:"user_id,omitempty"`
	TargetID   *uint64    `json:"target_id,omitempty"`
	TargetType *string    `json:"target_type,omitempty"`
	IPAddress  *string    `json:"ip_address,omitempty"`
	StartDate  *time.Time `json:"start_date,omitempty"`
	EndDate    *time.Time `json:"end_date,omitempty"`
	Limit      int        `json:"limit,omitempty"`
	Offset     int        `json:"offset,omitempty"`
}

// LogStats represents statistics about logs
type LogStats struct {
	TotalLogs     int64            `json:"total_logs"`
	LogsByLevel   map[string]int64 `json:"logs_by_level"`
	LogsByAction  map[string]int64 `json:"logs_by_action"`
	RecentActions []Log            `json:"recent_actions"`
}
