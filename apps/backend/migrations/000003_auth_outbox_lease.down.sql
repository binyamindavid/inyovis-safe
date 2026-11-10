DROP INDEX invoyis.auth_delivery_outbox_ready;
ALTER TABLE invoyis.auth_delivery_outbox
    DROP COLUMN claim_token,
    DROP COLUMN locked_until;
