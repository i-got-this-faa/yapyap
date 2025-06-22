package yapyap

type ChannelType uint

const (
	ChannelTypeText         ChannelType = 0
	ChannelTypeDM           ChannelType = 1
	ChannelTypeVoice        ChannelType = 2
	ChannelTypeAnnouncement ChannelType = 3
)

type Channel struct {
	ID        uint64      `json:"id"`
	Name      string      `json:"name"`
	Type      ChannelType `json:"type"`
	CreatedAt string      `json:"created_at"`
}

type ChannelPermissions struct {
	// (ChannelID, UserID) -> Permission
	ChannelID uint64 `json:"channel_id"`
	UserID    uint64 `json:"user_id"`

	ViewChannel bool `json:"view_channel"`

	SendMessage    bool `json:"send_message"`
	SendAttachment bool `json:"send_attachment"`

	ManageMessages bool `json:"manage_messages"` // Allow deleting messages in the channel
	ManageChannel  bool `json:"manage_channel"`  // Allow managing the channel (rename, edit permissions, etc.)
}

type ChannelMessage struct {
	ID          uint64   `json:"id"`
	ChannelID   uint64   `json:"channel_id"`
	UserID      uint64   `json:"user_id"`
	Content     string   `json:"content"`
	Attachments []string `json:"attachments"` // URLs to attachments, if any
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"` // Optional, for edited messages
}

type ChannelPinnedMessage struct {
	ChannelID uint64 `json:"channel_id"`
	MessageID uint64 `json:"message_id"`
}

type MessageReaction struct {
	MessageID uint64 `json:"message_id"`
	UserID    uint64 `json:"user_id"`
	Emoji     string `json:"emoji"` // The emoji used for the reaction, can be a custom emoji ID or a Unicode emoji

	// For counting reactions, we'll just use the database to count the number of reactions for a message
}
