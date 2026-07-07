-- Weave Gate 1: multi-tenant use-case RBAC + sessions.
-- gen_random_uuid() is built in on PostgreSQL 13+.

CREATE TABLE users (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject    TEXT        NOT NULL UNIQUE,
    email      TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE use_cases (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key            TEXT        NOT NULL UNIQUE,
    display_name   TEXT        NOT NULL DEFAULT '',
    repo_url       TEXT        NOT NULL,
    repo_slug      TEXT        NOT NULL,
    pr_provider    TEXT        NOT NULL DEFAULT 'bitbucket-cloud',
    base_branch    TEXT        NOT NULL DEFAULT 'main',
    env            TEXT        NOT NULL,
    credential_ref TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    user_id     UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    use_case_id UUID NOT NULL REFERENCES use_cases(id)  ON DELETE CASCADE,
    role        TEXT NOT NULL,
    PRIMARY KEY (user_id, use_case_id)
);

CREATE TABLE group_grants (
    use_case_id UUID NOT NULL REFERENCES use_cases(id) ON DELETE CASCADE,
    group_name  TEXT NOT NULL,
    role        TEXT NOT NULL,
    PRIMARY KEY (use_case_id, group_name)
);

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_memberships_use_case ON memberships (use_case_id);
CREATE INDEX idx_group_grants_group   ON group_grants (group_name);
CREATE INDEX idx_sessions_expires     ON sessions (expires_at);
