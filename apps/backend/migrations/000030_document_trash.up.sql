-- Reversible removal is deliberately separate from physical disposal.
ALTER TABLE invoyis.documents
 ADD COLUMN trashed_at timestamptz,
 ADD COLUMN recovery_until timestamptz,
 ADD CONSTRAINT document_trash_pair CHECK ((trashed_at IS NULL) = (recovery_until IS NULL));
CREATE INDEX documents_trash_lookup ON invoyis.documents(archive_id,recovery_until,id) WHERE trashed_at IS NOT NULL;

-- Every ordinary document join (including search, counts and new exports) sees live documents only.
-- The narrowly scoped Trash API opts in for its own transaction.
CREATE POLICY document_live_read ON invoyis.documents AS RESTRICTIVE FOR SELECT
 USING (trashed_at IS NULL OR current_setting('invoyis.include_trash',true)='on');

CREATE TABLE invoyis.document_trash_events (
 id uuid PRIMARY KEY, archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 document_id uuid NOT NULL, actor_id bigint NOT NULL REFERENCES invoyis.accounts(id),
 action text NOT NULL CHECK (action IN ('trash','restore')),
 reason text NOT NULL, occurred_at timestamptz NOT NULL DEFAULT now(),
 recovery_until timestamptz,
 FOREIGN KEY (archive_id,document_id) REFERENCES invoyis.documents(archive_id,id)
);
CREATE INDEX document_trash_events_history ON invoyis.document_trash_events(archive_id,document_id,occurred_at,id);
ALTER TABLE invoyis.document_trash_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY trash_events_read ON invoyis.document_trash_events FOR SELECT
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));

-- The runtime has no direct UPDATE grant on the trash columns. These functions
-- validate the transaction's actor, archive, membership and current state.
CREATE FUNCTION invoyis.can_trash(target uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT EXISTS(SELECT 1 FROM invoyis.archive_memberships WHERE archive_id=target AND account_id=invoyis.current_account_id() AND role IN ('admin','contributor'))
$$;
CREATE FUNCTION invoyis.can_restore(target uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT EXISTS(SELECT 1 FROM invoyis.archive_memberships WHERE archive_id=target AND account_id=invoyis.current_account_id() AND role IN ('admin','contributor'))
$$;
CREATE FUNCTION invoyis.can_shred(target uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT EXISTS(SELECT 1 FROM invoyis.archive_memberships WHERE archive_id=target AND account_id=invoyis.current_account_id() AND role='admin')
$$;
CREATE FUNCTION invoyis.move_document_to_trash(target_archive uuid,target_document uuid,reason text)
RETURNS timestamptz LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE deadline timestamptz; filename text;
BEGIN
 IF invoyis.current_archive_id() IS DISTINCT FROM target_archive
    OR NOT invoyis.can_trash(target_archive) THEN RAISE EXCEPTION 'trash permission denied' USING ERRCODE='42501'; END IF;
 IF length(btrim(reason)) NOT BETWEEN 1 AND 1000 THEN RAISE EXCEPTION 'trash reason required' USING ERRCODE='22023'; END IF;
 SELECT d.recovery_until,s.original_filename INTO deadline,filename
 FROM invoyis.documents d JOIN invoyis.submissions s ON s.id=d.submission_id
 WHERE d.archive_id=target_archive AND d.id=target_document FOR UPDATE OF d;
 IF NOT FOUND THEN RAISE EXCEPTION 'document not found' USING ERRCODE='02000'; END IF;
 IF deadline IS NOT NULL THEN RETURN deadline; END IF;
 deadline:=now()+interval '30 days';
 UPDATE invoyis.documents SET trashed_at=now(),recovery_until=deadline
 WHERE archive_id=target_archive AND id=target_document;
 INSERT INTO invoyis.document_trash_events(id,archive_id,document_id,actor_id,action,reason,recovery_until)
 VALUES(gen_random_uuid(),target_archive,target_document,invoyis.current_account_id(),'trash',reason,deadline);
 INSERT INTO invoyis.archive_audit(id,archive_id,actor_id,action,details)
 VALUES(gen_random_uuid(),target_archive,invoyis.current_account_id(),'document_trashed',
 jsonb_build_object('documentId',target_document,'name',filename,'recoveryUntil',deadline,'reason',reason));
 RETURN deadline;
END $$;

CREATE FUNCTION invoyis.restore_document_from_trash(target_archive uuid,target_document uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE deadline timestamptz;
BEGIN
 IF invoyis.current_archive_id() IS DISTINCT FROM target_archive
    OR NOT invoyis.can_restore(target_archive) THEN RAISE EXCEPTION 'restore permission denied' USING ERRCODE='42501'; END IF;
 SELECT recovery_until INTO deadline FROM invoyis.documents
 WHERE archive_id=target_archive AND id=target_document FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'document not found' USING ERRCODE='02000'; END IF;
 IF deadline IS NULL THEN RETURN true; END IF;
 IF deadline<=now() THEN RAISE EXCEPTION 'recovery period expired' USING ERRCODE='22023'; END IF;
 UPDATE invoyis.documents SET trashed_at=NULL,recovery_until=NULL
 WHERE archive_id=target_archive AND id=target_document;
 INSERT INTO invoyis.document_trash_events(id,archive_id,document_id,actor_id,action,reason)
 VALUES(gen_random_uuid(),target_archive,target_document,invoyis.current_account_id(),'restore','user_restore');
 INSERT INTO invoyis.archive_audit(id,archive_id,actor_id,action,details)
 VALUES(gen_random_uuid(),target_archive,invoyis.current_account_id(),'document_restored',jsonb_build_object('documentId',target_document));
 RETURN true;
END $$;
REVOKE ALL ON FUNCTION invoyis.can_trash(uuid),invoyis.can_restore(uuid),invoyis.can_shred(uuid),invoyis.move_document_to_trash(uuid,uuid,text),invoyis.restore_document_from_trash(uuid,uuid) FROM PUBLIC;
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
 GRANT SELECT ON invoyis.document_trash_events TO invoyis_archive_runtime;
 GRANT EXECUTE ON FUNCTION invoyis.can_trash(uuid),invoyis.can_restore(uuid),invoyis.can_shred(uuid),invoyis.move_document_to_trash(uuid,uuid,text),invoyis.restore_document_from_trash(uuid,uuid) TO invoyis_archive_runtime;
END IF; END $$;
