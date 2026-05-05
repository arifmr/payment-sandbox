package model

import "time"

type RegisterRequest struct {
	Name     string `json:"name" binding:"required,min=2,max=100" example:"Toko A"`
	Email    string `json:"email" binding:"required,email" example:"toko@example.com"`
	Password string `json:"password" binding:"required,min=8,max=72" example:"password123"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email" example:"toko@example.com"`
	Password string `json:"password" binding:"required" example:"password123"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type UserResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type LoginResponse struct {
	AccessToken      string       `json:"access_token"`
	AccessExpiresAt  time.Time    `json:"access_expires_at"`
	RefreshToken     string       `json:"refresh_token"`
	RefreshExpiresAt time.Time    `json:"refresh_expires_at"`
	User             UserResponse `json:"user"`
}
