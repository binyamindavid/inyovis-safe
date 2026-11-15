ALTER TABLE invoyis.upload_sessions ALTER COLUMN uploader_id DROP NOT NULL;
ALTER TABLE invoyis.submissions ALTER COLUMN uploader_id DROP NOT NULL;
ALTER TABLE invoyis.submissions DROP CONSTRAINT submissions_source_check;
ALTER TABLE invoyis.submissions ADD CONSTRAINT submissions_source_check CHECK (source IN ('browser_upload','forwarded_email'));

CREATE TABLE invoyis.inbound_deliveries (
 fingerprint text PRIMARY KEY CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
 archive_id uuid REFERENCES invoyis.archives(id),
 address_token text,
 outcome text NOT NULL CHECK (outcome IN ('pending','accepted','accepted_with_rejections','attachment_rejected','upload_guidance','no_pdf','routing_rejected','unknown_address','disabled_address','retired_address','address_quota','archive_quota')),
 accepted_count integer NOT NULL DEFAULT 0,
 rejected_count integer NOT NULL DEFAULT 0,
 received_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz
);
CREATE INDEX inbound_archive_received ON invoyis.inbound_deliveries(archive_id,received_at DESC);
CREATE INDEX inbound_address_quota ON invoyis.inbound_deliveries(address_token,received_at DESC);
ALTER TABLE invoyis.inbound_deliveries ENABLE ROW LEVEL SECURITY;
CREATE POLICY inbound_delivery_member_read ON invoyis.inbound_deliveries FOR SELECT
 USING (archive_id IS NOT NULL AND invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));

CREATE TABLE invoyis.inbound_parts (
 delivery_fingerprint text NOT NULL REFERENCES invoyis.inbound_deliveries(fingerprint),
 part_key text NOT NULL CHECK (part_key ~ '^attachment[1-9][0-9]*$'),
 archive_id uuid NOT NULL REFERENCES invoyis.archives(id),
 filename text NOT NULL,
 byte_count bigint NOT NULL CHECK (byte_count >= 0),
 sha256 text NOT NULL CHECK (sha256 ~ '^[a-f0-9]{64}$'),
 state text NOT NULL CHECK (state IN ('pending','accepted','rejected')),
 reason text,
 document_id uuid REFERENCES invoyis.documents(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (delivery_fingerprint,part_key)
);
ALTER TABLE invoyis.inbound_parts ENABLE ROW LEVEL SECURITY;
CREATE POLICY inbound_part_member_read ON invoyis.inbound_parts FOR SELECT
 USING (invoyis.in_requested_archive(archive_id) AND invoyis.is_archive_member(archive_id));

CREATE FUNCTION invoyis.inbound_admit(digest text, token text, fresh boolean, rejection text)
RETURNS TABLE(target_archive uuid, result text, known boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE current_row invoyis.inbound_deliveries%ROWTYPE;
DECLARE address_row invoyis.forwarding_addresses%ROWTYPE;
BEGIN
 IF digest !~ '^[a-f0-9]{64}$' OR token IS NULL OR rejection NOT IN ('','routing_rejected','attachment_rejected') THEN
  RAISE EXCEPTION 'invalid inbound admission';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtext(digest));
 SELECT * INTO current_row FROM invoyis.inbound_deliveries WHERE fingerprint=digest FOR UPDATE;
 IF FOUND THEN
  RETURN QUERY SELECT current_row.archive_id,current_row.outcome,true;
  RETURN;
 END IF;
 IF NOT fresh THEN
  RETURN QUERY SELECT NULL::uuid,'stale'::text,false;
  RETURN;
 END IF;
 IF rejection='routing_rejected' THEN
  INSERT INTO invoyis.inbound_deliveries(fingerprint,outcome) VALUES(digest,rejection);
  RETURN QUERY SELECT NULL::uuid,rejection,false;
  RETURN;
 END IF;
 SELECT * INTO address_row FROM invoyis.forwarding_addresses WHERE forwarding_addresses.token=inbound_admit.token;
 IF NOT FOUND THEN
  INSERT INTO invoyis.inbound_deliveries(fingerprint,address_token,outcome) VALUES(digest,token,'unknown_address');
  RETURN QUERY SELECT NULL::uuid,'unknown_address'::text,false;
  RETURN;
 END IF;
 -- Lock in the same order as address management: archive, then address.
 PERFORM 1 FROM invoyis.archives WHERE id=address_row.archive_id FOR UPDATE;
 SELECT * INTO address_row FROM invoyis.forwarding_addresses WHERE forwarding_addresses.token=inbound_admit.token FOR UPDATE;
 IF address_row.state<>'active' THEN
  result:=CASE address_row.state WHEN 'retired' THEN 'retired_address' ELSE 'disabled_address' END;
  INSERT INTO invoyis.inbound_deliveries(fingerprint,archive_id,address_token,outcome) VALUES(digest,address_row.archive_id,token,result);
  RETURN QUERY SELECT address_row.archive_id,result,false;
  RETURN;
 END IF;
 -- The address row serializes admissions for this address; the archive row
 -- serializes the archive-wide quota across all of its addresses.
 IF (SELECT count(*) FROM invoyis.inbound_deliveries WHERE address_token=token AND received_at>now()-interval '1 day'
  AND outcome IN ('pending','accepted','accepted_with_rejections','attachment_rejected','upload_guidance','no_pdf'))>=100 THEN
  result:='address_quota';
 ELSIF (SELECT count(*) FROM invoyis.inbound_deliveries WHERE archive_id=address_row.archive_id AND received_at>now()-interval '1 day'
  AND outcome IN ('pending','accepted','accepted_with_rejections','attachment_rejected','upload_guidance','no_pdf'))>=500 THEN
  result:='archive_quota';
 ELSE result:=CASE WHEN rejection='attachment_rejected' THEN 'attachment_rejected' ELSE 'pending' END; END IF;
 INSERT INTO invoyis.inbound_deliveries(fingerprint,archive_id,address_token,outcome)
 VALUES(digest,address_row.archive_id,token,result);
 RETURN QUERY SELECT address_row.archive_id,result,false;
END $$;

CREATE FUNCTION invoyis.inbound_part_prepare(digest text, key text, name text, bytes bigint, file_sha text, rejection text)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE delivery invoyis.inbound_deliveries%ROWTYPE;
DECLARE existing invoyis.inbound_parts%ROWTYPE;
DECLARE intended text;
BEGIN
 SELECT * INTO delivery FROM invoyis.inbound_deliveries WHERE fingerprint=digest FOR UPDATE;
 IF NOT FOUND OR delivery.archive_id IS NULL THEN RAISE EXCEPTION 'delivery unavailable'; END IF;
 IF key !~ '^attachment[1-9][0-9]*$' OR length(name)<1 OR length(name)>255 OR name ~ '[\\/]' OR bytes<0 OR file_sha !~ '^[a-f0-9]{64}$' THEN
  RAISE EXCEPTION 'invalid inbound part';
 END IF;
 intended:=CASE WHEN rejection='' THEN 'pending' ELSE 'rejected' END;
 SELECT * INTO existing FROM invoyis.inbound_parts WHERE delivery_fingerprint=digest AND part_key=key FOR UPDATE;
 IF FOUND THEN
  IF existing.sha256<>file_sha OR existing.byte_count<>bytes THEN RAISE EXCEPTION 'inbound part changed'; END IF;
  RETURN existing.state;
 END IF;
 IF delivery.outcome<>'pending' THEN RAISE EXCEPTION 'delivery unavailable'; END IF;
 INSERT INTO invoyis.inbound_parts(delivery_fingerprint,part_key,archive_id,filename,byte_count,sha256,state,reason)
 VALUES(digest,key,delivery.archive_id,name,bytes,file_sha,intended,nullif(rejection,''));
 RETURN intended;
END $$;

CREATE FUNCTION invoyis.inbound_canonical(digest text, file_sha text)
RETURNS TABLE(storage_key text,byte_count bigint) LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
 SELECT o.storage_key,o.byte_count FROM invoyis.inbound_deliveries d
 JOIN invoyis.stored_objects o ON o.archive_id=d.archive_id AND o.sha256=file_sha
 WHERE d.fingerprint=digest AND d.outcome='pending' LIMIT 1
$$;

CREATE FUNCTION invoyis.inbound_part_publish(digest text, key text, object_key text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE delivery invoyis.inbound_deliveries%ROWTYPE;
DECLARE part invoyis.inbound_parts%ROWTYPE;
DECLARE session_id uuid:=gen_random_uuid();
DECLARE object_id uuid;
DECLARE submission_id uuid:=gen_random_uuid();
DECLARE published_document_id uuid;
DECLARE receipt_id uuid:=gen_random_uuid();
DECLARE canonical_bytes bigint;
BEGIN
 SELECT * INTO delivery FROM invoyis.inbound_deliveries WHERE fingerprint=digest FOR UPDATE;
 IF NOT FOUND OR delivery.archive_id IS NULL THEN RAISE EXCEPTION 'delivery unavailable'; END IF;
 SELECT * INTO part FROM invoyis.inbound_parts WHERE delivery_fingerprint=digest AND part_key=key FOR UPDATE;
 IF NOT FOUND OR part.archive_id<>delivery.archive_id THEN RAISE EXCEPTION 'part unavailable'; END IF;
 IF part.state='accepted' THEN RETURN part.document_id; END IF;
 IF delivery.outcome<>'pending' THEN RAISE EXCEPTION 'delivery unavailable'; END IF;
 IF part.state<>'pending' OR object_key<>('inbound/'||digest||'/'||key) THEN RAISE EXCEPTION 'part not publishable'; END IF;
 PERFORM pg_advisory_xact_lock(hashtext(delivery.archive_id::text),hashtext(part.sha256));
 SELECT id,byte_count INTO object_id,canonical_bytes FROM invoyis.stored_objects
 WHERE archive_id=delivery.archive_id AND sha256=part.sha256;
 IF FOUND AND canonical_bytes<>part.byte_count THEN RAISE EXCEPTION 'canonical size mismatch'; END IF;
 INSERT INTO invoyis.upload_sessions(id,archive_id,uploader_id,idempotency_key,filename,media_type,
  declared_bytes,declared_sha256,staging_key,prepared_key,final_key,state,grant_expires_at,finalise_by,verified_bytes,verified_sha256)
 VALUES(session_id,delivery.archive_id,NULL,digest||':'||key,part.filename,'application/pdf',part.byte_count,part.sha256,
  'inbound-staging/'||session_id,'inbound-prepared/'||session_id,object_key,'complete',now()+interval '1 day',now()+interval '1 day',part.byte_count,part.sha256);
 IF object_id IS NULL THEN
  object_id:=gen_random_uuid();published_document_id:=gen_random_uuid();
  INSERT INTO invoyis.stored_objects(id,archive_id,upload_session_id,storage_key,byte_count,sha256)
  VALUES(object_id,delivery.archive_id,session_id,object_key,part.byte_count,part.sha256);
 ELSE
  SELECT d.id INTO published_document_id FROM invoyis.submissions s JOIN invoyis.documents d ON d.submission_id=s.id
  WHERE s.archive_id=delivery.archive_id AND s.stored_object_id=object_id ORDER BY d.created_at,d.id LIMIT 1;
  IF published_document_id IS NULL THEN RAISE EXCEPTION 'canonical document missing'; END IF;
 END IF;
 INSERT INTO invoyis.submissions(id,archive_id,upload_session_id,stored_object_id,uploader_id,source,original_filename)
 VALUES(submission_id,delivery.archive_id,session_id,object_id,NULL,'forwarded_email',part.filename);
 IF NOT EXISTS (SELECT 1 FROM invoyis.documents WHERE id=published_document_id) THEN
  INSERT INTO invoyis.documents(id,archive_id,submission_id,state) VALUES(published_document_id,delivery.archive_id,submission_id,'received');
  INSERT INTO invoyis.processing_jobs(id,archive_id,document_id,state) VALUES(gen_random_uuid(),delivery.archive_id,published_document_id,'pending');
 END IF;
 INSERT INTO invoyis.upload_receipts(id,archive_id,upload_session_id,submission_id,document_id,byte_count,sha256)
 VALUES(receipt_id,delivery.archive_id,session_id,submission_id,published_document_id,part.byte_count,part.sha256);
 UPDATE invoyis.inbound_parts SET state='accepted',document_id=published_document_id WHERE delivery_fingerprint=digest AND part_key=key;
 RETURN published_document_id;
END $$;

CREATE FUNCTION invoyis.inbound_finish(digest text, body_only boolean)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,invoyis AS $$
DECLARE delivery invoyis.inbound_deliveries%ROWTYPE;
DECLARE accepted integer;
DECLARE rejected integer;
DECLARE pending integer;
DECLARE result text;
BEGIN
 SELECT * INTO delivery FROM invoyis.inbound_deliveries WHERE fingerprint=digest FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'delivery unavailable'; END IF;
 IF delivery.outcome<>'pending' THEN RETURN delivery.outcome; END IF;
 SELECT count(*) FILTER (WHERE state='accepted'),count(*) FILTER (WHERE state='rejected'),count(*) FILTER (WHERE state='pending')
 INTO accepted,rejected,pending FROM invoyis.inbound_parts WHERE delivery_fingerprint=digest;
 IF pending>0 THEN RAISE EXCEPTION 'inbound parts unfinished'; END IF;
 result:=CASE WHEN accepted>0 AND rejected>0 THEN 'accepted_with_rejections'
  WHEN accepted>0 THEN 'accepted' WHEN rejected>0 THEN 'attachment_rejected'
  WHEN body_only THEN 'upload_guidance' ELSE 'no_pdf' END;
 UPDATE invoyis.inbound_deliveries SET outcome=result,accepted_count=accepted,rejected_count=rejected,finished_at=now()
 WHERE fingerprint=digest;
 RETURN result;
END $$;

REVOKE ALL ON FUNCTION invoyis.inbound_admit(text,text,boolean,text),invoyis.inbound_part_prepare(text,text,text,bigint,text,text),
 invoyis.inbound_canonical(text,text),invoyis.inbound_part_publish(text,text,text),invoyis.inbound_finish(text,boolean) FROM PUBLIC;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='invoyis_archive_runtime') THEN
  GRANT SELECT ON invoyis.inbound_deliveries,invoyis.inbound_parts TO invoyis_archive_runtime;
  GRANT EXECUTE ON FUNCTION invoyis.inbound_admit(text,text,boolean,text),invoyis.inbound_part_prepare(text,text,text,bigint,text,text),
   invoyis.inbound_canonical(text,text),invoyis.inbound_part_publish(text,text,text),invoyis.inbound_finish(text,boolean) TO invoyis_archive_runtime;
 END IF;
END $$;
