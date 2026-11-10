DROP FUNCTION invoyis.redeem_archive_invitation(bytea, bigint);
DROP POLICY invitations_read ON invoyis.archive_invitations;
CREATE POLICY invitations_read ON invoyis.archive_invitations FOR SELECT
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
DROP POLICY invitations_update ON invoyis.archive_invitations;
CREATE POLICY invitations_update ON invoyis.archive_invitations FOR UPDATE
    USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id))
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id));
DROP POLICY memberships_create ON invoyis.archive_memberships;
CREATE POLICY memberships_create ON invoyis.archive_memberships FOR INSERT
    WITH CHECK (invoyis.in_requested_archive(archive_id) AND
        (invoyis.is_archive_admin(archive_id) OR invoyis.is_archive_bootstrap(archive_id, account_id, role)));
ALTER TABLE invoyis.auth_challenges DROP COLUMN invitation_id;
ALTER TABLE invoyis.accounts DROP COLUMN pending_invitation_id;
