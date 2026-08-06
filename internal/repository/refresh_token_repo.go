package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

type refreshTokenRepo struct{ db *sql.DB }

func NewRefreshTokenRepo(db *sql.DB) RefreshTokenRepository { return &refreshTokenRepo{db: db} }

const refreshTokenColumns = `id, user_id, token_hash, expires_at, revoked_at, created_at`

func (r *refreshTokenRepo) Create(ctx context.Context, t *model.RefreshToken) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	_, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		INSERT INTO refresh_tokens (`+refreshTokenColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, t.UserID, t.TokenHash, t.ExpiresAt, timePtrToNullTime(t.RevokedAt), t.CreatedAt,
	)
	return mapWriteError(err)
}

func (r *refreshTokenRepo) FindByHash(ctx context.Context, hash string) (*model.RefreshToken, error) {
	row := transaction.FromCtx(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+refreshTokenColumns+` FROM refresh_tokens WHERE token_hash = $1`, hash)
	var t model.RefreshToken
	var revoked sql.NullTime
	err := row.Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &revoked, &t.CreatedAt)
	if err != nil {
		return nil, mapNoRowsToNotFound(err)
	}
	t.RevokedAt = nullTimePtr(revoked)
	return &t, nil
}

func (r *refreshTokenRepo) Revoke(ctx context.Context, id uuid.UUID, at time.Time) error {
	res, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = $1
		WHERE id = $2 AND revoked_at IS NULL`,
		at, id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperror.ErrInvalidState
	}
	return nil
}

func (r *refreshTokenRepo) RevokeAllForUser(ctx context.Context, userID uuid.UUID, at time.Time) error {
	_, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = $1
		WHERE user_id = $2 AND revoked_at IS NULL`,
		at, userID,
	)
	return err
}
