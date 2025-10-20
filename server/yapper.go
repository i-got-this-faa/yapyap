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
	"yapyap/utils"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// YapYapConfig holds server config loaded from config.json
type YapYapConfig struct {
	InstanceName               string   `json:"instance_name"`
	Host                       string   `json:"host"`
	Port                       int      `json:"port"`
	JWTSecret                  string   `json:"jwt_secret"`
	PostgresURL                string   `json:"postgres_url"`
	PermissionCacheSyncSeconds int      `json:"permission_cache_sync_seconds"`
	AdminUserIDs               []uint64 `json:"admin_user_ids"`
}

// initializeAdminRoles assigns admin roles to specified user IDs
func initializeAdminRoles(db *gorm.DB, adminUserIDs []uint64, logger *utils.Logger) error {
	// First, ensure the admin role exists
	var adminRole models.Role
	result := db.Where("name = ?", "admin").First(&adminRole)

	if result.Error != nil {
		// Admin role doesn't exist, create it
		adminPermissions := models.RolePermissions{
			"ViewAnalytics":     models.PermissionAllow,
			"SendMessages":      models.PermissionAllow,
			"SendAttachments":   models.PermissionAllow,
			"JoinVoiceChannels": models.PermissionAllow,
			"ManageMessages":    models.PermissionAllow,
			"ManageUsers":       models.PermissionAllow,
			"ManageChannels":    models.PermissionAllow,
			"ManageInstance":    models.PermissionAllow,
			"Admin":             models.PermissionAllow,
		}

		adminRole = models.Role{
			Name:        "admin",
			Permissions: adminPermissions,
		}

		if err := db.Create(&adminRole).Error; err != nil {
			log.Printf("Failed to create admin role: %v", err)
			return err
		}

		log.Printf("✅ Created admin role")

		// Log admin role creation
		metadata := models.LogMetadata{
			"role_name": "admin",
			"source":    "startup_initialization",
		}
		logger.LogSystemEvent(models.LogLevelInfo, models.LogActionRoleCreate,
			"Admin role created during startup", metadata)
	} else {
		log.Printf("ℹ️  Admin role already exists")
	}

	// Assign admin role to specified users
	for _, userID := range adminUserIDs {
		// Check if user exists
		var user models.User
		if err := db.Where("id = ?", userID).First(&user).Error; err != nil {
			log.Printf("⚠️  User ID %d not found, skipping admin role assignment", userID)
			continue
		}

		// Check if user already has admin role
		var existingUserRole models.UserRole
		roleResult := db.Where("user_id = ? AND role_id = ?", userID, adminRole.ID).First(&existingUserRole)

		if roleResult.Error == nil {
			log.Printf("ℹ️  User %s (ID: %d) already has admin role", user.Username, userID)
			continue
		}

		// Assign admin role
		userRole := models.UserRole{
			UserID: userID,
			RoleID: uint64(adminRole.ID),
		}

		if err := db.Create(&userRole).Error; err != nil {
			log.Printf("Failed to assign admin role to user ID %d: %v", userID, err)
			continue
		}

		log.Printf("✅ Assigned admin role to user: %s (ID: %d)", user.Username, userID)

		// Log admin role assignment
		metadata := models.LogMetadata{
			"username": user.Username,
			"user_id":  userID,
			"role":     "admin",
			"source":   "startup_initialization",
		}
		logger.LogSystemEvent(models.LogLevelInfo, models.LogActionRoleAssign,
			fmt.Sprintf("Admin role assigned to user: %s", user.Username), metadata)
	}

	return nil
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
	Logger   *utils.Logger // Database logger
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
	db.AutoMigrate(&models.User{}, &models.UserPermissions{}, &models.UserLoginToken{}, &models.Channel{}, &models.ChannelMessage{}, &models.Role{}, &models.UserRole{}, &models.Log{}, &models.ChannelOverwrite{})

	// Create logger
	logger := utils.NewLogger(db)

	// Initialize admin users from config
	if err := initializeAdminRoles(db, cfg.AdminUserIDs, logger); err != nil {
		log.Printf("Failed to initialize admin roles: %v", err)
	}

	return &YapYap{
		InstanceName: cfg.InstanceName,
		Host:         cfg.Host,
		Port:         cfg.Port,
		JWTSecret:    []byte(cfg.JWTSecret),
		Engine:       engine,
		DB:           db,
		Logger:       logger,
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

	// Log WebSocket connection
	s.Logger.LogWithUser(models.LogLevelInfo, models.LogAction("websocket.connect"),
		fmt.Sprintf("User %s connected via WebSocket", claims.Username),
		uint64(claims.UserID), c)

	// Send welcome message
	welcomeEvent := ws.WebSocketEvent{
		Type: ws.EventTypeServerInfo,
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

	// Set read deadline and pong handler for heartbeat only
	client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	client.Conn.SetPongHandler(func(string) error {
		client.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Only handle ping/pong for connection keepalive
	for {
		_, _, err := client.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error for user %d: %v", client.UserID, err)
			}
			break
		}
		// Ignore all incoming messages - WebSocket is now write-only from server
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

// REST API Handlers for client actions

// HandleCreateMessage creates a new message via REST API
func (s *YapYap) HandleCreateMessage(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var msgData struct {
		ChannelID   uint64   `json:"channel_id" binding:"required"`
		Content     string   `json:"content" binding:"required"`
		Attachments []string `json:"attachments,omitempty"`
	}

	if err := c.ShouldBindJSON(&msgData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check permissions (channel-aware)
	if !s.HasChannelFlag(userID.(uint64), msgData.ChannelID, models.PERM_SEND_MESSAGES) && !s.HasGlobalFlag(userID.(uint64), models.PERM_ADMINISTRATOR) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	msg := models.ChannelMessage{
		ChannelID:   msgData.ChannelID,
		UserID:      userID.(uint64),
		Content:     msgData.Content,
		Attachments: msgData.Attachments,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.DB.Create(&msg).Error; err != nil {
		// Log the error
		s.Logger.LogWithUser(models.LogLevelError, models.LogActionMessageSend,
			fmt.Sprintf("Failed to create message in channel %d: %v", msgData.ChannelID, err),
			userID.(uint64), c)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create message"})
		return
	}

	// Log successful message creation
	s.Logger.LogWithTarget(models.LogLevelInfo, models.LogActionMessageSend,
		fmt.Sprintf("Message sent to channel %d", msgData.ChannelID),
		&[]uint64{userID.(uint64)}[0], msg.ID, "message", c)

	// Broadcast the message to all WebSocket clients
	broadcast := ws.WebSocketEvent{
		Type: ws.EventTypeMessageCreated,
		Data: msg,
	}
	s.BroadcastToChannel(msgData.ChannelID, broadcast)

	c.JSON(http.StatusCreated, msg)
}

// HandleCreateChannel creates a new channel via REST API
func (s *YapYap) HandleCreateChannel(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check if user has admin or manage channels permission (unified)
	if !(s.HasPermission(userID.(uint64), "Admin") || s.HasPermission(userID.(uint64), "ManageChannels")) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var chData struct {
		Name string             `json:"name" binding:"required"`
		Type models.ChannelType `json:"type"`
	}

	if err := c.ShouldBindJSON(&chData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	channel := models.Channel{
		Name:      chData.Name,
		Type:      chData.Type,
		CreatedAt: time.Now(),
	}

	if err := s.DB.Create(&channel).Error; err != nil {
		// Log the error
		s.Logger.LogWithUser(models.LogLevelError, models.LogActionChannelCreate,
			fmt.Sprintf("Failed to create channel '%s': %v", chData.Name, err),
			userID.(uint64), c)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create channel"})
		return
	}

	// Log successful channel creation
	s.Logger.LogWithTarget(models.LogLevelInfo, models.LogActionChannelCreate,
		fmt.Sprintf("Channel '%s' created", chData.Name),
		&[]uint64{userID.(uint64)}[0], channel.ID, "channel", c)

	// Broadcast new channel to all WebSocket clients
	broadcast := ws.WebSocketEvent{
		Type: ws.EventTypeChannelCreated,
		Data: channel,
	}
	s.BroadcastToAll(broadcast)

	c.JSON(http.StatusCreated, channel)
}

// HandleUpdateChannel updates a channel via REST API
func (s *YapYap) HandleUpdateChannel(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Channel ID required"})
		return
	}

	// Check permissions (unified)
	if !(s.HasPermission(userID.(uint64), "Admin") || s.HasPermission(userID.(uint64), "ManageChannels")) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var updateData struct {
		Name *string             `json:"name,omitempty"`
		Type *models.ChannelType `json:"type,omitempty"`
	}

	if err := c.ShouldBindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var channel models.Channel
	if err := s.DB.First(&channel, channelID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
		return
	}

	if updateData.Name != nil {
		channel.Name = *updateData.Name
	}
	if updateData.Type != nil {
		channel.Type = *updateData.Type
	}

	if err := s.DB.Save(&channel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update channel"})
		return
	}

	// Broadcast update to all WebSocket clients
	broadcast := ws.WebSocketEvent{
		Type: ws.EventTypeChannelUpdated,
		Data: channel,
	}
	s.BroadcastToAll(broadcast)

	c.JSON(http.StatusOK, channel)
}

// HandleDeleteChannel deletes a channel via REST API
func (s *YapYap) HandleDeleteChannel(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Channel ID required"})
		return
	}

	// Check permissions (unified)
	if !(s.HasPermission(userID.(uint64), "Admin") || s.HasPermission(userID.(uint64), "ManageChannels")) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	// Get channel info before deletion for broadcast
	var channel models.Channel
	if err := s.DB.First(&channel, channelID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
		return
	}

	if err := s.DB.Delete(&channel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete channel"})
		return
	}

	// Broadcast deletion to all WebSocket clients
	broadcast := ws.WebSocketEvent{
		Type: ws.EventTypeChannelDeleted,
		Data: map[string]interface{}{
			"id": channel.ID,
		},
	}
	s.BroadcastToAll(broadcast)

	c.JSON(http.StatusOK, gin.H{"message": "Channel deleted"})
}

// HandleGetChannelMessages gets messages for a channel via REST API
func (s *YapYap) HandleGetChannelMessages(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Channel ID required"})
		return
	}

	// Check view permissions via flags
	chID := parseUint64(channelID)
	if !s.HasChannelFlag(userID.(uint64), chID, models.PERM_VIEW_CHANNEL) && !s.HasGlobalFlag(userID.(uint64), models.PERM_ADMINISTRATOR) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	limit := 50 // Default limit
	if l := c.Query("limit"); l != "" {
		if parsed := parseLimit(l); parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	before := c.Query("before")
	after := c.Query("after")
	around := c.Query("around")

	// Ensure only one of before/after/around is used
	used := 0
	if before != "" {
		used++
	}
	if after != "" {
		used++
	}
	if around != "" {
		used++
	}
	if used > 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Use only one of before, after, or around"})
		return
	}

	// No cursor params: latest messages, DESC
	if used == 0 {
		var messages []models.ChannelMessage
		if err := s.DB.Where("channel_id = ?", chID).Order("created_at desc").Limit(limit).Find(&messages).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch messages"})
			return
		}
		c.JSON(http.StatusOK, messages)
		return
	}

	// Helper to fetch pivot message
	getPivot := func(idStr string) (*models.ChannelMessage, bool) {
		mid := parseUint64(idStr)
		if mid == 0 {
			return nil, false
		}
		var pivot models.ChannelMessage
		if err := s.DB.Where("id = ? AND channel_id = ?", mid, chID).First(&pivot).Error; err != nil {
			return nil, false
		}
		return &pivot, true
	}

	if around != "" {
		pivot, ok := getPivot(around)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Pivot message not found"})
			return
		}
		half := limit / 2

		// Newer than pivot (more recent), order DESC, limit half
		var newer []models.ChannelMessage
		if half > 0 {
			_ = s.DB.Where("channel_id = ? AND created_at > ?", chID, pivot.CreatedAt).
				Order("created_at desc").Limit(half).Find(&newer).Error
		}

		// Older than pivot, order DESC, limit remaining
		var older []models.ChannelMessage
		rem := limit - len(newer)
		if rem > 0 {
			_ = s.DB.Where("channel_id = ? AND created_at < ?", chID, pivot.CreatedAt).
				Order("created_at desc").Limit(rem).Find(&older).Error
		}

		// Combine: newer (DESC), pivot, older (DESC)
		out := make([]models.ChannelMessage, 0, len(newer)+1+len(older))
		out = append(out, newer...)
		out = append(out, *pivot)
		out = append(out, older...)
		c.JSON(http.StatusOK, out)
		return
	}

	if before != "" {
		pivot, ok := getPivot(before)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Pivot message not found"})
			return
		}
		var messages []models.ChannelMessage
		if err := s.DB.Where("channel_id = ? AND created_at < ?", chID, pivot.CreatedAt).
			Order("created_at desc").Limit(limit).Find(&messages).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch messages"})
			return
		}
		c.JSON(http.StatusOK, messages)
		return
	}

	// after
	pivot, ok := getPivot(after)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Pivot message not found"})
		return
	}
	var asc []models.ChannelMessage
	if err := s.DB.Where("channel_id = ? AND created_at > ?", chID, pivot.CreatedAt).
		Order("created_at asc").Limit(limit).Find(&asc).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch messages"})
		return
	}
	// Reverse to return DESC to keep consistency
	for i, j := 0, len(asc)-1; i < j; i, j = i+1, j-1 {
		asc[i], asc[j] = asc[j], asc[i]
	}
	c.JSON(http.StatusOK, asc)
}

// HandleListChannels lists all channels the user can view
func (s *YapYap) HandleListChannels(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// List channels the user can view by filtering
	var channels []models.Channel
	if err := s.DB.Find(&channels).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch channels"})
		return
	}

	visible := make([]models.Channel, 0, len(channels))
	for _, ch := range channels {
		if s.HasChannelFlag(userID.(uint64), ch.ID, models.PERM_VIEW_CHANNEL) || s.HasGlobalFlag(userID.(uint64), models.PERM_ADMINISTRATOR) {
			visible = append(visible, ch)
		}
	}

	c.JSON(http.StatusOK, visible)
}

// HandleGetChannel gets a single channel by ID
func (s *YapYap) HandleGetChannel(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Channel ID required"})
		return
	}

	// Check view permissions
	if !s.HasChannelFlag(userID.(uint64), parseUint64(channelID), models.PERM_VIEW_CHANNEL) && !s.HasGlobalFlag(userID.(uint64), models.PERM_ADMINISTRATOR) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var channel models.Channel
	if err := s.DB.First(&channel, channelID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
		return
	}

	c.JSON(http.StatusOK, channel)
}

// HandleUpdateMessage updates a message
func (s *YapYap) HandleUpdateMessage(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	messageID := c.Param("id")
	if messageID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Message ID required"})
		return
	}

	var updateData struct {
		Content     *string   `json:"content,omitempty"`
		Attachments *[]string `json:"attachments,omitempty"`
	}

	if err := c.ShouldBindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var message models.ChannelMessage
	if err := s.DB.First(&message, messageID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Message not found"})
		return
	}

	// Check if user owns the message or has manage permissions (unified)
	if message.UserID != userID.(uint64) {
		if !s.HasPermission(userID.(uint64), "ManageMessages") && !s.HasPermission(userID.(uint64), "Admin") {
			c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
			return
		}
	}

	if updateData.Content != nil {
		message.Content = *updateData.Content
	}
	if updateData.Attachments != nil {
		message.Attachments = *updateData.Attachments
	}
	message.UpdatedAt = time.Now()

	if err := s.DB.Save(&message).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update message"})
		return
	}

	// Broadcast update to WebSocket clients
	broadcast := ws.WebSocketEvent{
		Type: ws.EventTypeMessageUpdated,
		Data: message,
	}
	s.BroadcastToChannel(message.ChannelID, broadcast)

	c.JSON(http.StatusOK, message)
}

// HandleDeleteMessage deletes a message
func (s *YapYap) HandleDeleteMessage(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	messageID := c.Param("id")
	if messageID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Message ID required"})
		return
	}

	var message models.ChannelMessage
	if err := s.DB.First(&message, messageID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Message not found"})
		return
	}

	// Check if user owns the message or has manage permissions (unified)
	if message.UserID != userID.(uint64) {
		if !s.HasPermission(userID.(uint64), "ManageMessages") && !s.HasPermission(userID.(uint64), "Admin") {
			c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
			return
		}
	}

	if err := s.DB.Delete(&message).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete message"})
		return
	}

	// Broadcast deletion to WebSocket clients
	broadcast := ws.WebSocketEvent{
		Type: ws.EventTypeMessageDeleted,
		Data: map[string]interface{}{
			"id":         message.ID,
			"channel_id": message.ChannelID,
		},
	}
	s.BroadcastToChannel(message.ChannelID, broadcast)

	c.JSON(http.StatusOK, gin.H{"message": "Message deleted"})
}

// HandleListUsers lists all users (admin only)
func (s *YapYap) HandleListUsers(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check if user has admin or manage users permissions (unified)
	if !(s.HasPermission(userID.(uint64), "Admin") || s.HasPermission(userID.(uint64), "ManageUsers")) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed := parseLimit(l); parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	var users []models.User
	err := s.DB.Select("id, username, status, last_active, avatar_url, bio, created_at, updated_at").Limit(limit).Find(&users).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch users"})
		return
	}

	c.JSON(http.StatusOK, users)
}

// HandleGetUser gets a single user by ID
func (s *YapYap) HandleGetUser(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User ID required"})
		return
	}

	var user models.User
	err := s.DB.Select("id, username, status, last_active, avatar_url, bio, created_at, updated_at").First(&user, userID).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, user)
}

// HandleUpdateChannelPermissions updates permissions for a user in a channel
func (s *YapYap) HandleUpdateChannelPermissions(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Channel ID required"})
		return
	}

	// Check if user has admin or manage channels permission (unified)
	if !(s.HasPermission(userID.(uint64), "Admin") || s.HasPermission(userID.(uint64), "ManageChannels")) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var permData struct {
		UserID         uint64 `json:"user_id" binding:"required"`
		ViewChannel    *bool  `json:"view_channel,omitempty"`
		SendMessage    *bool  `json:"send_message,omitempty"`
		SendAttachment *bool  `json:"send_attachment,omitempty"`
		ManageMessages *bool  `json:"manage_messages,omitempty"`
		ManageChannel  *bool  `json:"manage_channel,omitempty"`
	}

	if err := c.ShouldBindJSON(&permData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var perm models.ChannelPermissions
	err := s.DB.Where("user_id = ? AND channel_id = ?", permData.UserID, channelID).First(&perm).Error
	if err != nil {
		// Create new permission if not found
		perm = models.ChannelPermissions{
			UserID:    permData.UserID,
			ChannelID: parseUint64(channelID),
		}
	}

	if permData.ViewChannel != nil {
		perm.ViewChannel = *permData.ViewChannel
	}
	if permData.SendMessage != nil {
		perm.SendMessage = *permData.SendMessage
	}
	if permData.SendAttachment != nil {
		perm.SendAttachment = *permData.SendAttachment
	}
	if permData.ManageMessages != nil {
		perm.ManageMessages = *permData.ManageMessages
	}
	if permData.ManageChannel != nil {
		perm.ManageChannel = *permData.ManageChannel
	}

	if err := s.DB.Save(&perm).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update permissions"})
		return
	}

	// Sync permissions for the affected user if they're connected
	s.mu.RLock()
	for _, client := range s.clients {
		if client.UserID == permData.UserID {
			s.SyncPermissions(client)
			break
		}
	}
	s.mu.RUnlock()

	c.JSON(http.StatusOK, perm)
}

// Role Management Handlers

// HandleListRoles lists all roles (admin only)
func (s *YapYap) HandleListRoles(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check if user has admin permissions (unified)
	if !s.HasPermission(userID.(uint64), "Admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var roles []models.Role
	err := s.DB.Find(&roles).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch roles"})
		return
	}

	c.JSON(http.StatusOK, roles)
}

// HandleCreateRole creates a new role
func (s *YapYap) HandleCreateRole(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check if user has admin permissions (unified)
	if !s.HasPermission(userID.(uint64), "Admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var roleData struct {
		Name        string                 `json:"name" binding:"required"`
		Permissions models.RolePermissions `json:"permissions"`
	}

	if err := c.ShouldBindJSON(&roleData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	role := models.Role{
		Name:        roleData.Name,
		Permissions: roleData.Permissions,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.DB.Create(&role).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create role"})
		return
	}

	c.JSON(http.StatusCreated, role)
}

// HandleUpdateRole updates an existing role
func (s *YapYap) HandleUpdateRole(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	roleID := c.Param("id")
	if roleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Role ID required"})
		return
	}

	// Check if user has admin permissions (unified)
	if !s.HasPermission(userID.(uint64), "Admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var updateData struct {
		Name        *string                 `json:"name,omitempty"`
		Permissions *models.RolePermissions `json:"permissions,omitempty"`
	}

	if err := c.ShouldBindJSON(&updateData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var role models.Role
	if err := s.DB.First(&role, roleID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Role not found"})
		return
	}

	if updateData.Name != nil {
		role.Name = *updateData.Name
	}
	if updateData.Permissions != nil {
		role.Permissions = *updateData.Permissions
	}
	role.UpdatedAt = time.Now()

	if err := s.DB.Save(&role).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update role"})
		return
	}

	c.JSON(http.StatusOK, role)
}

// HandleDeleteRole deletes a role
func (s *YapYap) HandleDeleteRole(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	roleID := c.Param("id")
	if roleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Role ID required"})
		return
	}

	// Check if user has admin permissions (unified)
	if !s.HasPermission(userID.(uint64), "Admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var role models.Role
	if err := s.DB.First(&role, roleID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Role not found"})
		return
	}

	// Delete all user role assignments first
	if err := s.DB.Where("role_id = ?", roleID).Delete(&models.UserRole{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove role assignments"})
		return
	}

	if err := s.DB.Delete(&role).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete role"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Role deleted"})
}

// HandleAssignRole assigns a role to a user
func (s *YapYap) HandleAssignRole(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check if user has admin permissions (unified)
	if !s.HasPermission(userID.(uint64), "Admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var assignData struct {
		UserID uint64 `json:"user_id" binding:"required"`
		RoleID uint64 `json:"role_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&assignData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if assignment already exists
	var existing models.UserRole
	err := s.DB.Where("user_id = ? AND role_id = ?", assignData.UserID, assignData.RoleID).First(&existing).Error
	if err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Role already assigned to user"})
		return
	}

	userRole := models.UserRole{
		UserID: assignData.UserID,
		RoleID: assignData.RoleID,
	}

	if err := s.DB.Create(&userRole).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign role"})
		return
	}

	c.JSON(http.StatusCreated, userRole)
}

// HandleRemoveRole removes a role from a user
func (s *YapYap) HandleRemoveRole(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check if user has admin permissions (unified)
	if !s.HasPermission(userID.(uint64), "Admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var removeData struct {
		UserID uint64 `json:"user_id" binding:"required"`
		RoleID uint64 `json:"role_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&removeData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result := s.DB.Where("user_id = ? AND role_id = ?", removeData.UserID, removeData.RoleID).Delete(&models.UserRole{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove role"})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Role assignment not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Role removed from user"})
}

// HandleGetUserRoles gets roles assigned to a user
func (s *YapYap) HandleGetUserRoles(c *gin.Context) {
	targetUserID := c.Param("id")
	if targetUserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User ID required"})
		return
	}

	var userRoles []models.UserRole
	err := s.DB.Preload("Role").Where("user_id = ?", targetUserID).Find(&userRoles).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user roles"})
		return
	}

	// Extract just the roles
	roles := make([]models.Role, len(userRoles))
	for i, ur := range userRoles {
		roles[i] = ur.Role
	}

	c.JSON(http.StatusOK, roles)
}

// HandleGetRole gets a single role by ID
func (s *YapYap) HandleGetRole(c *gin.Context) {
	roleID := c.Param("id")
	if roleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Role ID required"})
		return
	}

	var role models.Role
	if err := s.DB.First(&role, roleID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Role not found"})
		return
	}

	c.JSON(http.StatusOK, role)
}

// HandleGetRoleUsers gets all users assigned to a role
func (s *YapYap) HandleGetRoleUsers(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	roleID := c.Param("id")
	if roleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Role ID required"})
		return
	}

	// Check if user has admin permissions (unified)
	if !s.HasPermission(userID.(uint64), "Admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var userRoles []models.UserRole
	err := s.DB.Where("role_id = ?", roleID).Find(&userRoles).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch role users"})
		return
	}

	// Get user IDs and fetch user details
	userIDs := make([]uint64, len(userRoles))
	for i, ur := range userRoles {
		userIDs[i] = ur.UserID
	}

	var users []models.User
	if len(userIDs) > 0 {
		err = s.DB.Select("id, username, status, last_active, avatar_url, bio, created_at, updated_at").Where("id IN ?", userIDs).Find(&users).Error
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch users"})
			return
		}
	}

	c.JSON(http.StatusOK, users)
}

// HandleGetLogs retrieves logs with filtering
func (s *YapYap) HandleGetLogs(c *gin.Context) {
	// Check if user has admin permissions
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	if !s.HasPermission(userID.(uint64), "Admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	// Parse query parameters for filtering
	filter := models.LogFilter{}

	// Parse level filter
	if levelStr := c.Query("level"); levelStr != "" {
		levelInt := parseInt(levelStr)
		if levelInt >= 0 && levelInt <= 4 {
			level := models.LogLevel(levelInt)
			filter.Level = &level
		}
	}

	// Parse action filter
	if actionStr := c.Query("action"); actionStr != "" {
		action := models.LogAction(actionStr)
		filter.Action = &action
	}

	// Parse user_id filter
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		userIDValue := parseUint64(userIDStr)
		if userIDValue > 0 {
			filter.UserID = &userIDValue
		}
	}

	// Parse target_id filter
	if targetIDStr := c.Query("target_id"); targetIDStr != "" {
		targetIDValue := parseUint64(targetIDStr)
		if targetIDValue > 0 {
			filter.TargetID = &targetIDValue
		}
	}

	// Parse target_type filter
	if targetType := c.Query("target_type"); targetType != "" {
		filter.TargetType = &targetType
	}

	// Parse ip_address filter
	if ipAddress := c.Query("ip_address"); ipAddress != "" {
		filter.IPAddress = &ipAddress
	}

	// Parse date filters
	if startDateStr := c.Query("start_date"); startDateStr != "" {
		if startDate, err := time.Parse(time.RFC3339, startDateStr); err == nil {
			filter.StartDate = &startDate
		}
	}

	if endDateStr := c.Query("end_date"); endDateStr != "" {
		if endDate, err := time.Parse(time.RFC3339, endDateStr); err == nil {
			filter.EndDate = &endDate
		}
	}

	// Parse pagination
	filter.Limit = parseLimit(c.DefaultQuery("limit", "100"))
	filter.Offset = parseLimit(c.DefaultQuery("offset", "0"))

	// Get logs
	logs, totalCount, err := s.Logger.GetLogs(filter)
	if err != nil {
		s.Logger.Error(models.LogActionSystemError, fmt.Sprintf("Failed to retrieve logs: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve logs"})
		return
	}

	// Log the access
	s.Logger.LogWithUser(models.LogLevelInfo, models.LogAction("admin.logs.view"), "Admin viewed logs", userID.(uint64), c)

	c.JSON(http.StatusOK, gin.H{
		"logs":        logs,
		"total_count": totalCount,
		"limit":       filter.Limit,
		"offset":      filter.Offset,
	})
}

// HandleGetLogStats retrieves log statistics
func (s *YapYap) HandleGetLogStats(c *gin.Context) {
	// Check if user has admin permissions
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	if !s.HasPermission(userID.(uint64), "Admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	// Get log statistics
	stats, err := s.Logger.GetLogStats()
	if err != nil {
		s.Logger.Error(models.LogActionSystemError, fmt.Sprintf("Failed to retrieve log stats: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve log statistics"})
		return
	}

	// Log the access
	s.Logger.LogWithUser(models.LogLevelInfo, models.LogAction("admin.logs.stats"), "Admin viewed log statistics", userID.(uint64), c)

	c.JSON(http.StatusOK, stats)
}

// Helper function to parse integer from string
func parseInt(s string) int {
	if len(s) == 0 {
		return -1
	}
	result := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		result = result*10 + int(r-'0')
	}
	return result
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

		// Admin-only routes
		admin := api.Group("/admin", authHandlers.AuthMiddleware(s.JWTSecret), authHandlers.RequireAdminMiddleware(s.DB))
		{
			admin.POST("/users", authHandlers.CreateAdminHandler(s.DB, s.JWTSecret)) // Create admin user
		}

		// Channel management (protected routes)
		channels := api.Group("/channels", authHandlers.AuthMiddleware(s.JWTSecret))
		{
			channels.GET("", s.HandleListChannels)                             // List channels
			channels.POST("", s.HandleCreateChannel)                           // Create channel
			channels.GET("/:id", s.HandleGetChannel)                           // Get single channel
			channels.PUT("/:id", s.HandleUpdateChannel)                        // Update channel
			channels.DELETE("/:id", s.HandleDeleteChannel)                     // Delete channel
			channels.GET("/:id/messages", s.HandleGetChannelMessages)          // Get channel messages
			channels.PUT("/:id/permissions", s.HandleUpdateChannelPermissions) // Update channel permissions
			// Channel overwrites management (Admin or ManageChannels)
			channels.GET("/:id/overwrites", s.HandleListChannelOverwrites)
			channels.PUT("/:id/overwrites", s.HandleUpsertChannelOverwrite)
			channels.DELETE("/:id/overwrites", s.HandleDeleteChannelOverwrite)
		}

		// Message management (protected routes)
		messages := api.Group("/messages", authHandlers.AuthMiddleware(s.JWTSecret))
		{
			messages.POST("", s.HandleCreateMessage)       // Create message
			messages.PUT("/:id", s.HandleUpdateMessage)    // Update message
			messages.DELETE("/:id", s.HandleDeleteMessage) // Delete message
		}

		// User management (protected routes)
		users := api.Group("/users", authHandlers.AuthMiddleware(s.JWTSecret))
		{
			users.GET("", s.HandleListUsers)   // List users (admin only)
			users.GET("/:id", s.HandleGetUser) // Get user by ID
		}

		// Permission management (protected routes)
		permissions := api.Group("/permissions", authHandlers.AuthMiddleware(s.JWTSecret))
		{
			permissions.PUT("/channels/:id", s.HandleUpdateChannelPermissions) // Update channel permissions
		}

		// Role management (protected routes - admin only)
		roles := api.Group("/roles", authHandlers.AuthMiddleware(s.JWTSecret))
		{
			roles.GET("", s.HandleListRoles)              // List all roles
			roles.POST("", s.HandleCreateRole)            // Create role
			roles.GET("/:id", s.HandleGetRole)            // Get role by ID
			roles.PUT("/:id", s.HandleUpdateRole)         // Update role
			roles.DELETE("/:id", s.HandleDeleteRole)      // Delete role
			roles.GET("/:id/users", s.HandleGetRoleUsers) // Get users by role ID
			roles.POST("/assign", s.HandleAssignRole)     // Assign role to user
			roles.POST("/remove", s.HandleRemoveRole)     // Remove role from user
		}

		// Add user roles endpoint
		users.GET("/:id/roles", s.HandleGetUserRoles) // Get roles for a user

		// Log viewing endpoints (admin only)
		logs := api.Group("/logs", authHandlers.AuthMiddleware(s.JWTSecret))
		{
			logs.GET("", s.HandleGetLogs)           // Get logs with filtering
			logs.GET("/stats", s.HandleGetLogStats) // Get log statistics
		}
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
	// Log system shutdown
	s.Logger.LogSystemEvent(models.LogLevelInfo, models.LogActionSystemShutdown,
		"YapYap server shutting down gracefully",
		models.LogMetadata{
			"connected_clients": len(s.clients),
		})

	// Close all WebSocket connections
	s.mu.Lock()
	defer s.mu.Unlock()
	for conn, client := range s.clients {
		log.Printf("Closing WebSocket connection for user %d", client.UserID)
		// Send shutdown event before closing
		s.sendToClient(client, ws.WebSocketEvent{
			Type: ws.EventTypeServerStatus,
			Data: map[string]string{
				"message":               "Server is shutting down. Please reconnect later.",
				"protocol_version":      ProtocolVersion,
				"min_supported_version": MinimumSupportedVersion,
			},
		})
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

	// Log system startup
	s.Logger.LogSystemEvent(models.LogLevelInfo, models.LogActionSystemStartup,
		fmt.Sprintf("YapYap server starting on %s:%d", s.Host, s.Port),
		models.LogMetadata{
			"host":          s.Host,
			"port":          s.Port,
			"instance_name": s.InstanceName,
		})

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
		// Log WebSocket disconnection
		s.Logger.LogWithUser(models.LogLevelInfo, models.LogAction("websocket.disconnect"),
			"User disconnected from WebSocket",
			client.UserID, nil)

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

// HandleListChannelOverwrites lists all overwrites for a channel (Admin or ManageChannels required)
func (s *YapYap) HandleListChannelOverwrites(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Channel ID required"})
		return
	}
	uid := userID.(uint64)
	if !(s.HasGlobalFlag(uid, models.PERM_ADMINISTRATOR) || s.HasGlobalFlag(uid, models.PERM_MANAGE_CHANNELS)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var overwrites []models.ChannelOverwrite
	if err := s.DB.Where("channel_id = ?", channelID).Find(&overwrites).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch overwrites"})
		return
	}
	c.JSON(http.StatusOK, overwrites)
}

type overwritePayload struct {
	TargetType models.ChannelOverwriteTarget `json:"target_type" binding:"required"`
	TargetID   uint64                        `json:"target_id" binding:"required"`
	Allow      uint64                        `json:"allow"`
	Deny       uint64                        `json:"deny"`
}

// HandleUpsertChannelOverwrite creates or updates a channel overwrite (Admin or ManageChannels required)
func (s *YapYap) HandleUpsertChannelOverwrite(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Channel ID required"})
		return
	}
	uid := userID.(uint64)
	if !(s.HasGlobalFlag(uid, models.PERM_ADMINISTRATOR) || s.HasGlobalFlag(uid, models.PERM_MANAGE_CHANNELS)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var payload overwritePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate target exists (role or user)
	switch payload.TargetType {
	case models.OverwriteTargetRole:
		var role models.Role
		if err := s.DB.First(&role, payload.TargetID).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Role not found"})
			return
		}
	case models.OverwriteTargetMember:
		var user models.User
		if err := s.DB.First(&user, payload.TargetID).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "User not found"})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid target_type"})
		return
	}

	var ow models.ChannelOverwrite
	q := s.DB.Where("channel_id = ? AND target_type = ? AND target_id = ?", channelID, payload.TargetType, payload.TargetID)
	if err := q.First(&ow).Error; err != nil {
		// create
		ow = models.ChannelOverwrite{
			ChannelID:  parseUint64(channelID),
			TargetType: payload.TargetType,
			TargetID:   payload.TargetID,
			Allow:      payload.Allow,
			Deny:       payload.Deny,
		}
		if err := s.DB.Create(&ow).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create overwrite"})
			return
		}
		c.JSON(http.StatusCreated, ow)
		return
	}

	// update
	ow.Allow = payload.Allow
	ow.Deny = payload.Deny
	if err := s.DB.Save(&ow).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update overwrite"})
		return
	}
	c.JSON(http.StatusOK, ow)
}

// HandleDeleteChannelOverwrite deletes a specific overwrite (Admin or ManageChannels required)
func (s *YapYap) HandleDeleteChannelOverwrite(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	channelID := c.Param("id")
	if channelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Channel ID required"})
		return
	}
	uid := userID.(uint64)
	if !(s.HasGlobalFlag(uid, models.PERM_ADMINISTRATOR) || s.HasGlobalFlag(uid, models.PERM_MANAGE_CHANNELS)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var payload overwritePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result := s.DB.Where("channel_id = ? AND target_type = ? AND target_id = ?", channelID, payload.TargetType, payload.TargetID).Delete(&models.ChannelOverwrite{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete overwrite"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Overwrite not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Overwrite deleted"})
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
		case "ViewAnalytics":
			if up.ViewAnalytics {
				return PermissionAllow
			}
		case "SendMessages":
			if up.SendMessages {
				return PermissionAllow
			}
		case "SendAttachments":
			if up.SendAttachments {
				return PermissionAllow
			}
		case "JoinVoiceChannels":
			if up.JoinVoiceChannels {
				return PermissionAllow
			}
		case "ManageMessages":
			if up.ManageMessages {
				return PermissionAllow
			}
		case "ManageUsers":
			if up.ManageUsers {
				return PermissionAllow
			}
		case "ManageChannels":
			if up.ManageChannels {
				return PermissionAllow
			}
		case "ManageInstance":
			if up.ManageInstance {
				return PermissionAllow
			}
		case "Admin":
			if up.Admin {
				return PermissionAllow
			}
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

// Note: Permission checks should use HasPermission(userID, key) to avoid drift

// Helper function to parse uint64 from string
func parseUint64(s string) uint64 {
	result := uint64(0)
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		result = result*10 + uint64(r-'0')
	}
	return result
}

// Helper function to parse limit parameter
func parseLimit(s string) int {
	if len(s) == 0 {
		return 0
	}
	result := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		result = result*10 + int(r-'0')
	}
	return result
}
