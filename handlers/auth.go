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
