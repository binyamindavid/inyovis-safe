ALTER TABLE invoyis.accounts
    ADD COLUMN pending_invitation_id uuid REFERENCES invoyis.archive_invitations(id);
ALTER TABLE invoyis.auth_challenges
    ADD COLUMN invitation_id uuid REFERENCES invoyis.archive_invitations(id);

DROP POLICY memberships_create ON invoyis.archive_memberships;
CREATE POLICY memberships_create ON invoyis.archive_memberships FOR INSERT
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND
        (invoyis.is_archive_admin(archive_id)
         OR invoyis.is_archive_bootstrap(archive_id, account_id, role)));

DROP POLICY invitations_read ON invoyis.archive_invitations;
CREATE POLICY invitations_read ON invoyis.archive_invitations FOR SELECT
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
DROP POLICY invitations_update ON invoyis.archive_invitations;
CREATE POLICY invitations_update ON invoyis.archive_invitations FOR UPDATE
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id))
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));

-- The caller supplies only a digest and its session-derived account ID. The
-- function reads the invitation's role, forces export off, and commits nothing
-- itself; Go's surrounding transaction also writes the audit row.
CREATE FUNCTION invoyis.redeem_archive_invitation(token_hash bytea, invitee_id bigint)
RETURNS TABLE(redeemed_archive_id uuid, redeemed_role text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, invoyis AS $$
DECLARE
    target_archive uuid;
    invitation_row invoyis.archive_invitations%ROWTYPE;
    account_row invoyis.accounts%ROWTYPE;
BEGIN
    IF invitee_id IS DISTINCT FROM invoyis.current_account_id() THEN
        RETURN;
    END IF;
    SELECT i.archive_id INTO target_archive FROM invoyis.archive_invitations i
        WHERE i.token_digest = token_hash;
    IF target_archive IS NULL OR NOT invoyis.in_requested_archive(target_archive) THEN
        RETURN;
    END IF;
    PERFORM 1 FROM invoyis.archives a WHERE a.id = target_archive FOR UPDATE;
    SELECT i.* INTO invitation_row FROM invoyis.archive_invitations i
        WHERE i.token_digest = token_hash FOR UPDATE;
    IF NOT FOUND OR invitation_row.revoked_at IS NOT NULL
        OR invitation_row.consumed_at IS NOT NULL OR invitation_row.expires_at <= now() THEN
        RETURN;
    END IF;
    SELECT a.* INTO account_row FROM invoyis.accounts a WHERE a.id = invitee_id FOR UPDATE;
    IF NOT FOUND OR account_row.email <> invitation_row.email
        OR (NOT account_row.eligible AND account_row.pending_invitation_id IS DISTINCT FROM invitation_row.id) THEN
        RETURN;
    END IF;
    INSERT INTO invoyis.archive_memberships (archive_id, account_id, role, can_bulk_export)
        VALUES (target_archive, invitee_id, invitation_row.role, false);
    UPDATE invoyis.archive_invitations SET consumed_at = now() WHERE id = invitation_row.id;
    UPDATE invoyis.archive_invitations SET revoked_at = now()
        WHERE archive_id = target_archive AND email = invitation_row.email
          AND id <> invitation_row.id AND consumed_at IS NULL AND revoked_at IS NULL;
    IF NOT account_row.eligible THEN
        UPDATE invoyis.accounts SET eligible = true, pending_invitation_id = NULL WHERE id = invitee_id;
    END IF;
    redeemed_archive_id := target_archive;
    redeemed_role := invitation_row.role;
    RETURN NEXT;
END;
$$;
REVOKE ALL ON FUNCTION invoyis.redeem_archive_invitation(bytea, bigint) FROM PUBLIC;
