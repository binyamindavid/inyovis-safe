ALTER TABLE invoyis.accounts
    ADD COLUMN interface_language text NOT NULL DEFAULT 'en'
    CHECK (interface_language IN ('en', 'fr', 'he'));

CREATE TABLE invoyis.archives (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('personal', 'business')),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
    jurisdiction text CHECK (jurisdiction ~ '^[A-Z]{2}$'),
    created_by bigint NOT NULL REFERENCES invoyis.accounts(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (kind <> 'business' OR jurisdiction IS NOT NULL)
);

CREATE TABLE invoyis.archive_memberships (
    archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
    account_id bigint NOT NULL REFERENCES invoyis.accounts(id),
    role text NOT NULL CHECK (role IN ('admin', 'contributor', 'viewer')),
    can_bulk_export boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (archive_id, account_id)
);
CREATE INDEX archive_memberships_account ON invoyis.archive_memberships (account_id, archive_id);

CREATE TABLE invoyis.archive_invitations (
    id uuid PRIMARY KEY,
    archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
    email text NOT NULL CHECK (email = lower(btrim(email))),
    role text NOT NULL CHECK (role IN ('admin', 'contributor', 'viewer')),
    token_digest bytea NOT NULL UNIQUE,
    invited_by bigint NOT NULL REFERENCES invoyis.accounts(id),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX archive_invitations_email_active ON invoyis.archive_invitations (email, expires_at)
    WHERE revoked_at IS NULL AND consumed_at IS NULL;
CREATE INDEX archive_invitations_archive ON invoyis.archive_invitations (archive_id, created_at DESC);

CREATE TABLE invoyis.archive_audit (
    id uuid PRIMARY KEY,
    archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
    actor_id bigint NOT NULL REFERENCES invoyis.accounts(id),
    action text NOT NULL,
    target_account_id bigint REFERENCES invoyis.accounts(id),
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX archive_audit_archive_time ON invoyis.archive_audit (archive_id, created_at DESC, id DESC);

CREATE FUNCTION invoyis.current_account_id() RETURNS bigint
LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('invoyis.account_id', true), '')::bigint $$;
CREATE FUNCTION invoyis.current_archive_id() RETURNS uuid
LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('invoyis.archive_id', true), '')::uuid $$;
CREATE FUNCTION invoyis.in_requested_archive(target uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$ SELECT invoyis.current_archive_id() IS NULL OR target = invoyis.current_archive_id() $$;

-- These helpers run as the migration owner so membership policies do not recurse.
-- The runtime role receives EXECUTE but never ownership or BYPASSRLS.
CREATE FUNCTION invoyis.is_archive_member(target uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, invoyis AS $$
    SELECT EXISTS (SELECT 1 FROM invoyis.archive_memberships m
                   WHERE m.archive_id = target AND m.account_id = invoyis.current_account_id())
$$;
CREATE FUNCTION invoyis.is_archive_admin(target uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, invoyis AS $$
    SELECT EXISTS (SELECT 1 FROM invoyis.archive_memberships m
                   WHERE m.archive_id = target AND m.account_id = invoyis.current_account_id() AND m.role = 'admin')
$$;
CREATE FUNCTION invoyis.is_archive_bootstrap(target uuid, member_id bigint, member_role text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, invoyis AS $$
    SELECT member_id = invoyis.current_account_id() AND member_role = 'admin'
       AND EXISTS (SELECT 1 FROM invoyis.archives a WHERE a.id = target AND a.created_by = member_id)
       AND NOT EXISTS (SELECT 1 FROM invoyis.archive_memberships m WHERE m.archive_id = target)
$$;
CREATE FUNCTION invoyis.is_invitee(invited_email text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, invoyis AS $$
    SELECT EXISTS (SELECT 1 FROM invoyis.accounts a WHERE a.id = invoyis.current_account_id() AND a.email = invited_email)
$$;
CREATE FUNCTION invoyis.has_active_invitation(target uuid, member_id bigint) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, invoyis AS $$
    SELECT EXISTS (SELECT 1 FROM invoyis.archive_invitations i JOIN invoyis.accounts a ON a.email=i.email
                   WHERE i.archive_id=target AND a.id=member_id AND i.consumed_at IS NULL
                     AND i.revoked_at IS NULL AND i.expires_at > now())
$$;
CREATE FUNCTION invoyis.archive_member_email(target uuid, member_id bigint) RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, invoyis AS $$
    SELECT a.email FROM invoyis.accounts a WHERE a.id=member_id AND invoyis.is_archive_admin(target)
$$;

ALTER TABLE invoyis.archives ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.archive_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.archive_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoyis.archive_audit ENABLE ROW LEVEL SECURITY;

CREATE POLICY archives_read ON invoyis.archives FOR SELECT
    USING (invoyis.in_requested_archive(id) AND invoyis.is_archive_member(id));
CREATE POLICY archives_create ON invoyis.archives FOR INSERT
    WITH CHECK (created_by = invoyis.current_account_id() AND invoyis.in_requested_archive(id));
CREATE POLICY archives_update ON invoyis.archives FOR UPDATE
    USING (invoyis.in_requested_archive(id) AND invoyis.is_archive_admin(id))
    WITH CHECK (invoyis.in_requested_archive(id) AND invoyis.is_archive_admin(id));

CREATE POLICY memberships_read ON invoyis.archive_memberships FOR SELECT
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));
CREATE POLICY memberships_create ON invoyis.archive_memberships FOR INSERT
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND
        (invoyis.is_archive_admin(archive_id) OR invoyis.is_archive_bootstrap(archive_id, account_id, role)));
CREATE POLICY memberships_update ON invoyis.archive_memberships FOR UPDATE
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id))
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
CREATE POLICY memberships_delete ON invoyis.archive_memberships FOR DELETE
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));

CREATE POLICY invitations_read ON invoyis.archive_invitations FOR SELECT
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
CREATE POLICY invitations_create ON invoyis.archive_invitations FOR INSERT
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id)
        AND invited_by = invoyis.current_account_id());
CREATE POLICY invitations_update ON invoyis.archive_invitations FOR UPDATE
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id))
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));

CREATE POLICY audit_read ON invoyis.archive_audit FOR SELECT
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
CREATE POLICY audit_create ON invoyis.archive_audit FOR INSERT
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND actor_id = invoyis.current_account_id()
        AND invoyis.is_archive_member(archive_id));
