ALTER TABLE invoyis.auth_delivery_outbox
    ADD COLUMN locked_until timestamptz,
    ADD COLUMN claim_token text;

CREATE INDEX auth_delivery_outbox_ready
    ON invoyis.auth_delivery_outbox (next_attempt_at, locked_until);
