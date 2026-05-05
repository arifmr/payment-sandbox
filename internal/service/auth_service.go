package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/pkg/hash"
	"github.com/dboarif/payment-sandbox/internal/pkg/jwt"
	"github.com/dboarif/payment-sandbox/internal/pkg/token"
	"github.com/dboarif/payment-sandbox/internal/repository"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

// TokenPair is the bundle returned at login and refresh.
type TokenPair struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string // opaque, plaintext — show to user once
	RefreshExpiresAt time.Time
	User             *model.User
}

type AuthService interface {
	Register(ctx context.Context, name, email, password string) (*model.User, error)
	Login(ctx context.Context, email, password string) (*TokenPair, error)
	Refresh(ctx context.Context, refreshToken string) (*TokenPair, error)
	Logout(ctx context.Context, refreshToken string) error
}

type authService struct {
	users      repository.UserRepository
	wallets    repository.WalletRepository
	refresh    repository.RefreshTokenRepository
	hasher     *hash.Hasher
	jwt        *jwt.Manager
	uow        transaction.UnitOfWork
	refreshTTL time.Duration
}

func NewAuthService(
	u repository.UserRepository,
	w repository.WalletRepository,
	rt repository.RefreshTokenRepository,
	h *hash.Hasher,
	j *jwt.Manager,
	uow transaction.UnitOfWork,
	refreshTTL time.Duration,
) AuthService {
	return &authService{
		users:      u,
		wallets:    w,
		refresh:    rt,
		hasher:     h,
		jwt:        j,
		uow:        uow,
		refreshTTL: refreshTTL,
	}
}

func (s *authService) Register(ctx context.Context, name, email, password string) (*model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" || name == "" {
		return nil, apperror.New(apperror.KindBadRequest, "INVALID_INPUT", "name, email, and password are required")
	}

	existing, err := s.users.FindByEmail(ctx, email)
	if err != nil && !errors.Is(err, apperror.ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		return nil, apperror.ErrEmailTaken
	}

	hashed, err := s.hasher.Hash(password)
	if err != nil {
		return nil, err
	}

	u := &model.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: hashed,
		Name:         name,
		Role:         constant.RoleMerchant,
	}

	err = s.uow.Do(ctx, func(ctx context.Context) error {
		if err := s.users.Create(ctx, u); err != nil {
			return err
		}
		return s.wallets.Create(ctx, &model.Wallet{
			ID:         uuid.New(),
			MerchantID: u.ID,
			Balance:    0,
		})
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *authService) Login(ctx context.Context, email, password string) (*TokenPair, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, apperror.ErrNotFound) {
			return nil, apperror.ErrInvalidCredentials
		}
		return nil, err
	}
	if err := s.hasher.Compare(u.PasswordHash, password); err != nil {
		return nil, apperror.ErrInvalidCredentials
	}
	return s.issueTokens(ctx, u)
}

// Refresh validates the opaque refresh token, rotates it (revoke + issue new pair) atomically.
func (s *authService) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	if refreshToken == "" {
		return nil, apperror.ErrUnauthorized
	}
	hash := hashRefreshToken(refreshToken)

	var pair *TokenPair
	err := s.uow.Do(ctx, func(ctx context.Context) error {
		stored, err := s.refresh.FindByHash(ctx, hash)
		if err != nil {
			if errors.Is(err, apperror.ErrNotFound) {
				return apperror.ErrUnauthorized
			}
			return err
		}
		now := time.Now().UTC()
		if !stored.IsActive(now) {
			return apperror.ErrUnauthorized
		}
		// Rotate: revoke old.
		if err := s.refresh.Revoke(ctx, stored.ID, now); err != nil {
			return err
		}
		u, err := s.users.FindByID(ctx, stored.UserID)
		if err != nil {
			return err
		}
		p, err := s.issueTokens(ctx, u)
		if err != nil {
			return err
		}
		pair = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pair, nil
}

// Logout revokes the supplied refresh token. Idempotent: revoking a missing/already-revoked token is a no-op.
func (s *authService) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	hash := hashRefreshToken(refreshToken)
	stored, err := s.refresh.FindByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, apperror.ErrNotFound) {
			return nil
		}
		return err
	}
	if stored.RevokedAt != nil {
		return nil
	}
	if err := s.refresh.Revoke(ctx, stored.ID, time.Now().UTC()); err != nil && !errors.Is(err, apperror.ErrInvalidState) {
		return err
	}
	return nil
}

// issueTokens creates a new access JWT and a new opaque refresh token, persisting only the hash.
func (s *authService) issueTokens(ctx context.Context, u *model.User) (*TokenPair, error) {
	access, accessExp, err := s.jwt.Issue(u.ID.String(), string(u.Role))
	if err != nil {
		return nil, err
	}
	refreshPlain, err := token.Random(32)
	if err != nil {
		return nil, err
	}
	refreshExp := time.Now().UTC().Add(s.refreshTTL)
	rt := &model.RefreshToken{
		ID:        uuid.New(),
		UserID:    u.ID,
		TokenHash: hashRefreshToken(refreshPlain),
		ExpiresAt: refreshExp,
	}
	if err := s.refresh.Create(ctx, rt); err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refreshPlain,
		RefreshExpiresAt: refreshExp,
		User:             u,
	}, nil
}

// hashRefreshToken uses SHA-256: deterministic and fast (we only need a one-way mapping
// for lookup; bcrypt would prevent indexed lookup).
func hashRefreshToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
