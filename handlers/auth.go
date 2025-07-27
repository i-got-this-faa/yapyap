package yapyap

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	yapyapModels "yapyap/models"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthResponse struct {
	UserID uint              `json:"user_id"`
	Token  string            `json:"token"`
	User   yapyapModels.User `json:"user"`
}

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Bio      string `json:"bio,omitempty"`
}

type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// HashPassword hashes a password using bcrypt
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPasswordHash compares a password with its hash
func CheckPasswordHash(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GenerateJWT creates a new JWT token for a user
func GenerateJWT(userID uint, username string, jwtSecret []byte) (string, error) {
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(yapyapModels.TokenExpirationDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// ValidateJWT validates a JWT token and returns the claims
func ValidateJWT(tokenString string, jwtSecret []byte) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrTokenMalformed
		}
		return jwtSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, jwt.ErrTokenInvalidClaims
}

// ExtractTokenFromHeader extracts JWT token from Authorization header
func ExtractTokenFromHeader(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}

	// Check for "Bearer " prefix
	if len(authHeader) > 7 && strings.ToLower(authHeader[0:7]) == "bearer " {
		return authHeader[7:]
	}

	return authHeader
}

// GenerateRandomToken generates a random token for user login tokens
func GenerateRandomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// AuthMiddleware is a middleware that validates JWT tokens
func AuthMiddleware(jwtSecret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := ExtractTokenFromHeader(c.Request)
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization token required"})
			c.Abort()
			return
		}

		claims, err := ValidateJWT(tokenString, jwtSecret)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)

		c.Next()
	}
}

// LoginHandler returns a login handler with the JWT secret and DB
func LoginHandler(db *gorm.DB, jwtSecret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req AuthRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
			return
		}
		if req.Username == "" || req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username and password required"})
			return
		}
		var user yapyapModels.User
		if err := db.Where("username = ?", req.Username).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
			return
		}
		if !CheckPasswordHash(req.Password, user.PasswordHash) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
			return
		}
		token, err := GenerateJWT(user.ID, user.Username, jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}
		response := AuthResponse{
			UserID: user.ID,
			Token:  token,
			User:   user,
		}
		c.JSON(http.StatusOK, response)
	}
}

// RegisterHandler returns a register handler with the JWT secret and DB
func RegisterHandler(db *gorm.DB, jwtSecret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RegisterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
			return
		}
		if req.Username == "" || req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username and password required"})
			return
		}
		var existing yapyapModels.User
		if err := db.Where("username = ?", req.Username).First(&existing).Error; err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "Username already exists"})
			return
		}
		hashedPassword, err := HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}
		newUser := yapyapModels.User{
			Username:     req.Username,
			PasswordHash: hashedPassword,
			Status:       yapyapModels.StatusActive,
			LastActive:   time.Now(),
			Bio:          req.Bio,
		}
		if err := db.Create(&newUser).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
			return
		}

		// Create basic user permissions (non-admin)
		permissions := yapyapModels.UserPermissions{
			UserID:            uint(newUser.ID),
			ViewAnalytics:     false,
			SendMessages:      true,
			SendAttachments:   true,
			JoinVoiceChannels: true,
			ManageMessages:    false,
			ManageUsers:       false,
			ManageChannels:    false,
			ManageInstance:    false,
			Admin:             false, // Regular users are not admins
		}

		if err := db.Create(&permissions).Error; err != nil {
			// If permissions creation fails, delete the user to maintain consistency
			db.Delete(&newUser)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user permissions"})
			return
		}

		token, err := GenerateJWT(newUser.ID, newUser.Username, jwtSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}
		response := AuthResponse{
			UserID: newUser.ID,
			Token:  token,
			User:   newUser,
		}
		c.JSON(http.StatusCreated, response)
	}
}

// HandleGetCurrentUser handler - protected endpoint
func HandleGetCurrentUser(c *gin.Context) {
	username, _ := c.Get("username")
	userID, _ := c.Get("user_id")
	user := yapyapModels.User{
		Model:      gorm.Model{ID: userID.(uint)},
		Username:   username.(string),
		Status:     yapyapModels.StatusActive,
		LastActive: time.Now(),
		Bio:        "Current authenticated user",
	}
	c.JSON(http.StatusOK, user)
}

// AdminCreateRequest represents an admin user creation request
type AdminCreateRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

// RequireAdminMiddleware returns a middleware that checks if user has admin role
func RequireAdminMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// First check if user is authenticated
		userID, exists := c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			c.Abort()
			return
		}

		// Check if user has admin role
		var adminRole yapyapModels.Role
		if err := db.Where("name = ?", "admin").First(&adminRole).Error; err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			c.Abort()
			return
		}

		var userRole yapyapModels.UserRole
		if err := db.Where("user_id = ? AND role_id = ?", userID, adminRole.ID).First(&userRole).Error; err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// CreateAdminHandler returns a handler for creating admin users - requires admin authentication
func CreateAdminHandler(db *gorm.DB, jwtSecret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req AdminCreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
			return
		}

		if req.Username == "" || req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username and password required"})
			return
		}

		// Check if username already exists
		var existing yapyapModels.User
		if err := db.Where("username = ?", req.Username).First(&existing).Error; err == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "Username already exists"})
			return
		}

		// Hash password
		hashedPassword, err := HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}

		// Create user
		newUser := yapyapModels.User{
			Username:     req.Username,
			PasswordHash: hashedPassword,
			Status:       yapyapModels.StatusActive,
			LastActive:   time.Now(),
		}

		if err := db.Create(&newUser).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
			return
		}

		// Create admin permissions
		permissions := yapyapModels.UserPermissions{
			UserID:            uint(newUser.ID),
			ViewAnalytics:     true,
			SendMessages:      true,
			SendAttachments:   true,
			JoinVoiceChannels: true,
			ManageMessages:    true,
			ManageUsers:       true,
			ManageChannels:    true,
			ManageInstance:    true,
			Admin:             true,
		}

		if err := db.Create(&permissions).Error; err != nil {
			// If permissions creation fails, delete the user to maintain consistency
			db.Delete(&newUser)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create admin permissions"})
			return
		}

		// Assign admin role
		var adminRole yapyapModels.Role
		if err := db.Where("name = ?", "admin").First(&adminRole).Error; err != nil {
			// If admin role doesn't exist, create it
			adminPermissions := yapyapModels.RolePermissions{
				"ViewAnalytics":     yapyapModels.PermissionAllow,
				"SendMessages":      yapyapModels.PermissionAllow,
				"SendAttachments":   yapyapModels.PermissionAllow,
				"JoinVoiceChannels": yapyapModels.PermissionAllow,
				"ManageMessages":    yapyapModels.PermissionAllow,
				"ManageUsers":       yapyapModels.PermissionAllow,
				"ManageChannels":    yapyapModels.PermissionAllow,
				"ManageInstance":    yapyapModels.PermissionAllow,
				"Admin":             yapyapModels.PermissionAllow,
			}

			adminRole = yapyapModels.Role{
				Name:        "admin",
				Permissions: adminPermissions,
			}

			if err := db.Create(&adminRole).Error; err != nil {
				db.Delete(&newUser)
				db.Delete(&permissions)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create admin role"})
				return
			}
		}

		// Assign admin role to user
		userRole := yapyapModels.UserRole{
			UserID: uint64(newUser.ID),
			RoleID: uint64(adminRole.ID),
		}

		if err := db.Create(&userRole).Error; err != nil {
			// If role assignment fails, delete the user and permissions to maintain consistency
			db.Delete(&newUser)
			db.Delete(&permissions)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign admin role"})
			return
		}

		// Don't return a token - admin creation is for other admins, not auto-login
		response := gin.H{
			"message":  "Admin user created successfully",
			"user_id":  newUser.ID,
			"username": newUser.Username,
		}

		c.JSON(http.StatusCreated, response)
	}
}
