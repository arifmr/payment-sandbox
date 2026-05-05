package model

import (
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
)

type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Name         string
	Role         constant.Role
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
