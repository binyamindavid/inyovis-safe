CREATE TABLE invoyis.accounts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email text NOT NULL UNIQUE,
    eligible boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT accounts_email_normalized CHECK (email = lower(btrim(email)))
);

CREATE TABLE invoyis.auth_challenges (
    id text PRIMARY KEY,
    email text NOT NULL,
    account_id bigint REFERENCES invoyis.accounts(id),
    purpose text NOT NULL CHECK (purpose = 'sign_in'),
    code_digest bytea NOT NULL,
    locale text NOT NULL CHECK (locale IN ('en', 'fr', 'he')),
    expires_at timestamptz NOT NULL,
    invalidated_at timestamptz,
    consumed_at timestamptz,
    verify_attempts integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_challenges_email_recent ON invoyis.auth_challenges (email, purpose, created_at DESC);

CREATE TABLE invoyis.auth_delivery_outbox (
    challenge_id text PRIMARY KEY REFERENCES invoyis.auth_challenges(id) ON DELETE CASCADE,
    encrypted_code bytea NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE invoyis.auth_rate_limits (
    kind text NOT NULL,
    key_digest bytea NOT NULL,
    window_start timestamptz NOT NULL,
    hits integer NOT NULL,
    PRIMARY KEY (kind, key_digest)
);

CREATE TABLE invoyis.sessions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id bigint NOT NULL REFERENCES invoyis.accounts(id),
    token_digest bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_account_id ON invoyis.sessions (account_id);
