package model

import (
	"time"

	"github.com/google/uuid"
)

type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

func (r *RefreshToken) IsActive(now time.Time) bool {
	return r.RevokedAt == nil && now.Before(r.ExpiresAt)
}
