package yapyap

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"yapyap/ws"

	authHandlers "yapyap/handlers"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

type YapYap struct {
	InstanceName string `json:"instance_name"` // The name of the YapYap instance

	// Network settings
	Host string `json:"host"` // The host of the YapYap instance
	Port int    `json:"port"` // The port of the YapYap instance

	// Database settings
	PostgresURL string `json:"postgres_url"` // The URL to connect to the Postgres database
	RedisURL    string `json:"redis_url"`    // The URL to connect to the Redis

	// JWT settings
	JWTSecret []byte `json:"-"` // JWT secret for authentication (excluded from JSON)

	// Server components
	Router   *mux.Router
	upgrader websocket.Upgrader
	clients  map[*websocket.Conn]*Client //  Map of connected clients ( Every single client is a WebSocket connection )
	mu       sync.RWMutex
}

type Client struct {
	Conn   *websocket.Conn
	UserID uint64
	Send   chan []byte
}

func NewYapYap(instanceName, host string, port int, jwtSecret []byte) *YapYap {
	return &YapYap{
		InstanceName: instanceName,
		Host:         host,
		Port:         port,
		JWTSecret:    jwtSecret,
		Router:       mux.NewRouter(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// Configure CORS as needed
				return true
			},
		},
		clients: make(map[*websocket.Conn]*Client),
	}
}

func (s *YapYap) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
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
		Conn:   conn,
		UserID: claims.UserID,
		Send:   make(chan []byte, 256),
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
		log.Printf("User %d sent message: %v", client.UserID, event.Data)
		// TODO: Validate message, save to database, and broadcast to appropriate users
		// For now, just echo back
		s.BroadcastToAll(event)

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

func (s *YapYap) SetupRoutes() {
	// Serve static files (for testing)
	s.Router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./assets/"))))

	// WebSocket endpoint (requires authentication)
	s.Router.HandleFunc("/ws", s.HandleWebSocket).Methods("GET")

	// API routes
	api := s.Router.PathPrefix("/api/v1").Subrouter()

	// Public auth routes (no authentication required)
	api.HandleFunc("/auth/login", authHandlers.LoginHandler(s.JWTSecret)).Methods("POST")
	api.HandleFunc("/auth/register", authHandlers.RegisterHandler(s.JWTSecret)).Methods("POST")

	// Protected routes (authentication required)
	api.HandleFunc("/users/me", authHandlers.AuthMiddleware(authHandlers.HandleGetCurrentUser, s.JWTSecret)).Methods("GET")

	// Health check
	s.Router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "ok",
			"instance": s.InstanceName,
		})
	}).Methods("GET")

	// Log the routes for debugging
	s.Router.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err != nil {
			return err
		}
		methods, _ := route.GetMethods()
		if len(methods) == 0 {
			return nil
		}
		log.Printf("Registered route: %s %s", methods, path)
		return nil
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

	if err := http.ListenAndServe(addr, s.Router); err != nil {
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
