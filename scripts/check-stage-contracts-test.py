"""Isolated tests for dependency and actual contract-binding checks."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

CHECK = Path(__file__).with_name("check-stage-contracts.py")


def reopen_block(owners):
    """Serialize a reopen block naming the stages currently under rework."""
    entries = {"activeReopen": None, "suspendedReopen": None}
    for field, stage in owners.items():
        entries[field] = {"ownerStage": stage, "ownerPreviousStatus": "DONE",
                          "previousCurrentStage": None,
                          "reopenedAt": "2026-10-17T00:13:05+08:00",
                          "successorBaseline": {}}
    return "```json\n" + json.dumps(
        {"schemaVersion": "kowa-stage-reopen.v1", **entries}) + "\n```\n"


class ContractGateTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.states = {"backend": "DONE", "frontend": "NOT_STARTED"}
        self.deps = {"backend": "无", "frontend": "backend:S00"}
        self.declared = "backend:S00"
        self.reopen = None
        self.schema = {"title": "kowa.fixture.v1", "$defs": {"View": {"type": "object"}}}
        self.manifest = {"contract": "kowa.fixture.v1", "owner": "backend:S00",
                         "schema": "schema.json", "operations": {"view": "http"}}
        self.req = {"owner": "backend:S00", "contract": "kowa.fixture.v1",
                    "operation": "view", "manifest": "api/fixture/v1/delivery.json",
                    "sha256": None, "level": "http", "evidence": None}

    def write(self):
        directory = self.root / "api/fixture/v1"
        directory.mkdir(parents=True, exist_ok=True)
        data = json.dumps(self.schema).encode()
        (directory / "schema.json").write_bytes(data)
        self.manifest["schemaSha256"] = hashlib.sha256(data).hexdigest()
        data = json.dumps(self.manifest).encode()
        (directory / "delivery.json").write_bytes(data)
        if self.req["sha256"] == "freeze":
            self.req["sha256"] = hashlib.sha256(data).hexdigest()
        for lane, name in [("backend", "Kowa后端设计"), ("frontend", "Kowa前端设计")]:
            directory = self.root / "doc" / name
            (directory / "stage").mkdir(parents=True, exist_ok=True)
            table = f"| S00 | {self.states[lane]} | {self.deps[lane]} | fixture |\n"
            if lane == "backend" and self.reopen:
                table += reopen_block(self.reopen)
            (directory / "总体设计与进度.md").write_text(table)
            body = "- 本端前置阶段：无。\n- 跨端阶段：无。\n"
            if lane == "frontend":
                body = "- 本端前置阶段：无。\n- 跨端阶段：" + self.declared + "。\n"
                body += "```json\n" + json.dumps({"schemaVersion": "kowa-stage-consumption.v1",
                                                          "requires": [self.req]}) + "\n```\n"
            (directory / "stage/S00-fixture.md").write_text(body)
        (self.root / "evidence.md").write_text("fixture evidence")

    def check(self, diagnostic=None):
        self.write()
        result = subprocess.run(["python3", str(CHECK), str(self.root)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 1 if diagnostic else 0, result.stdout + result.stderr)
        if diagnostic:
            self.assertIn(diagnostic, result.stdout + result.stderr)

    def freeze(self):
        self.req.update(sha256="freeze", evidence="evidence.md")
        self.states["frontend"] = "IN_PROGRESS"

    def test_planned(self):
        self.check()

    def test_verified(self):
        self.freeze()
        self.check()

    def test_cycle(self):
        self.deps["backend"] = "frontend:S00"
        self.check("dependency cycle")

    def test_dependency_drift(self):
        self.declared = "backend:S99"
        self.check("dependency declaration differs")

    def test_owner_not_dependency(self):
        self.req["owner"] = "frontend:S00"
        self.check("owner not a declared dependency")

    def test_unfrozen_active(self):
        self.states["frontend"] = "IN_PROGRESS"
        self.check("unfrozen requirement")

    def test_missing_manifest(self):
        self.freeze()
        self.req["manifest"] = "api/missing.json"
        self.check("manifest missing")

    def test_wrong_digest(self):
        self.freeze()
        self.req["sha256"] = "0" * 64
        self.check("manifest digest differs")

    def test_wrong_owner(self):
        self.freeze()
        self.manifest["owner"] = "backend:S99"
        self.check("manifest identity differs")

    def test_missing_operation(self):
        self.freeze()
        self.req["operation"] = "session.bootstrap"
        self.check("operation unavailable")

    def test_lower_delivery_level(self):
        self.freeze()
        self.manifest["operations"]["view"] = "http-seam"
        self.check("operation unavailable")

    def structured(self, layer="http", **overrides):
        operation = {"method": "GET", "path": "/api/v1/fixture", "layer": layer,
                     "authorization": "session+member"}
        operation.update(overrides)
        self.manifest["operations"]["view"] = operation

    def test_structured_operation(self):
        self.freeze()
        self.structured()
        self.check()

    def application(self, **overrides):
        operation = {"layer": "application", "authorization": "closure use case"}
        operation.update(overrides)
        self.manifest["operations"]["view"] = operation

    def test_structured_layer_mismatch(self):
        self.freeze()
        self.application()
        self.check("operation unavailable")

    def test_application_operation(self):
        self.freeze()
        self.req["level"] = "application"
        self.application()
        self.check()

    def test_application_requires_authorization(self):
        self.freeze()
        self.application(authorization="")
        self.check("invalid delivery operation field")

    def test_application_rejects_http_fields(self):
        self.freeze()
        self.application(method="GET", path="/api/v1/fixture")
        self.check("invalid delivery operation entry")

    def test_application_unknown_layer(self):
        self.freeze()
        self.application(layer="journey")
        self.check("invalid delivery operation layer")

    def test_structured_missing_field(self):
        self.freeze()
        self.manifest["operations"]["view"] = {"method": "GET", "path": "/api/v1/fixture", "layer": "http"}
        self.check("invalid delivery operation entry")

    def test_structured_unknown_field(self):
        self.freeze()
        self.structured(description="fixture")
        self.check("invalid delivery operation entry")

    def test_structured_invalid_method(self):
        self.freeze()
        self.structured(method="FETCH")
        self.check("invalid delivery operation method")

    def test_unfinished_owner(self):
        self.freeze()
        self.states["backend"] = "IN_PROGRESS"
        self.check("owner not DONE")

    def test_reopened_owner_keeps_the_frozen_consumer(self):
        """A reopen names the owner under rework, so the DONE consumer keeps its pin.

        The reopen block is what makes the state legal at all (AGENTS.md: every
        successor keeps the baseline frozen at the reopen instant), so the
        owner-not-DONE condition — an entry condition — is waived exactly for
        the stages that block names.
        """
        self.freeze()
        self.states["backend"] = "REOPENED"
        self.reopen = {"activeReopen": "S00"}
        self.check()

    def test_suspended_owner_keeps_the_frozen_consumer(self):
        """A nested reopen names the earlier owner first and the suspended owner apart."""
        self.freeze()
        self.states["backend"] = "BLOCKED"
        self.reopen = {"activeReopen": "S01", "suspendedReopen": "S00"}
        self.check()

    def test_reopen_exception_requires_the_named_owner(self):
        """The same unfinished owner without a reopen naming it stays rejected."""
        self.freeze()
        self.states["backend"] = "REOPENED"
        self.check("owner not DONE")

    def test_missing_evidence(self):
        self.freeze()
        self.req["evidence"] = "missing.md"
        self.check("evidence missing")

    def test_missing_binding(self):
        self.write()
        p = self.root / "doc/Kowa前端设计/stage/S00-fixture.md"
        p.write_text("- 本端前置阶段：无。\n- 跨端阶段：backend:S00。\n")
        result = subprocess.run(["python3", str(CHECK), str(self.root)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("consumption block missing", result.stdout)

    def test_schema_tamper(self):
        self.freeze()
        self.write()
        p = self.root / "api/fixture/v1/schema.json"
        p.write_text(json.dumps({"title": "changed"}))
        result = subprocess.run(["python3", str(CHECK), str(self.root)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("schema digest differs", result.stdout)

    def test_format_independent(self):
        self.freeze()
        self.write()
        p = self.root / "doc/Kowa前端设计/stage/S00-fixture.md"
        p.write_text(p.read_text().replace('"requires": [', '"requires":\n['))
        result = subprocess.run(["python3", str(CHECK), str(self.root)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
