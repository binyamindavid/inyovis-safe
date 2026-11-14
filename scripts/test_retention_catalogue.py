import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("retention_catalogue", Path(__file__).with_name("retention-catalogue.py"))
catalogue = importlib.util.module_from_spec(spec)
spec.loader.exec_module(catalogue)


class CatalogueValidationTest(unittest.TestCase):
    def manifest(self, active, exceptions):
        return {
            "version": "synthetic-validation-test",
            "rules": [{
                "id": "synthetic-bill", "version": 1, "jurisdiction": "FR",
                "entityScope": "person", "documentScope": "bill",
                "effectiveFrom": "1990-01-01", "effectiveUntil": None,
                "selectionDate": "issue_date", "source": "https://www.service-public.gouv.fr/particuliers/vosdroits/F19134",
                "trigger": "issue_date", "years": 5, "active": active,
                "exceptions": exceptions,
                "validation": {"status": "approved", "scope_verified": True,
                               "starting_event_verified": True, "reviewer": "test",
                               "reviewed_at": "2026-11-14", "evidence": "synthetic test"},
            }], "coverage": [],
        }

    def check(self, data):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "release.json"
            path.write_text(json.dumps(data))
            return catalogue.validate(path)

    def test_active_exception_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "active rules with exceptions"):
            self.check(self.manifest(True, ["ongoing dispute"]))

    def test_exception_can_be_staged_in_inactive_draft(self):
        self.assertEqual(self.check(self.manifest(False, ["ongoing dispute"]))["rules"][0]["exceptions"], ["ongoing dispute"])


if __name__ == "__main__":
    unittest.main()
