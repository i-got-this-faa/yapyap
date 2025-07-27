package utils

import (
	"fmt"
	"log"
	"time"
	models "yapyap/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Logger wraps the database connection and provides logging methods
type Logger struct {
	db *gorm.DB
}

// NewLogger creates a new Logger instance
func NewLogger(db *gorm.DB) *Logger {
	return &Logger{db: db}
}

// LogEntry represents a log entry to be written
type LogEntry struct {
	Level      models.LogLevel
	Action     models.LogAction
	Message    string
	UserID     *uint64
	TargetID   *uint64
	TargetType string
	IPAddress  string
	UserAgent  string
	Metadata   models.LogMetadata
}

// WriteLog writes a log entry to the database
func (l *Logger) WriteLog(entry LogEntry) error {
	logRecord := models.Log{
		Level:      entry.Level,
		Action:     entry.Action,
		Message:    entry.Message,
		UserID:     entry.UserID,
		TargetID:   entry.TargetID,
		TargetType: entry.TargetType,
		IPAddress:  entry.IPAddress,
		UserAgent:  entry.UserAgent,
		Metadata:   entry.Metadata.ToJSON(),
		CreatedAt:  time.Now(),
	}

	result := l.db.Create(&logRecord)
	if result.Error != nil {
		// Log to standard output if database logging fails
		log.Printf("Failed to write log to database: %v", result.Error)
		return result.Error
	}

	return nil
}

// Debug logs a debug message
func (l *Logger) Debug(action models.LogAction, message string) error {
	return l.WriteLog(LogEntry{
		Level:   models.LogLevelDebug,
		Action:  action,
		Message: message,
	})
}

// Info logs an info message
func (l *Logger) Info(action models.LogAction, message string) error {
	return l.WriteLog(LogEntry{
		Level:   models.LogLevelInfo,
		Action:  action,
		Message: message,
	})
}

// Warn logs a warning message
func (l *Logger) Warn(action models.LogAction, message string) error {
	return l.WriteLog(LogEntry{
		Level:   models.LogLevelWarn,
		Action:  action,
		Message: message,
	})
}

// Error logs an error message
func (l *Logger) Error(action models.LogAction, message string) error {
	return l.WriteLog(LogEntry{
		Level:   models.LogLevelError,
		Action:  action,
		Message: message,
	})
}

// Fatal logs a fatal message
func (l *Logger) Fatal(action models.LogAction, message string) error {
	return l.WriteLog(LogEntry{
		Level:   models.LogLevelFatal,
		Action:  action,
		Message: message,
	})
}

// LogWithUser logs an action performed by a specific user
func (l *Logger) LogWithUser(level models.LogLevel, action models.LogAction, message string, userID uint64, c *gin.Context) error {
	entry := LogEntry{
		Level:   level,
		Action:  action,
		Message: message,
		UserID:  &userID,
	}

	if c != nil {
		entry.IPAddress = c.ClientIP()
		entry.UserAgent = c.GetHeader("User-Agent")
	}

	return l.WriteLog(entry)
}

// LogWithTarget logs an action on a specific target resource
func (l *Logger) LogWithTarget(level models.LogLevel, action models.LogAction, message string, userID *uint64, targetID uint64, targetType string, c *gin.Context) error {
	entry := LogEntry{
		Level:      level,
		Action:     action,
		Message:    message,
		UserID:     userID,
		TargetID:   &targetID,
		TargetType: targetType,
	}

	if c != nil {
		entry.IPAddress = c.ClientIP()
		entry.UserAgent = c.GetHeader("User-Agent")
	}

	return l.WriteLog(entry)
}

// LogWithMetadata logs an action with additional structured metadata
func (l *Logger) LogWithMetadata(level models.LogLevel, action models.LogAction, message string, userID *uint64, metadata models.LogMetadata, c *gin.Context) error {
	entry := LogEntry{
		Level:    level,
		Action:   action,
		Message:  message,
		UserID:   userID,
		Metadata: metadata,
	}

	if c != nil {
		entry.IPAddress = c.ClientIP()
		entry.UserAgent = c.GetHeader("User-Agent")
	}

	return l.WriteLog(entry)
}

// LogAuthAttempt logs authentication attempts
func (l *Logger) LogAuthAttempt(success bool, username string, c *gin.Context) error {
	var level models.LogLevel
	var action models.LogAction
	var message string

	if success {
		level = models.LogLevelInfo
		action = models.LogActionAuthSuccess
		message = fmt.Sprintf("Successful authentication for user: %s", username)
	} else {
		level = models.LogLevelWarn
		action = models.LogActionAuthFailure
		message = fmt.Sprintf("Failed authentication attempt for user: %s", username)
	}

	entry := LogEntry{
		Level:   level,
		Action:  action,
		Message: message,
		Metadata: models.LogMetadata{
			"username": username,
			"success":  success,
		},
	}

	if c != nil {
		entry.IPAddress = c.ClientIP()
		entry.UserAgent = c.GetHeader("User-Agent")
	}

	return l.WriteLog(entry)
}

// LogSystemEvent logs system-level events
func (l *Logger) LogSystemEvent(level models.LogLevel, action models.LogAction, message string, metadata models.LogMetadata) error {
	return l.WriteLog(LogEntry{
		Level:    level,
		Action:   action,
		Message:  message,
		Metadata: metadata,
	})
}

// GetLogs retrieves logs based on filter criteria
func (l *Logger) GetLogs(filter models.LogFilter) ([]models.Log, int64, error) {
	var logs []models.Log
	var totalCount int64

	query := l.db.Model(&models.Log{}).Preload("User")

	// Apply filters
	if filter.Level != nil {
		query = query.Where("level = ?", *filter.Level)
	}
	if filter.Action != nil {
		query = query.Where("action = ?", *filter.Action)
	}
	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if filter.TargetID != nil {
		query = query.Where("target_id = ?", *filter.TargetID)
	}
	if filter.TargetType != nil {
		query = query.Where("target_type = ?", *filter.TargetType)
	}
	if filter.IPAddress != nil {
		query = query.Where("ip_address = ?", *filter.IPAddress)
	}
	if filter.StartDate != nil {
		query = query.Where("created_at >= ?", *filter.StartDate)
	}
	if filter.EndDate != nil {
		query = query.Where("created_at <= ?", *filter.EndDate)
	}

	// Get total count
	if err := query.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	// Apply pagination and ordering
	if filter.Limit <= 0 {
		filter.Limit = 100 // Default limit
	}
	if filter.Limit > 1000 {
		filter.Limit = 1000 // Max limit
	}

	query = query.Order("created_at DESC").
		Limit(filter.Limit).
		Offset(filter.Offset)

	if err := query.Find(&logs).Error; err != nil {
		return nil, 0, err
	}

	return logs, totalCount, nil
}

// GetLogStats retrieves statistics about logs
func (l *Logger) GetLogStats() (*models.LogStats, error) {
	var stats models.LogStats

	// Get total count
	if err := l.db.Model(&models.Log{}).Count(&stats.TotalLogs).Error; err != nil {
		return nil, err
	}

	// Get counts by level
	stats.LogsByLevel = make(map[string]int64)
	rows, err := l.db.Model(&models.Log{}).
		Select("level, COUNT(*) as count").
		Group("level").
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var level models.LogLevel
		var count int64
		if err := rows.Scan(&level, &count); err != nil {
			continue
		}
		stats.LogsByLevel[level.String()] = count
	}

	// Get counts by action
	stats.LogsByAction = make(map[string]int64)
	rows, err = l.db.Model(&models.Log{}).
		Select("action, COUNT(*) as count").
		Group("action").
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var action models.LogAction
		var count int64
		if err := rows.Scan(&action, &count); err != nil {
			continue
		}
		stats.LogsByAction[string(action)] = count
	}

	// Get recent actions (last 10)
	if err := l.db.Model(&models.Log{}).
		Preload("User").
		Order("created_at DESC").
		Limit(10).
		Find(&stats.RecentActions).Error; err != nil {
		return nil, err
	}

	return &stats, nil
}

// CleanupOldLogs removes logs older than the specified duration
func (l *Logger) CleanupOldLogs(olderThan time.Duration) (int64, error) {
	cutoffTime := time.Now().Add(-olderThan)

	result := l.db.Where("created_at < ?", cutoffTime).Delete(&models.Log{})
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}
