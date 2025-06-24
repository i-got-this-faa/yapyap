package yapyap

type UserStatus uint

const (
	StatusOffline UserStatus = 0
	StatusActive  UserStatus = 1
	StatusIdle    UserStatus = 2
)
