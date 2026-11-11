-- Keys are supplied by the versioned application normaliser. The database owns
-- reservations and checks every relationship, including writes outside the API.
CREATE TABLE invoyis.suppliers (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 240 AND display_name=btrim(display_name)),
 identity_key text NOT NULL CHECK (identity_key<>''), suggestion_key text NOT NULL CHECK (suggestion_key<>''),
 key_version integer NOT NULL CHECK (key_version=1), suggestion_version integer NOT NULL CHECK (suggestion_version=1),
 archived_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(archive_id,id), UNIQUE(archive_id,id,identity_key)
);
CREATE TABLE invoyis.supplier_identity_keys (
 archive_id uuid NOT NULL, identity_key text NOT NULL CHECK (identity_key<>''),
 supplier_id uuid NOT NULL, kind text NOT NULL CHECK (kind IN ('canonical','alias')),
 key_version integer NOT NULL CHECK (key_version=1),
 suggestion_key text NOT NULL CHECK (suggestion_key<>''), suggestion_version integer NOT NULL CHECK (suggestion_version=1),
 PRIMARY KEY(archive_id,identity_key),
 FOREIGN KEY(archive_id,supplier_id) REFERENCES invoyis.suppliers(archive_id,id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX supplier_one_canonical ON invoyis.supplier_identity_keys(archive_id,supplier_id) WHERE kind='canonical';
CREATE INDEX supplier_suggestions ON invoyis.supplier_identity_keys(archive_id,suggestion_key);

CREATE FUNCTION invoyis.check_active_alias_target() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF NEW.kind='alias' AND (TG_OP='INSERT' OR NEW.supplier_id IS DISTINCT FROM OLD.supplier_id) AND
  NOT EXISTS(SELECT 1 FROM invoyis.suppliers WHERE archive_id=NEW.archive_id AND id=NEW.supplier_id AND archived_at IS NULL FOR SHARE) THEN
  RAISE EXCEPTION 'alias target must be active in the same archive' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER supplier_alias_active BEFORE INSERT OR UPDATE OF supplier_id ON invoyis.supplier_identity_keys
 FOR EACH ROW EXECUTE FUNCTION invoyis.check_active_alias_target();

CREATE FUNCTION invoyis.sync_supplier_canonical() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  INSERT INTO invoyis.supplier_identity_keys(archive_id,identity_key,supplier_id,kind,key_version,suggestion_key,suggestion_version)
  VALUES(NEW.archive_id,NEW.identity_key,NEW.id,'canonical',NEW.key_version,NEW.suggestion_key,NEW.suggestion_version);
 ELSIF NEW.identity_key IS DISTINCT FROM OLD.identity_key THEN
  DELETE FROM invoyis.supplier_identity_keys WHERE archive_id=OLD.archive_id AND supplier_id=OLD.id AND kind='canonical';
  INSERT INTO invoyis.supplier_identity_keys(archive_id,identity_key,supplier_id,kind,key_version,suggestion_key,suggestion_version)
  VALUES(NEW.archive_id,NEW.identity_key,NEW.id,'canonical',NEW.key_version,NEW.suggestion_key,NEW.suggestion_version);
 ELSE
  UPDATE invoyis.supplier_identity_keys SET suggestion_key=NEW.suggestion_key, key_version=NEW.key_version,
   suggestion_version=NEW.suggestion_version WHERE archive_id=NEW.archive_id AND supplier_id=NEW.id AND kind='canonical';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER supplier_canonical_sync AFTER INSERT OR UPDATE OF identity_key,suggestion_key,key_version,suggestion_version ON invoyis.suppliers
 FOR EACH ROW EXECUTE FUNCTION invoyis.sync_supplier_canonical();

-- Deferred check allows the synchronising trigger to replace a canonical key
-- in one statement, but rejects tampering with the registry at commit.
CREATE FUNCTION invoyis.check_supplier_registry() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE sid uuid; aid uuid; canon text; suggestion text; kv integer; sv integer; n integer;
BEGIN
 aid:=coalesce(NEW.archive_id,OLD.archive_id); sid:=coalesce(NEW.supplier_id,OLD.supplier_id);
 SELECT identity_key,suggestion_key,key_version,suggestion_version INTO canon,suggestion,kv,sv FROM invoyis.suppliers WHERE archive_id=aid AND id=sid;
 IF canon IS NOT NULL THEN
  SELECT count(*) INTO n FROM invoyis.supplier_identity_keys
   WHERE archive_id=aid AND supplier_id=sid AND kind='canonical' AND identity_key=canon
    AND suggestion_key=suggestion AND key_version=kv AND suggestion_version=sv;
  IF n<>1 THEN RAISE EXCEPTION 'supplier canonical registry mismatch' USING ERRCODE='23514'; END IF;
 END IF;
 IF TG_OP<>'DELETE' AND NEW.kind='canonical' AND (NEW.identity_key IS DISTINCT FROM canon
  OR NEW.suggestion_key IS DISTINCT FROM suggestion OR NEW.key_version IS DISTINCT FROM kv
  OR NEW.suggestion_version IS DISTINCT FROM sv) THEN
  RAISE EXCEPTION 'supplier canonical registry mismatch' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER supplier_registry_consistency AFTER INSERT OR UPDATE OR DELETE ON invoyis.supplier_identity_keys
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION invoyis.check_supplier_registry();

CREATE TABLE invoyis.categories (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 display_name text NOT NULL CHECK(length(btrim(display_name)) BETWEEN 1 AND 240 AND display_name=btrim(display_name)),
 identity_key text NOT NULL CHECK(identity_key<>''), suggestion_key text NOT NULL CHECK(suggestion_key<>''),
 key_version integer NOT NULL CHECK(key_version=1), suggestion_version integer NOT NULL CHECK(suggestion_version=1),
 archived_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(archive_id,id), UNIQUE(archive_id,identity_key)
);
CREATE TABLE invoyis.properties (LIKE invoyis.categories INCLUDING ALL);
CREATE TABLE invoyis.paper_locations (LIKE invoyis.categories INCLUDING ALL);
ALTER TABLE invoyis.properties ADD FOREIGN KEY(archive_id) REFERENCES invoyis.archives(id);
ALTER TABLE invoyis.paper_locations ADD FOREIGN KEY(archive_id) REFERENCES invoyis.archives(id);

CREATE FUNCTION invoyis.organisation_immutable_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id IS DISTINCT FROM OLD.id OR NEW.archive_id IS DISTINCT FROM OLD.archive_id THEN
  RAISE EXCEPTION 'organisation scope and ID are immutable' USING ERRCODE='23514';
 END IF;
 IF OLD.archived_at IS NOT NULL AND (NEW.identity_key IS DISTINCT FROM OLD.identity_key OR NEW.display_name IS DISTINCT FROM OLD.display_name) THEN
  RAISE EXCEPTION 'archived identity cannot be renamed' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['suppliers','categories','properties','paper_locations'] LOOP
  EXECUTE format('CREATE TRIGGER organisation_scope BEFORE UPDATE ON invoyis.%I FOR EACH ROW EXECUTE FUNCTION invoyis.organisation_immutable_scope()',t);
 END LOOP;
END $$;

DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['suppliers','categories','properties','paper_locations','supplier_identity_keys'] LOOP
  EXECUTE format('ALTER TABLE invoyis.%I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('CREATE POLICY org_read ON invoyis.%I FOR SELECT USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id))',t);
  IF t='supplier_identity_keys' THEN
   EXECUTE format('CREATE POLICY org_insert ON invoyis.%I FOR INSERT WITH CHECK (invoyis.in_requested_archive(archive_id) AND kind=''alias'' AND invoyis.can_submit(archive_id))',t);
  ELSE
   EXECUTE format('CREATE POLICY org_insert ON invoyis.%I FOR INSERT WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id))',t);
  END IF;
  EXECUTE format('CREATE POLICY org_update ON invoyis.%I FOR UPDATE USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id)) WITH CHECK (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_admin(archive_id))',t);
  EXECUTE format('CREATE POLICY org_delete ON invoyis.%I FOR DELETE USING (false)',t);
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
   EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON invoyis.%I TO invoyis_archive_runtime',t);
  END IF;
 END LOOP;
END $$;
