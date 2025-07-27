package yapyap

import (
	"time"

	"gorm.io/gorm"
)

type ChannelType uint

const (
	ChannelTypeText         ChannelType = 0
	ChannelTypeDM           ChannelType = 1
	ChannelTypeVoice        ChannelType = 2
	ChannelTypeAnnouncement ChannelType = 3
)

type Channel struct {
	gorm.Model
	ID        uint64      `json:"id" gorm:"primaryKey;autoIncrement"`
	Name      string      `json:"name"`
	Type      ChannelType `json:"type"`
	CreatedAt time.Time   `json:"created_at"`

	Messages []ChannelMessage `json:"messages" gorm:"foreignKey:ChannelID"`
}

type ChannelPermissions struct {
	gorm.Model
	ID        uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID uint64 `json:"channel_id" gorm:"index;not null"`
	UserID    uint64 `json:"user_id" gorm:"index;not null"`

	ViewChannel bool `json:"view_channel"`

	SendMessage    bool `json:"send_message"`
	SendAttachment bool `json:"send_attachment"`

	ManageMessages bool `json:"manage_messages"` // Allow deleting messages in the channel
	ManageChannel  bool `json:"manage_channel"`  // Allow managing the channel (rename, edit permissions, etc.)
}

type ChannelMessage struct {
	gorm.Model
	ID          uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID   uint64    `json:"channel_id" gorm:"index;not null"`
	UserID      uint64    `json:"user_id" gorm:"index;not null"`
	Content     string    `json:"content"`
	Attachments []string  `json:"attachments" gorm:"type:jsonb"` // Store as JSONB
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"` // Optional, for edited messages
}

type ChannelPinnedMessage struct {
	gorm.Model
	ID        uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID uint64 `json:"channel_id"`
	MessageID uint64 `json:"message_id"`
}

type MessageReaction struct {
	gorm.Model
	ID        uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	MessageID uint64 `json:"message_id"`
	UserID    uint64 `json:"user_id"`
	Emoji     string `json:"emoji"` // The emoji used for the reaction, can be a custom emoji ID or a Unicode emoji
	// For counting reactions, we'll just use the database to count the number of reactions for a message
}
