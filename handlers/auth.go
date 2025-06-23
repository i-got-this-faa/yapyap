package yapyap

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	yapyapModels "yapyap/models"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// JWT secret key - in production, this should come from environment variables

type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthResponse struct {
	UserID uint64            `json:"user_id"`
	Token  string            `json:"token"` // JWT token
	User   yapyapModels.User `json:"user"`
}

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Bio      string `json:"bio,omitempty"`
}

type Claims struct {
	UserID   uint64 `json:"user_id"`
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
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// GenerateJWT creates a new JWT token for a user
func GenerateJWT(userID uint64, username string, jwtSecret []byte) (string, error) {
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
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jwtSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
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
func AuthMiddleware(next http.HandlerFunc, jwtSecret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenString := ExtractTokenFromHeader(r)
		if tokenString == "" {
			http.Error(w, "Authorization token required", http.StatusUnauthorized)
			return
		}

		claims, err := ValidateJWT(tokenString, jwtSecret)
		if err != nil {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		// Add user info to request context for use in handlers
		r.Header.Set("X-User-ID", fmt.Sprintf("%d", claims.UserID))
		r.Header.Set("X-Username", claims.Username)

		next.ServeHTTP(w, r)
	}
}

// LoginHandler returns a login handler with the JWT secret
func LoginHandler(jwtSecret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req AuthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// TODO: Get user from database
		// For now, we'll use a mock user
		mockUser := yapyapModels.User{
			ID:         1,
			Username:   req.Username,
			CreatedAt:  time.Now(),
			Status:     yapyapModels.StatusActive,
			LastActive: time.Now(),
			Bio:        "Test user",
		}

		// TODO: Validate password against database hash
		// For now, we'll accept any password for demo purposes
		if req.Username == "" || req.Password == "" {
			http.Error(w, "Username and password required", http.StatusBadRequest)
			return
		}

		// Generate JWT token
		token, err := GenerateJWT(mockUser.ID, mockUser.Username, jwtSecret)
		if err != nil {
			http.Error(w, "Failed to generate token", http.StatusInternalServerError)
			return
		}

		// TODO: Save login token to database
		loginToken := yapyapModels.UserLoginToken{
			UserID:    mockUser.ID,
			Token:     token,
			CreatedAt: time.Now(),
			LastUsed:  time.Now(),
		}
		_ = loginToken // Use this when you have database

		response := AuthResponse{
			UserID: mockUser.ID,
			Token:  token,
			User:   mockUser,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// RegisterHandler returns a register handler with the JWT secret
func RegisterHandler(jwtSecret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if req.Username == "" || req.Password == "" {
			http.Error(w, "Username and password required", http.StatusBadRequest)
			return
		}

		// Hash password
		hashedPassword, err := HashPassword(req.Password)
		if err != nil {
			http.Error(w, "Failed to hash password", http.StatusInternalServerError)
			return
		}

		// TODO: Save user to database
		// For now, we'll create a mock user
		newUser := yapyapModels.User{
			ID:         2, // TODO: Get from database auto-increment
			Username:   req.Username,
			CreatedAt:  time.Now(),
			Status:     yapyapModels.StatusActive,
			LastActive: time.Now(),
			Bio:        req.Bio,
		}

		_ = hashedPassword // Use this when saving to database

		// Generate JWT token
		token, err := GenerateJWT(newUser.ID, newUser.Username, jwtSecret)
		if err != nil {
			http.Error(w, "Failed to generate token", http.StatusInternalServerError)
			return
		}

		response := AuthResponse{
			UserID: newUser.ID,
			Token:  token,
			User:   newUser,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(response)
	}
}

// Backward compatibility handlers (deprecated - use factory functions above)

// HandleLogin - deprecated, use LoginHandler instead
func HandleLogin(w http.ResponseWriter, r *http.Request) {
	// This is kept for backward compatibility but should not be used
	// Use LoginHandler(jwtSecret) instead
	http.Error(w, "Internal configuration error: JWT secret not provided", http.StatusInternalServerError)
}

// HandleRegister - deprecated, use RegisterHandler instead
func HandleRegister(w http.ResponseWriter, r *http.Request) {
	// This is kept for backward compatibility but should not be used
	// Use RegisterHandler(jwtSecret) instead
	http.Error(w, "Internal configuration error: JWT secret not provided", http.StatusInternalServerError)
}

// HandleGetCurrentUser handler - protected endpoint
func HandleGetCurrentUser(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Username")

	// TODO: Get user from database using userID from X-User-ID header
	// For now, return a mock user
	user := yapyapModels.User{
		ID:         1,
		Username:   username,
		CreatedAt:  time.Now().Add(-24 * time.Hour), // Created yesterday
		Status:     yapyapModels.StatusActive,
		LastActive: time.Now(),
		Bio:        "Current authenticated user",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}
