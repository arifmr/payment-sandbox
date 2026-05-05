-- 0001_init.sql: schema for payment-sandbox

CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY,
    email         VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    name          VARCHAR(255) NOT NULL,
    role          VARCHAR(32)  NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);

CREATE TABLE IF NOT EXISTS wallets (
    id          UUID PRIMARY KEY,
    merchant_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    balance     BIGINT NOT NULL DEFAULT 0,
    version     BIGINT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS topups (
    id           UUID PRIMARY KEY,
    merchant_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount       BIGINT NOT NULL,
    status       VARCHAR(32) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_topups_merchant ON topups(merchant_id);
CREATE INDEX IF NOT EXISTS idx_topups_status ON topups(status);

CREATE TABLE IF NOT EXISTS invoices (
    id             UUID PRIMARY KEY,
    invoice_number VARCHAR(64)  NOT NULL UNIQUE,
    merchant_id    UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    customer_name  VARCHAR(255) NOT NULL DEFAULT '',
    customer_email VARCHAR(255) NOT NULL DEFAULT '',
    description    VARCHAR(500) NOT NULL DEFAULT '',
    amount         BIGINT       NOT NULL,
    status         VARCHAR(32)  NOT NULL,
    due_date       TIMESTAMPTZ  NOT NULL,
    payment_token  VARCHAR(128) NOT NULL UNIQUE,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    paid_at        TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_invoices_merchant_status_created ON invoices(merchant_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_invoices_due_date ON invoices(due_date);

CREATE TABLE IF NOT EXISTS payment_intents (
    id            UUID PRIMARY KEY,
    invoice_id    UUID        NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    method        VARCHAR(32) NOT NULL,
    status        VARCHAR(32) NOT NULL,
    amount        BIGINT      NOT NULL,
    payer_user_id UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_payment_intents_invoice ON payment_intents(invoice_id);
CREATE INDEX IF NOT EXISTS idx_payment_intents_status ON payment_intents(status);

CREATE TABLE IF NOT EXISTS refunds (
    id                UUID PRIMARY KEY,
    invoice_id        UUID         NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    payment_intent_id UUID         NOT NULL REFERENCES payment_intents(id) ON DELETE CASCADE,
    merchant_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount            BIGINT       NOT NULL,
    reason            VARCHAR(500) NOT NULL DEFAULT '',
    status            VARCHAR(32)  NOT NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    processed_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_refunds_merchant ON refunds(merchant_id);
CREATE INDEX IF NOT EXISTS idx_refunds_invoice ON refunds(invoice_id);
CREATE INDEX IF NOT EXISTS idx_refunds_status ON refunds(status);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         UUID PRIMARY KEY,
    user_id    UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(128) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ  NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_refresh_user ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_expires ON refresh_tokens(expires_at);
