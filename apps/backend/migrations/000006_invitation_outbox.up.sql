CREATE TABLE invoyis.archive_invitation_outbox (
    invitation_id uuid PRIMARY KEY REFERENCES invoyis.archive_invitations(id) ON DELETE CASCADE,
    archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
    encrypted_message bytea NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    claim_token uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX archive_invitation_outbox_ready
    ON invoyis.archive_invitation_outbox (next_attempt_at, locked_until);
ALTER TABLE invoyis.archive_invitation_outbox ENABLE ROW LEVEL SECURITY;
CREATE POLICY archive_invitation_outbox_create ON invoyis.archive_invitation_outbox FOR INSERT
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
