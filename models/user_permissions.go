package yapyap

import "gorm.io/gorm"

type UserPermissions struct {
	gorm.Model
	UserID            uint `json:"user_id" gorm:"not null;index"`
	ViewAnalytics     bool `json:"view_analytics"`
	SendMessages      bool `json:"send_messages"`
	SendAttachments   bool `json:"send_attachments"`
	JoinVoiceChannels bool `json:"join_voice_channels"`
	ManageMessages    bool `json:"manage_messages"`
	ManageUsers       bool `json:"manage_users"`
	ManageChannels    bool `json:"manage_channels"`
	ManageInstance    bool `json:"manage_instance"`
	Admin             bool `json:"admin"`
}
