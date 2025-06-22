package ws

type EventType uint

const (

	// System-related events // prefix (0xxx)
	EventTypeClockTick    EventType = 0000 // Clock tick event, sent every second to all connected clients (basically a heartbeat)
	EventTypeServerStatus EventType = 0001 // Server status update event, sent to all connected clients (e.g., server is shutting down, maintenance mode, etc.)
	EventTypeServerInfo   EventType = 0002 // Server info event, sent to all connected clients (e.g., server version, uptime, etc.)

	// User-related events // prefix (1xxx)
	EventTypeUserStatusUpdate EventType = 1000 // User status update event, sent to all users in the system
	EventTypeUserCreated      EventType = 1001 // User registration or creation event, this will only be sent to the user themselves
	EventTypeUserUpdated      EventType = 1002 // User updates their profile
	EventTypeUserDeleted      EventType = 1003 // User deletion event, this will only be sent to the user themselves

	// Channel-related events // prefix (2xxx)
	EventTypeChannelCreated EventType = 2000 //  Channel creation event, sent to all users in the system
	EventTypeChannelUpdated EventType = 2001 // Channel update event, sent to all users in the system
	EventTypeChannelDeleted EventType = 2002 // Channel deletion event, sent to all users in the system

	// MESSAGE-related events // prefix (3xxx)
	EventTypeMessageCreated EventType = 3000 // Message creation event, sent to all users who can view the channel
	EventTypeMessageUpdated EventType = 3001 // Message update event, sent to all users  who can view the channel
	EventTypeMessageDeleted EventType = 3002 // Message deletion event, sent to all users who can view the channel

	EventTypeMessageReactionAdded   EventType = 3003 // Message reaction added event, sent to all users who can view the channel
	EventTypeMessageReactionRemoved EventType = 3004 // Message reaction removed event, sent to all users who can view the channel
	EventTypeMessagePinned          EventType = 3005 // Message pinned event, sent to all users who can view the channel
	EventTypeMessageUnpinned        EventType = 3006 // Message unpinned event, sent to all users who can view the channel

)

// Am Event Message sent over a WebSocket connection.
type WebSocketEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"` // The data can be any type, but should be JSON serializable.
}
