package yapyap

import (
	"time"

	"gorm.io/gorm"
)

// Permission flags (bitwise) inspired by Discord, scoped to our needs
const (
	PERM_VIEW_CHANNEL     uint64 = 1 << 0
	PERM_SEND_MESSAGES    uint64 = 1 << 1
	PERM_SEND_ATTACHMENTS uint64 = 1 << 2
	PERM_MANAGE_MESSAGES  uint64 = 1 << 3
	PERM_MANAGE_CHANNELS  uint64 = 1 << 4
	PERM_MANAGE_ROLES     uint64 = 1 << 5
	PERM_MANAGE_GUILD     uint64 = 1 << 6 // a.k.a. Manage Instance
	PERM_CONNECT          uint64 = 1 << 7
	PERM_VIEW_ANALYTICS   uint64 = 1 << 8
	PERM_ADMINISTRATOR    uint64 = 1 << 60 // high bit for admin short-circuit
)

// ChannelOverwriteTarget indicates whether an overwrite is for a Role or a Member
type ChannelOverwriteTarget uint8

const (
	OverwriteTargetRole   ChannelOverwriteTarget = 0
	OverwriteTargetMember ChannelOverwriteTarget = 1
)

// ChannelOverwrite mimics Discord channel permission overwrites
// Applies allow/deny flags for a role or a specific member in a given channel
type ChannelOverwrite struct {
	gorm.Model
	ID         uint64                 `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID  uint64                 `json:"channel_id" gorm:"index;not null"`
	TargetType ChannelOverwriteTarget `json:"target_type" gorm:"not null"`
	TargetID   uint64                 `json:"target_id" gorm:"index;not null"`
	Allow      uint64                 `json:"allow" gorm:"not null;default:0"`
	Deny       uint64                 `json:"deny" gorm:"not null;default:0"`
	CreatedAt  time.Time              `json:"created_at"`
}

// FlagsFromLegacy maps legacy role permission keys (string->PermissionState) to bitflags
func FlagsFromLegacy(perms RolePermissions) uint64 {
	if perms == nil {
		return 0
	}
	var flags uint64
	for k, v := range perms {
		if v != PermissionAllow {
			continue
		}
		switch k {
		case "Admin":
			flags |= PERM_ADMINISTRATOR
		case "ManageInstance":
			flags |= PERM_MANAGE_GUILD
		case "ManageRoles":
			flags |= PERM_MANAGE_ROLES
		case "ManageChannels":
			flags |= PERM_MANAGE_CHANNELS
		case "ManageMessages":
			flags |= PERM_MANAGE_MESSAGES
		case "ViewAnalytics":
			flags |= PERM_VIEW_ANALYTICS
		case "SendMessages":
			flags |= PERM_SEND_MESSAGES
		case "SendAttachments":
			flags |= PERM_SEND_ATTACHMENTS
		case "JoinVoiceChannels":
			flags |= PERM_CONNECT
		}
	}
	return flags
}
