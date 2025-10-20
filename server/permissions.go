package yapyap

import (
	models "yapyap/models"
)

// resolveGlobalPermissions aggregates role flags for a user and accounts for ADMINISTRATOR
func (s *YapYap) resolveGlobalPermissions(userID uint64) (perms uint64) {
	// Aggregate from roles
	var userRoles []models.UserRole
	if err := s.DB.Where("user_id = ?", userID).Find(&userRoles).Error; err != nil {
		return 0
	}
	for _, ur := range userRoles {
		var role models.Role
		if err := s.DB.First(&role, ur.RoleID).Error; err != nil {
			continue
		}
		// Convert legacy map to flags
		perms |= models.FlagsFromLegacy(role.Permissions)
	}

	// Map legacy user-specific booleans as an additional role
	var up models.UserPermissions
	if err := s.DB.Where("user_id = ?", userID).First(&up).Error; err == nil {
		if up.Admin {
			perms |= models.PERM_ADMINISTRATOR
		}
		if up.ManageInstance {
			perms |= models.PERM_MANAGE_GUILD
		}
		if up.ManageUsers {
			perms |= models.PERM_MANAGE_ROLES // Closest mapping
		}
		if up.ManageChannels {
			perms |= models.PERM_MANAGE_CHANNELS
		}
		if up.ManageMessages {
			perms |= models.PERM_MANAGE_MESSAGES
		}
		if up.SendMessages {
			perms |= models.PERM_SEND_MESSAGES
		}
		if up.SendAttachments {
			perms |= models.PERM_SEND_ATTACHMENTS
		}
		if up.JoinVoiceChannels {
			perms |= models.PERM_CONNECT
		}
		if up.ViewAnalytics {
			perms |= models.PERM_VIEW_ANALYTICS
		}
	}

	return perms
}

// ResolveChannelPermissions applies Discord-like overwrites for a channel
func (s *YapYap) ResolveChannelPermissions(userID, channelID uint64) uint64 {
	perms := s.resolveGlobalPermissions(userID)

	// Short-circuit admin
	if perms&models.PERM_ADMINISTRATOR != 0 {
		return ^uint64(0) // all bits
	}

	// Apply @everyone overwrite first (we do not model @everyone role explicitly; skip)
	// Apply role overwrites (aggregate allow/deny across roles)
	var userRoles []models.UserRole
	_ = s.DB.Where("user_id = ?", userID).Find(&userRoles).Error

	var roleIDs []uint64
	for _, ur := range userRoles {
		roleIDs = append(roleIDs, ur.RoleID)
	}

	var roleOverwrites []models.ChannelOverwrite
	if len(roleIDs) > 0 {
		_ = s.DB.Where("channel_id = ? AND target_type = ? AND target_id IN ?", channelID, models.OverwriteTargetRole, roleIDs).Find(&roleOverwrites).Error
	}

	var aggAllow, aggDeny uint64
	for _, ow := range roleOverwrites {
		aggAllow |= ow.Allow
		aggDeny |= ow.Deny
	}
	perms = (perms &^ aggDeny) | aggAllow

	// Apply member overwrite last
	var memberOverwrite models.ChannelOverwrite
	if err := s.DB.Where("channel_id = ? AND target_type = ? AND target_id = ?", channelID, models.OverwriteTargetMember, userID).First(&memberOverwrite).Error; err == nil {
		perms = (perms &^ memberOverwrite.Deny) | memberOverwrite.Allow
	}

	return perms
}

// HasGlobalFlag checks a single permission flag in aggregated global perms
func (s *YapYap) HasGlobalFlag(userID uint64, flag uint64) bool {
	perms := s.resolveGlobalPermissions(userID)
	if perms&models.PERM_ADMINISTRATOR != 0 {
		return true
	}
	return perms&flag != 0
}

// HasChannelFlag checks a channel-scoped permission flag
func (s *YapYap) HasChannelFlag(userID, channelID uint64, flag uint64) bool {
	perms := s.ResolveChannelPermissions(userID, channelID)
	return perms&flag != 0
}
