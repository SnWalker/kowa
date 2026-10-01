"""Check declared dependencies and pinned consumption; no business inference."""
import hashlib
import json
from pathlib import Path
import re
import sys


def check(root):
    errors, stages = [], {}
    for lane, name in [("backend", "Kowa后端设计"), ("frontend", "Kowa前端设计")]:
        directory = root / "doc" / name
        for line in (directory / "总体设计与进度.md").read_text().splitlines():
            cells = [s.strip() for s in line.split("|")]
            if len(cells) < 6 or not re.fullmatch(r"S\d{2}", cells[1]):
                continue
            key = lane + ":" + cells[1]
            deps = re.findall(r"(?:backend:|frontend:)?S\d{2}", cells[3])
            deps = {s if ":" in s else lane + ":" + s for s in deps}
            files = list((directory / "stage").glob(cells[1] + "-*.md"))
            stages[key] = (cells[2], deps, files[0].read_text() if len(files) == 1 else "")
    visiting, visited = set(), set()

    def visit(key):
        if key in visiting:
            errors.append("dependency cycle: " + key)
            return
        if key in visited:
            return
        visiting.add(key)
        for dep in stages[key][1]:
            if dep not in stages:
                errors.append("dependency missing: " + dep)
            else:
                visit(dep)
        visiting.remove(key)
        visited.add(key)

    for key in stages:
        visit(key)

    def path(value):
        if not isinstance(value, str):
            raise ValueError("path must be a repository-relative string")
        p = (root / value).resolve()
        if Path(value).is_absolute() or root not in p.parents:
            raise ValueError("path outside repository")
        return p

    for key, (status, deps, body) in stages.items():
        blocks = []
        for block in re.findall(r"```json\s*\n(.*?)\n```", body, re.S):
            if "kowa-stage-consumption.v1" in block:
                blocks.append(json.loads(block))
        # Historic stages remain auditable without retroactively rewriting evidence.
        if not blocks:
            if status != "DONE" and any(dep.split(":")[0] != key.split(":")[0] for dep in deps):
                errors.append(key + ": consumption block missing")
            continue
        if (len(blocks) != 1 or set(blocks[0]) != {"schemaVersion", "requires"}
                or blocks[0]["schemaVersion"] != "kowa-stage-consumption.v1"):
            errors.append(key + ": invalid consumption block")
            continue
        declared = set()
        for label in ["本端前置阶段", "跨端阶段"]:
            value = re.search(r"^- " + label + r"：(.*)$", body, re.M)
            for dep in re.findall(r"(?:backend:|frontend:)?S\d{2}", value[1] if value else ""):
                declared.add(dep if ":" in dep else key.split(":")[0] + ":" + dep)
        if deps != declared:
            errors.append(key + ": dependency declaration differs")
        requirements = blocks[0]["requires"]
        if not isinstance(requirements, list):
            errors.append(key + ": requirements must be an array")
            continue
        cross_owners = {dep for dep in deps if dep.split(":")[0] != key.split(":")[0]}
        bound_owners = {req.get("owner") for req in requirements if isinstance(req, dict)}
        if not cross_owners.issubset(bound_owners):
            errors.append(key + ": cross-track dependency lacks operation binding")
        for req in requirements:
            try:
                if set(req) != {"owner", "contract", "operation", "manifest", "sha256", "level", "evidence"}:
                    raise ValueError("invalid requirement fields")
                for field in ["owner", "contract", "operation", "manifest", "level"]:
                    if not isinstance(req[field], str) or not req[field]:
                        raise ValueError("empty requirement field: " + field)
                path(req["manifest"])
                if req["level"] not in {"schema", "application", "http-seam", "http", "journey"}:
                    raise ValueError("invalid delivery level")
                if req["owner"] not in deps:
                    raise ValueError("owner not a declared dependency")
                if req["sha256"] is None:
                    if status != "NOT_STARTED":
                        raise ValueError("unfrozen requirement")
                    continue
                if not re.fullmatch(r"[a-f0-9]{64}", req["sha256"]):
                    raise ValueError("invalid manifest digest")
                p = path(req["manifest"])
                if not p.is_file():
                    raise ValueError("manifest missing")
                data = p.read_bytes()
                if hashlib.sha256(data).hexdigest() != req["sha256"]:
                    raise ValueError("manifest digest differs")
                manifest = json.loads(data)
                if (not isinstance(manifest, dict) or set(manifest) != {"contract", "owner", "schema", "schemaSha256", "operations"}
                        or not isinstance(manifest["operations"], dict)):
                    raise ValueError("invalid delivery manifest")
                if manifest["contract"] != req["contract"] or manifest["owner"] != req["owner"]:
                    raise ValueError("manifest identity differs")
                schema = path(str(p.parent.relative_to(root) / manifest["schema"]))
                data = schema.read_bytes()
                if hashlib.sha256(data).hexdigest() != manifest["schemaSha256"]:
                    raise ValueError("schema digest differs")
                if json.loads(data)["title"] != req["contract"]:
                    raise ValueError("schema identity differs")
                if manifest["operations"].get(req["operation"]) != req["level"]:
                    raise ValueError("operation unavailable at required delivery level")
                if stages[req["owner"]][0] != "DONE":
                    raise ValueError("owner not DONE")
                if not path(req["evidence"]).is_file():
                    raise ValueError("evidence missing")
            except (ValueError, KeyError, TypeError, OSError) as error:
                errors.append(key + ": " + str(error))
    return errors


if __name__ == "__main__":
    try:
        failures = check(Path(sys.argv[1]).resolve())
    except (ValueError, OSError, KeyError, TypeError) as error:
        failures = [str(error)]
    for failure in failures:
        print("STAGE_CONTRACT_FAIL: " + failure)
    if not failures:
        print("STAGE_CONTRACT_PASS")
    sys.exit(bool(failures))
