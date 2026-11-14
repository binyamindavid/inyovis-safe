#!/usr/bin/env python3
"""Validate, stage and publish an immutable retention rule-set release."""

import argparse
import datetime as dt
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlparse


def fail(message):
    raise ValueError(message)


def date(value, name):
    try:
        if dt.date.fromisoformat(value).isoformat() != value:
            fail(f"{name}: expected YYYY-MM-DD")
    except (TypeError, ValueError):
        fail(f"{name}: expected YYYY-MM-DD")


def nonempty(value, name):
    if not isinstance(value, str) or not value.strip():
        fail(f"{name}: required text")


def validate(path):
    data = json.loads(Path(path).read_text())
    if set(data) != {"version", "rules", "coverage"}:
        fail("manifest requires exactly version, rules and coverage")
    nonempty(data["version"], "version")
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9._-]{0,99}", data["version"]):
        fail("version: invalid characters")
    if not isinstance(data["rules"], list) or not isinstance(data["coverage"], list):
        fail("rules and coverage must be arrays")
    ids = set()
    for rule in data["rules"]:
        required = {"id", "version", "jurisdiction", "entityScope", "documentScope", "effectiveFrom", "effectiveUntil", "selectionDate", "source", "trigger", "years", "active", "exceptions", "validation"}
        if set(rule) != required:
            fail(f"rule fields differ from schema: {rule.get('id')}")
        for field in ("id", "entityScope", "documentScope"):
            nonempty(rule[field], field)
        if rule["jurisdiction"] not in ("FR", "IE", "IL"):
            fail("jurisdiction must be FR, IE or IL")
        if rule["entityScope"] not in ("person", "company"):
            fail("entityScope must be person or company")
        if rule["documentScope"] not in ("invoice", "bill", "credit_note", "lease", "court_correspondence", "other"):
            fail("documentScope must be a confirmed document type")
        if type(rule["version"]) is not int or rule["version"] < 1 or (rule["id"], rule["version"]) in ids:
            fail("duplicate or invalid rule identity")
        ids.add((rule["id"], rule["version"]))
        date(rule["effectiveFrom"], "effectiveFrom")
        if rule["effectiveUntil"] is not None:
            date(rule["effectiveUntil"], "effectiveUntil")
            if rule["effectiveUntil"] <= rule["effectiveFrom"]:
                fail("effectiveUntil must follow effectiveFrom")
        if rule["selectionDate"] not in ("issue_date", "financial_year_end") or rule["trigger"] not in ("issue_date", "financial_year_end", "filing_date"):
            fail("unsupported date basis or starting event")
        if type(rule["years"]) is not int or rule["years"] < 0 or type(rule["active"]) is not bool:
            fail("invalid duration or active flag")
        if not isinstance(rule["exceptions"], list) or not all(isinstance(x, str) and x.strip() for x in rule["exceptions"]):
            fail("exceptions must be an array of explanations; use [] when none are known")
        source = urlparse(rule["source"])
        if source.scheme != "https" or not source.netloc:
            fail("official source must be an HTTPS URL")
        record = rule["validation"]
        if set(record) != {"status", "scope_verified", "starting_event_verified", "reviewer", "reviewed_at", "evidence"}:
            fail("validation record fields differ from schema")
        if record["status"] not in ("approved", "pending"):
            fail("validation status must be approved or pending")
        for field in ("scope_verified", "starting_event_verified"):
            if type(record[field]) is not bool:
                fail(f"{field} must be boolean")
        for field in ("reviewer", "evidence"):
            nonempty(record[field], field)
        date(record["reviewed_at"], "reviewed_at")
        if rule["active"] and (record["status"] != "approved" or not record["scope_verified"] or not record["starting_event_verified"]):
            fail("active rule requires approved scope and starting event")
    coverage_ids = set()
    for coverage in data["coverage"]:
        required = {"id", "jurisdiction", "entityScope", "documentScope", "effectiveFrom", "effectiveUntil", "selectionDate", "status", "source"}
        if set(coverage) != required:
            fail("coverage fields differ from schema")
        nonempty(coverage["id"], "coverage id")
        if coverage["id"] in coverage_ids:
            fail("duplicate coverage id")
        coverage_ids.add(coverage["id"])
        if coverage["jurisdiction"] not in ("FR", "IE", "IL") or coverage["entityScope"] not in ("person", "company"):
            fail("invalid coverage scope")
        if coverage["documentScope"] not in ("invoice", "bill", "credit_note", "lease", "court_correspondence", "other"):
            fail("invalid coverage document type")
        date(coverage["effectiveFrom"], "coverage effectiveFrom")
        if coverage["effectiveUntil"] is not None:
            date(coverage["effectiveUntil"], "coverage effectiveUntil")
            if coverage["effectiveUntil"] <= coverage["effectiveFrom"]:
                fail("invalid coverage window")
        if coverage["selectionDate"] not in ("issue_date", "financial_year_end") or coverage["status"] not in ("complete", "excluded"):
            fail("invalid coverage date or status")
        source = urlparse(coverage["source"])
        if source.scheme != "https" or not source.netloc:
            fail("coverage requires an HTTPS source")
    return data


def sql(value):
    if value is None:
        return "NULL"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, (dict, list)):
        return sql(json.dumps(value, ensure_ascii=False, sort_keys=True)) + "::jsonb"
    return "'" + value.replace("'", "''") + "'"


def query(database_url, body):
    result = subprocess.run(["psql", database_url, "-X", "-v", "ON_ERROR_STOP=1", "-q"], input=body, text=True)
    if result.returncode:
        fail("database rejected catalogue operation")


def stage(data, database_url):
    version = sql(data["version"])
    statements = ["BEGIN;", f"INSERT INTO invoyis.retention_rule_sets(version) VALUES ({version});"]
    for r in data["rules"]:
        values = [r[k] for k in ("id", "version", "jurisdiction", "entityScope", "documentScope", "effectiveFrom", "effectiveUntil", "selectionDate", "source", "trigger", "years", "active", "exceptions", "validation")]
        statements.append("INSERT INTO invoyis.retention_rule_versions(rule_id,version,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,source_reference,trigger_event,calendar_years,active,exceptions,validation_record) VALUES (" + ",".join([sql(values[0]), sql(values[1]), version] + [sql(v) for v in values[2:]]) + ");")
    for c in data["coverage"]:
        values = [c[k] for k in ("id", "jurisdiction", "entityScope", "documentScope", "effectiveFrom", "effectiveUntil", "selectionDate", "status", "source")]
        statements.append("INSERT INTO invoyis.retention_coverage(coverage_id,rule_set_version,jurisdiction,entity_scope,document_scope,effective_from,effective_until,selection_date,status,source_reference) VALUES (" + ",".join([sql(values[0]), version] + [sql(v) for v in values[1:]]) + ");")
    statements.append("COMMIT;")
    query(database_url, "\n".join(statements))


def publish(version, database_url):
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9._-]{0,99}", version):
        fail("invalid version")
    name = sql(version)
    query(database_url, f"""BEGIN;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM invoyis.retention_rule_sets WHERE version={name}) THEN
  RAISE EXCEPTION 'unknown catalogue release';
 END IF;
 IF EXISTS (SELECT 1 FROM invoyis.retention_rule_versions WHERE rule_set_version={name} AND active
  AND (validation_record->>'status' IS DISTINCT FROM 'approved'
   OR validation_record->>'scope_verified' IS DISTINCT FROM 'true'
   OR validation_record->>'starting_event_verified' IS DISTINCT FROM 'true')) THEN
  RAISE EXCEPTION 'rule scope or starting event has not been approved';
 END IF;
END $$;
UPDATE invoyis.retention_active_rule_set SET version={name} WHERE singleton=true AND version IS DISTINCT FROM {name};
COMMIT;""")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("validate", "stage", "publish"))
    parser.add_argument("manifest", help="JSON manifest for validate/stage; release version for publish")
    args = parser.parse_args()
    try:
        if args.action == "publish":
            publish(args.manifest, os.environ["MIGRATION_DATABASE_URL"])
        else:
            data = validate(args.manifest)
            if args.action == "stage":
                stage(data, os.environ["MIGRATION_DATABASE_URL"])
        print(f"{args.action} complete")
    except (ValueError, KeyError, OSError, json.JSONDecodeError) as error:
        print(f"retention catalogue: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
