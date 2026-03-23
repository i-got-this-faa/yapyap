package yapyap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	authHandlers "yapyap/handlers"
	models "yapyap/models"
	"yapyap/utils"
	"yapyap/ws"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestYapYap(t *testing.T) *YapYap {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserPermissions{}, &models.UserLoginToken{}, &models.Channel{}, &models.ChannelMessage{}, &models.Role{}, &models.UserRole{}, &models.Log{}, &models.ChannelOverwrite{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	server := &YapYap{
		InstanceName:               "test",
		Host:                       "127.0.0.1",
		Port:                       0,
		PermissionCacheSyncSeconds: 1,
		JWTSecret:                  []byte("test-secret"),
		Engine:                     gin.New(),
		DB:                         db,
		Logger:                     utils.NewLogger(db),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		clients: make(map[*websocket.Conn]*Client),
	}
	server.Engine.Use(gin.Recovery())
	server.SetupRoutes()
	return server
}

func createTestUser(t *testing.T, db *gorm.DB, username string, isAdmin bool) models.User {
	t.Helper()
	hash, err := authHandlers.HashPassword("password123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := models.User{
		Username:     username,
		PasswordHash: hash,
		Status:       models.StatusActive,
		LastActive:   time.Now(),
		Bio:          username + " bio",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	perms := models.UserPermissions{
		UserID:            uint(user.ID),
		ViewAnalytics:     isAdmin,
		SendMessages:      true,
		SendAttachments:   true,
		JoinVoiceChannels: true,
		ManageMessages:    isAdmin,
		ManageUsers:       isAdmin,
		ManageChannels:    isAdmin,
		ManageInstance:    isAdmin,
		Admin:             isAdmin,
	}
	if err := db.Create(&perms).Error; err != nil {
		t.Fatalf("create user permissions: %v", err)
	}
	return user
}

func authHeaderForUser(t *testing.T, server *YapYap, user models.User) string {
	t.Helper()
	token, err := authHandlers.GenerateJWT(user.ID, user.Username, server.JWTSecret)
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}
	return "Bearer " + token
}

func performRequest(server *YapYap, method, path, body, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	server.Engine.ServeHTTP(w, req)
	return w
}

func TestAdminRouteUsesPermissionEngine(t *testing.T) {
	server := newTestYapYap(t)
	adminUser := createTestUser(t, server.DB, "admin-user", true)
	regularUser := createTestUser(t, server.DB, "regular-user", false)

	adminResp := performRequest(server, http.MethodPost, "/api/v1/admin/users", `{"username":"created-admin","password":"secret123"}`, authHeaderForUser(t, server, adminUser))
	if adminResp.Code != http.StatusCreated {
		t.Fatalf("expected admin create success, got %d: %s", adminResp.Code, adminResp.Body.String())
	}

	regularResp := performRequest(server, http.MethodPost, "/api/v1/admin/users", `{"username":"blocked-admin","password":"secret123"}`, authHeaderForUser(t, server, regularUser))
	if regularResp.Code != http.StatusForbidden {
		t.Fatalf("expected regular user forbidden, got %d: %s", regularResp.Code, regularResp.Body.String())
	}
}

func TestAuthAndCurrentUserCompatibility(t *testing.T) {
	server := newTestYapYap(t)
	user := createTestUser(t, server.DB, "login-user", false)

	loginResp := performRequest(server, http.MethodPost, "/api/v1/auth/login", `{"username":"login-user","password":"password123"}`, "")
	if loginResp.Code != http.StatusOK {
		t.Fatalf("expected login success, got %d: %s", loginResp.Code, loginResp.Body.String())
	}
	var loginBody map[string]any
	if err := json.Unmarshal(loginResp.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	for _, key := range []string{"user_id", "token", "user"} {
		if _, ok := loginBody[key]; !ok {
			t.Fatalf("expected login response key %q in %v", key, loginBody)
		}
	}
	currentUserResp := performRequest(server, http.MethodGet, "/api/v1/users/me", "", authHeaderForUser(t, server, user))
	if currentUserResp.Code != http.StatusOK {
		t.Fatalf("expected current user success, got %d: %s", currentUserResp.Code, currentUserResp.Body.String())
	}
	var currentUser map[string]any
	if err := json.Unmarshal(currentUserResp.Body.Bytes(), &currentUser); err != nil {
		t.Fatalf("unmarshal current user response: %v", err)
	}
	for _, key := range []string{"id", "created_at", "updated_at", "username", "status", "last_active", "avatar_url", "bio"} {
		if _, ok := currentUser[key]; !ok {
			t.Fatalf("expected current user key %q in %v", key, currentUser)
		}
	}
}

func TestChannelCreateListAndLegacyPermissionsCompatibility(t *testing.T) {
	server := newTestYapYap(t)
	adminUser := createTestUser(t, server.DB, "channel-admin", true)
	targetUser := createTestUser(t, server.DB, "channel-member", false)
	adminAuth := authHeaderForUser(t, server, adminUser)

	createResp := performRequest(server, http.MethodPost, "/api/v1/channels", `{"name":"general","type":0}`, adminAuth)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected channel create success, got %d: %s", createResp.Code, createResp.Body.String())
	}

	listResp := performRequest(server, http.MethodGet, "/api/v1/channels", "", authHeaderForUser(t, server, targetUser))
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected channel list success, got %d: %s", listResp.Code, listResp.Body.String())
	}
	var channels []map[string]any
	if err := json.Unmarshal(listResp.Body.Bytes(), &channels); err != nil {
		t.Fatalf("unmarshal channels: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(channels))
	}
	for _, key := range []string{"id", "created_at", "updated_at", "name", "type", "messages"} {
		if _, ok := channels[0][key]; !ok {
			t.Fatalf("expected channel response key %q in %v", key, channels[0])
		}
	}

	permResp := performRequest(server, http.MethodPut, "/api/v1/channels/1/permissions", fmt.Sprintf(`{"user_id":%d,"view_channel":true,"send_message":true}`, targetUser.ID), adminAuth)
	if permResp.Code != http.StatusOK {
		t.Fatalf("expected permission update success, got %d: %s", permResp.Code, permResp.Body.String())
	}
	var permBody map[string]any
	if err := json.Unmarshal(permResp.Body.Bytes(), &permBody); err != nil {
		t.Fatalf("unmarshal permission response: %v", err)
	}
	for _, key := range []string{"id", "created_at", "updated_at", "channel_id", "user_id", "view_channel", "send_message", "send_attachment", "manage_messages", "manage_channel"} {
		if _, ok := permBody[key]; !ok {
			t.Fatalf("expected permission response key %q in %v", key, permBody)
		}
	}

	var overwrite models.ChannelOverwrite
	if err := server.DB.Where("channel_id = ? AND target_type = ? AND target_id = ?", 1, models.OverwriteTargetMember, targetUser.ID).First(&overwrite).Error; err != nil {
		t.Fatalf("expected overwrite row to exist: %v", err)
	}
	if overwrite.Allow&models.PERM_VIEW_CHANNEL == 0 || overwrite.Allow&models.PERM_SEND_MESSAGES == 0 {
		t.Fatalf("expected overwrite allow flags to include view/send, got allow=%d deny=%d", overwrite.Allow, overwrite.Deny)
	}
}

func TestBroadcastToChannelOnlySendsToAuthorizedClients(t *testing.T) {
	server := newTestYapYap(t)
	allowed := &Client{UserID: 1, Send: make(chan []byte, 1), PermCache: map[uint64]models.ChannelPermissions{9: {ChannelID: 9, UserID: 1, ViewChannel: true}}}
	blocked := &Client{UserID: 2, Send: make(chan []byte, 1), PermCache: map[uint64]models.ChannelPermissions{9: {ChannelID: 9, UserID: 2, ViewChannel: false}}}
	server.clients[&websocket.Conn{}] = allowed
	server.clients[&websocket.Conn{}] = blocked

	server.BroadcastToChannel(9, ws.WebSocketEvent{Type: ws.EventTypeMessageCreated, Data: map[string]any{"ok": true}})

	select {
	case <-allowed.Send:
	default:
		t.Fatal("expected authorized client to receive broadcast")
	}
	select {
	case msg := <-blocked.Send:
		t.Fatalf("expected blocked client to receive nothing, got %s", string(msg))
	default:
	}
}

func TestWebSocketWelcomeEventCompatibility(t *testing.T) {
	server := newTestYapYap(t)
	user := createTestUser(t, server.DB, "ws-user", false)
	httpServer := httptest.NewServer(server.Engine)
	defer httpServer.Close()

	parsedURL, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatalf("parse httptest url: %v", err)
	}
	token, err := authHandlers.GenerateJWT(user.ID, user.Username, server.JWTSecret)
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}
	wsURL := url.URL{Scheme: "ws", Host: parsedURL.Host, Path: "/ws", RawQuery: "token=" + token}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	_, message, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket welcome message: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(message, &event); err != nil {
		t.Fatalf("unmarshal websocket event: %v", err)
	}
	if got := int(event["type"].(float64)); got != int(ws.EventTypeServerInfo) {
		t.Fatalf("expected server info event, got %d", got)
	}
	data, ok := event["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected event data object, got %T", event["data"])
	}
	for _, key := range []string{"instance_name", "message", "user_id", "username"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("expected welcome data key %q in %v", key, data)
		}
	}
}
