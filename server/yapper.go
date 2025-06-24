package yapyap

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	models "yapyap/models"
	"yapyap/ws"

	authHandlers "yapyap/handlers"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// YapYapConfig holds server config loaded from config.json
type YapYapConfig struct {
	InstanceName               string `json:"instance_name"`
	Host                       string `json:"host"`
	Port                       int    `json:"port"`
	JWTSecret                  string `json:"jwt_secret"`
	PostgresURL                string `json:"postgres_url"`
	PermissionCacheSyncSeconds int    `json:"permission_cache_sync_seconds"`
}

const (
	ProtocolVersion         = "0.1"
	MinimumSupportedVersion = "0.1"
)

func LoadConfig(path string) (*YapYapConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var cfg YapYapConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type YapYap struct {
	InstanceName string `json:"instance_name"`

	// Network settings
	Host string `json:"host"` // The host of the YapYap instance
	Port int    `json:"port"` // The port of the YapYap instance

	// Database settings
	PostgresURL string `json:"postgres_url"` // The URL to connect to the Postgres database
	RedisURL    string `json:"redis_url"`    // The URL to connect to the Redis

	// JWT settings
	JWTSecret []byte `json:"-"` // JWT secret for authentication (excluded from JSON)

	// Server components
	Engine   *gin.Engine
	upgrader websocket.Upgrader
	clients  map[*websocket.Conn]*Client //  Map of connected clients ( Every single client is a WebSocket connection )
	mu       sync.RWMutex
	DB       *gorm.DB
}

type Client struct {
	Conn        *websocket.Conn
	UserID      uint64
	Send        chan []byte
	Channels    map[uint64]bool                      // Channel IDs this client is a member of
	PermCache   map[uint64]models.ChannelPermissions // channelID -> permissions
	permCacheMu sync.RWMutex
}

func NewYapYap(cfg *YapYapConfig) *YapYap {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())

	db, err := gorm.Open(postgres.Open(cfg.PostgresURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to Postgres: %v", err)
	}
	db.AutoMigrate(&models.User{}, &models.UserPermissions{}, &models.UserLoginToken{}, &models.Channel{}, &models.ChannelMessage{}, &models.Role{}, &models.UserRole{})

	return &YapYap{
		InstanceName: cfg.InstanceName,
		Host:         cfg.Host,
		Port:         cfg.Port,
		JWTSecret:    []byte(cfg.JWTSecret),
		Engine:       engine,
		DB:           db,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// Configure CORS as needed
				return true
			},
		},
		clients: make(map[*websocket.Conn]*Client),
	}
}

func (s *YapYap) HandleWebSocket(c *gin.Context) {
	w := c.Writer
	r := c.Request
	// Extract token from query parameter or header
	token := r.URL.Query().Get("token")
	if token == "" {
		token = authHandlers.ExtractTokenFromHeader(r)
	}

	if token == "" {
		http.Error(w, "Authentication token required", http.StatusUnauthorized)
		return
	}

	// Validate JWT token
	claims, err := authHandlers.ValidateJWT(token, s.JWTSecret)
	if err != nil {
		http.Error(w, "Invalid authentication token", http.StatusUnauthorized)
		return
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	// Create client with authenticated user ID
	client := &Client{
		Conn:      conn,
		UserID:    uint64(claims.UserID),
		Send:      make(chan []byte, 256),
		Channels:  make(map[uint64]bool),
		PermCache: make(map[uint64]models.ChannelPermissions),
	}

	// Register client
	s.mu.Lock()
	s.clients[conn] = client
	s.mu.Unlock()

	log.Printf("User %d (%s) connected via WebSocket", claims.UserID, claims.Username)

	// Send welcome message
	welcomeEvent := ws.WebSocketEvent{
		Type: "0002", // EventTypeServerInfo
		Data: map[string]interface{}{
			"instance_name": s.InstanceName,
			"message":       "Connected successfully",
			"user_id":       claims.UserID,
			"username":      claims.Username,
		},
	}
	s.sendToClient(client, welcomeEvent)

	// Sync permissions for the new client
	s.SyncPermissions(client)

	// Start goroutines for reading and writing
	go s.handleClientRead(client)
	go s.handleClientWrite(client)

	// Wait for the connection to close by checking if client is still registered
	for {
		s.mu.RLock()
		_, exists := s.clients[conn]
		s.mu.RUnlock()

		if !exists {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *YapYap) handleClientRead(client *Client) {
	defer func() {
		log.Printf("Client %d disconnected", client.UserID)
		s.removeClient(client)
		client.Conn.Close()
	}()

	// Set read deadline and pong handler for heartbeat
	client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		var event ws.WebSocketEvent
		err := client.Conn.ReadJSON(&event)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error for user %d: %v", client.UserID, err)
			}
			break
		}

		// Handle incoming events
		s.handleWebSocketEvent(client, event)
	}
}

func (s *YapYap) handleClientWrite(client *Client) {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		client.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.Send:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// The send channel was closed
				client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := client.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Printf("WebSocket write error for user %d: %v", client.UserID, err)
				return
			}

		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("WebSocket ping error for user %d: %v", client.UserID, err)
				return
			}
		}
	}
}

// SyncPermissions loads all channel permissions for this user from DB into cache
func (s *YapYap) SyncPermissions(client *Client) {
	var perms []models.ChannelPermissions
	err := s.DB.Where("user_id = ?", client.UserID).Find(&perms).Error
	if err != nil {
		log.Printf("Failed to sync permissions for user %d: %v", client.UserID, err)
		return
	}
	client.permCacheMu.Lock()
	client.PermCache = make(map[uint64]models.ChannelPermissions)
	for _, p := range perms {
		client.PermCache[p.ChannelID] = p
	}
	client.permCacheMu.Unlock()
}

// userCanSendMessage checks local cache first, falls back to DB and updates cache if needed
func (s *YapYap) userCanSendMessage(client *Client, channelID uint64) bool {
	client.permCacheMu.RLock()
	perm, ok := client.PermCache[channelID]
	client.permCacheMu.RUnlock()
	if ok {
		return perm.SendMessage
	}
	// Fallback to DB
	var dbPerm models.ChannelPermissions
	err := s.DB.Where("user_id = ? AND channel_id = ?", client.UserID, channelID).First(&dbPerm).Error
	if err != nil {
		return false
	}
	client.permCacheMu.Lock()
	client.PermCache[channelID] = dbPerm
	client.permCacheMu.Unlock()
	return dbPerm.SendMessage
}

// UpdatePermission updates local cache and writes to DB
func (s *YapYap) UpdatePermission(client *Client, perm models.ChannelPermissions) error {
	client.permCacheMu.Lock()
	client.PermCache[perm.ChannelID] = perm
	client.permCacheMu.Unlock()
	return s.DB.Save(&perm).Error
}

func (s *YapYap) handleWebSocketEvent(client *Client, event ws.WebSocketEvent) {
	switch event.Type {
	case "0000": // EventTypeClockTick - Heartbeat
		// Respond with server status
		response := ws.WebSocketEvent{
			Type: "0000",
			Data: map[string]interface{}{
				"timestamp":       time.Now().Unix(),
				"connected_users": s.GetConnectedClientsCount(),
			},
		}
		s.sendToClient(client, response)

	case "3000": // EventTypeMessageCreated
		// Handle message creation
		// Expect event.Data to be a map with channel_id, content, attachments
		msgData, ok := event.Data.(map[string]interface{})
		if !ok {
			log.Printf("Invalid message data from user %d: %v", client.UserID, event.Data)
			return
		}
		channelID, _ := msgData["channel_id"].(float64)
		content, _ := msgData["content"].(string)
		attachments, _ := msgData["attachments"].(string)
		if content == "" || channelID == 0 {
			log.Printf("Missing content or channel_id in message from user %d", client.UserID)
			return
		}
		if !s.userCanSendMessage(client, uint64(channelID)) {
			log.Printf("User %d does not have permission to send messages in channel %d", client.UserID, uint64(channelID))
			return
		}
		msg := models.ChannelMessage{
			ChannelID:   uint64(channelID),
			UserID:      client.UserID,
			Content:     content,
			Attachments: attachments,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := s.DB.Create(&msg).Error; err != nil {
			log.Printf("Failed to save message: %v", err)
			return
		}
		// Broadcast the message to all clients (or just channel members in future)
		broadcast := ws.WebSocketEvent{
			Type: "3000",
			Data: msg,
		}
		s.BroadcastToChannel(uint64(channelID), broadcast)

	case "2000": // EventTypeChannelCreated
		// Only allow admins to create channels
		var user models.User
		if err := s.DB.First(&user, client.UserID).Error; err != nil {
			log.Printf("User %d not found for channel creation", client.UserID)
			return
		}
		if !userHasAdminOrManageChannels(&user) {
			log.Printf("User %d is not admin and cannot create channels", client.UserID)
			return
		}
		chData, ok := event.Data.(map[string]interface{})
		if !ok {
			log.Printf("Invalid channel data from user %d: %v", client.UserID, event.Data)
			return
		}
		name, _ := chData["name"].(string)
		chType, _ := chData["type"].(float64)
		if name == "" {
			log.Printf("Missing channel name from user %d", client.UserID)
			return
		}
		channel := models.Channel{
			Name:      name,
			Type:      models.ChannelType(chType),
			CreatedAt: time.Now(),
		}
		if err := s.DB.Create(&channel).Error; err != nil {
			log.Printf("Failed to create channel: %v", err)
			return
		}
		// Broadcast new channel to all users
		broadcast := ws.WebSocketEvent{
			Type: "2000",
			Data: channel,
		}
		s.BroadcastToAll(broadcast)

	case "2001": // EventTypeChannelUpdated
		var user models.User
		if err := s.DB.Preload("Permissions").First(&user, client.UserID).Error; err != nil {
			log.Printf("User %d not found for channel update", client.UserID)
			return
		}
		if !userHasAdminOrManageChannels(&user) {
			log.Printf("User %d is not admin and cannot update channels", client.UserID)
			return
		}
		chData, ok := event.Data.(map[string]interface{})
		if !ok {
			log.Printf("Invalid channel update data from user %d: %v", client.UserID, event.Data)
			return
		}
		id, _ := chData["id"].(float64)
		name, _ := chData["name"].(string)
		chType, _ := chData["type"].(float64)
		if id == 0 {
			log.Printf("Missing channel id from user %d", client.UserID)
			return
		}
		var channel models.Channel
		if err := s.DB.First(&channel, uint64(id)).Error; err != nil {
			log.Printf("Channel %d not found for update", uint64(id))
			return
		}
		if name != "" {
			channel.Name = name
		}
		if chType >= 0 {
			channel.Type = models.ChannelType(chType)
		}
		if err := s.DB.Save(&channel).Error; err != nil {
			log.Printf("Failed to update channel: %v", err)
			return
		}
		broadcast := ws.WebSocketEvent{
			Type: "2001",
			Data: channel,
		}
		s.BroadcastToAll(broadcast)
	case "2002": // EventTypeChannelDeleted
		var user models.User
		if err := s.DB.Preload("Permissions").First(&user, client.UserID).Error; err != nil {
			log.Printf("User %d not found for channel delete", client.UserID)
			return
		}
		if !userHasAdminOrManageChannels(&user) {
			log.Printf("User %d is not admin and cannot delete channels", client.UserID)
			return
		}
		chData, ok := event.Data.(map[string]interface{})
		if !ok {
			log.Printf("Invalid channel delete data from user %d: %v", client.UserID, event.Data)
			return
		}
		id, _ := chData["id"].(float64)
		if id == 0 {
			log.Printf("Missing channel id from user %d", client.UserID)
			return
		}
		if err := s.DB.Delete(&models.Channel{}, uint64(id)).Error; err != nil {
			log.Printf("Failed to delete channel: %v", err)
			return
		}
		broadcast := ws.WebSocketEvent{
			Type: "2002",
			Data: map[string]interface{}{"id": uint64(id)},
		}
		s.BroadcastToAll(broadcast)
	case "2100": // EventTypePermissionUpdate
		// Only admins or ManageChannels can update permissions
		var user models.User
		if err := s.DB.Preload("Permissions").First(&user, client.UserID).Error; err != nil {
			log.Printf("User %d not found for permission update", client.UserID)
			return
		}
		if !userHasAdminOrManageChannels(&user) {
			log.Printf("User %d is not admin and cannot update permissions", client.UserID)
			return
		}
		permData, ok := event.Data.(map[string]interface{})
		if !ok {
			log.Printf("Invalid permission update data from user %d: %v", client.UserID, event.Data)
			return
		}
		targetUserID, _ := permData["user_id"].(float64)
		channelID, _ := permData["channel_id"].(float64)
		view, _ := permData["view_channel"].(bool)
		send, _ := permData["send_message"].(bool)
		manage, _ := permData["manage_channel"].(bool)
		if targetUserID == 0 || channelID == 0 {
			log.Printf("Missing user_id or channel_id in permission update from user %d", client.UserID)
			return
		}
		var perm models.ChannelPermissions
		err := s.DB.Where("user_id = ? AND channel_id = ?", uint64(targetUserID), uint64(channelID)).First(&perm).Error
		if err != nil {
			// Create new permission if not found
			perm = models.ChannelPermissions{
				UserID:    uint64(targetUserID),
				ChannelID: uint64(channelID),
			}
		}
		perm.ViewChannel = view
		perm.SendMessage = send
		perm.ManageChannel = manage
		if err := s.DB.Save(&perm).Error; err != nil {
			log.Printf("Failed to update permission: %v", err)
			return
		}
		// Optionally, notify the affected user if connected
		for _, c := range s.clients {
			if c.UserID == uint64(targetUserID) {
				s.SyncPermissions(c)
				break
			}
		}
		log.Printf("Permissions updated for user %d in channel %d", uint64(targetUserID), uint64(channelID))
	case "2101": // EventTypeChannelJoin (client requests to join/view a channel)
		joinData, ok := event.Data.(map[string]interface{})
		if !ok {
			log.Printf("Invalid join data from user %d: %v", client.UserID, event.Data)
			return
		}
		channelID, _ := joinData["channel_id"].(float64)
		if channelID == 0 {
			log.Printf("Missing channel_id in join from user %d", client.UserID)
			return
		}
		client.permCacheMu.RLock()
		perm, ok := client.PermCache[uint64(channelID)]
		client.permCacheMu.RUnlock()
		if !ok || !perm.ViewChannel {
			log.Printf("User %d does not have view permission for channel %d", client.UserID, uint64(channelID))
			return
		}
		client.Channels[uint64(channelID)] = true
		// Send recent messages for the channel
		var messages []models.ChannelMessage
		err := s.DB.Where("channel_id = ?", uint64(channelID)).Order("created_at desc").Limit(50).Find(&messages).Error
		if err != nil {
			log.Printf("Failed to fetch messages for channel %d: %v", uint64(channelID), err)
			return
		}
		resp := ws.WebSocketEvent{
			Type: "3100", // EventTypeChannelHistory
			Data: map[string]interface{}{
				"channel_id": uint64(channelID),
				"messages":   messages,
			},
		}
		s.sendToClient(client, resp)

	default:
		log.Printf("Unknown event type %s from user %d", event.Type, client.UserID)
	}
}

func (s *YapYap) sendToClient(client *Client, event ws.WebSocketEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to marshal event: %v", err)
		return
	}

	select {
	case client.Send <- data:
		// Message sent successfully
	default:
		// Client send channel is full or closed, remove the client
		log.Printf("Client %d send channel blocked, removing client", client.UserID)
		s.removeClient(client)
	}
}

func (s *YapYap) BroadcastToAll(event ws.WebSocketEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to marshal broadcast event: %v", err)
		return
	}

	s.mu.RLock()
	clients := make([]*Client, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.RUnlock()

	for _, client := range clients {
		select {
		case client.Send <- data:
			// Message sent successfully
		default:
			// Client send channel is full or closed, remove the client
			log.Printf("Client %d send channel blocked during broadcast, removing client", client.UserID)
			s.removeClient(client)
		}
	}
}

func (s *YapYap) BroadcastToChannel(channelID uint64, event ws.WebSocketEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to marshal broadcast event: %v", err)
		return
	}

	s.mu.RLock()
	for _, client := range s.clients {
		client.permCacheMu.RLock()
		perm, ok := client.PermCache[channelID]
		client.permCacheMu.RUnlock()
		if ok && perm.ViewChannel {
			select {
			case client.Send <- data:
				// Message sent successfully
			default:
				log.Printf("Client %d send channel blocked during channel broadcast, removing client", client.UserID)
				s.removeClient(client)
			}
		}
	}
	s.mu.RUnlock()
}

func (s *YapYap) SetupRoutes() {
	// Serve static files
	s.Engine.Static("/static", "./assets")

	// WebSocket endpoint
	s.Engine.GET("/ws", s.HandleWebSocket)

	// API routes
	api := s.Engine.Group("/api/v1")
	{
		api.POST("/auth/login", authHandlers.LoginHandler(s.DB, s.JWTSecret))
		api.POST("/auth/register", authHandlers.RegisterHandler(s.DB, s.JWTSecret))
		api.GET("/users/me", authHandlers.AuthMiddleware(s.JWTSecret), authHandlers.HandleGetCurrentUser)
	}

	// Health check
	s.Engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":                "ok",
			"instance":              s.InstanceName,
			"protocol_version":      ProtocolVersion,
			"min_supported_version": MinimumSupportedVersion,
		})
	})
}

func (s *YapYap) GracefullExit() {
	// Close all WebSocket connections
	s.mu.Lock()
	defer s.mu.Unlock()
	for conn, client := range s.clients {
		log.Printf("Closing WebSocket connection for user %d", client.UserID)
		conn.Close()
		// Close the send channel safely
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// Channel was already closed, ignore the panic
				}
			}()
			close(client.Send)
		}()
		delete(s.clients, conn)
	}
	log.Println("WebSocket connections closed")

	// TODO: Save Server state to Database

	log.Println("YapYap server shutting down")
	os.Exit(0) // Exit the application gracefully
}

func (s *YapYap) Start() {
	s.SetupRoutes()

	sigs := make(chan os.Signal, 1)
	// Handle graceful shutdown
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	done := make(chan bool, 1)
	go func() {
		<-sigs
		s.GracefullExit()

		done <- true
	}()

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	log.Printf(("Starting YapYap instance on %s"), addr)

	if err := s.Engine.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	<-done // Wait for shutdown signal

}

// removeClient safely removes a client from the clients map
func (s *YapYap) removeClient(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.clients[client.Conn]; exists {
		delete(s.clients, client.Conn)
		// Close the send channel safely
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// Channel was already closed, ignore the panic
				}
			}()
			close(client.Send)
		}()
	}
}

// GetConnectedClientsCount returns the number of currently connected clients
func (s *YapYap) GetConnectedClientsCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// Periodically sync all connected clients' permissions
func (s *YapYap) StartPermissionSyncer(intervalSec int) {
	go func() {
		for {
			time.Sleep(time.Duration(intervalSec) * time.Second)
			s.mu.RLock()
			for _, client := range s.clients {
				s.SyncPermissions(client)
			}
			s.mu.RUnlock()
		}
	}()
}

type PermissionState string

const (
	PermissionAllow PermissionState = "allow"
	PermissionDeny  PermissionState = "deny"
	PermissionUnset PermissionState = "unset"
)

// PermissionCheckType is a string for permission keys (e.g. "ViewChannel")
type PermissionCheckType string

// GetUserPermission returns the user's explicit permission for a given key, or PermissionUnset
func (s *YapYap) GetUserPermission(userID uint64, permKey PermissionCheckType) PermissionState {
	var up models.UserPermissions
	if err := s.DB.Where("user_id = ?", userID).First(&up).Error; err == nil {
		switch permKey {
		case "ViewChannel":
			if up.ViewAnalytics {
				return PermissionAllow
			} // Example mapping
			return PermissionUnset
		case "SendMessage":
			if up.SendMessages {
				return PermissionAllow
			}
			return PermissionUnset
			// ...add other permission mappings as needed...
		}
	}
	return PermissionUnset
}

// GetRolePermission returns the merged role permission for a user for a given key
func (s *YapYap) GetRolePermission(userID uint64, permKey PermissionCheckType) PermissionState {
	var userRoles []models.UserRole
	if err := s.DB.Where("user_id = ?", userID).Find(&userRoles).Error; err != nil {
		return PermissionUnset
	}
	allow := false
	deny := false
	for _, ur := range userRoles {
		var role models.Role
		if err := s.DB.First(&role, ur.RoleID).Error; err != nil {
			continue
		}
		perms := role.Permissions // Already a map[string]PermissionState
		state := perms[string(permKey)]
		if state == models.PermissionAllow {
			allow = true
		}
		if state == models.PermissionDeny {
			deny = true
		}
	}
	if deny {
		return PermissionDeny
	}
	if allow {
		return PermissionAllow
	}
	return PermissionUnset
}

// Unified permission check: User > Role
func (s *YapYap) HasPermission(userID uint64, permKey PermissionCheckType) bool {
	userPerm := s.GetUserPermission(userID, permKey)
	if userPerm != PermissionUnset {
		return userPerm == PermissionAllow
	}
	rolePerm := s.GetRolePermission(userID, permKey)
	return rolePerm == PermissionAllow
}

func userHasAdminOrManageChannels(user *models.User) bool {
	for _, perm := range user.Permissions {
		if perm.Admin || perm.ManageChannels {
			return true
		}
	}
	return false
}
