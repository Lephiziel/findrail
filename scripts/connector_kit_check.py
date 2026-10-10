#!/usr/bin/env python3
"""Validate the public connector kit and its separate-module example."""

import hashlib
import os
import pathlib
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
EXAMPLE = ROOT / "examples" / "connectors" / "catalog"
PROTECTED = (
    "go.mod", "go.sum", "internal/evaluation", "examples/evaluation",
    "docs/search-evaluation.md", "VERSION", "build", "release",
    ".github/workflows/alpha-release.yml",
    ".github/workflows/package-rehearsal.yml",
)
EXTERNAL_CONSUMER_TEST = r'''package consumer
import (
 "context"
 "testing"
 "github.com/Lephiziel/findrail/pkg/connector"
 "github.com/Lephiziel/findrail/pkg/connector/conformance"
)
type adapter struct{}
func(adapter) Source() connector.Source { return connector.Source{ID:"source",Kind:"test",Name:"fixture"} }
func(adapter) Scan(ctx context.Context, emit func(connector.Document) error)(connector.Report,error) {
 d:=connector.Document{ID:"doc",SourceID:"source",Title:"title",URI:"https://example.test/doc",Path:"doc.txt",Content:"synthetic",Hash:"body-v1",MediaType:"text/plain",SizeBytes:9}
 if err:=emit(d);err!=nil{return connector.Report{},err};return connector.Report{Seen:1},nil
}
func TestExternalConsumer(t *testing.T) {
 if _,_,err:=conformance.Observe(context.Background(),adapter{},conformance.DefaultOptions());err!=nil{t.Fatal(err)}
 if err:=conformance.CheckCallbackError(context.Background(),adapter{},conformance.DefaultOptions());err!=nil{t.Fatal("consumer callback failure was not propagated")}
}
'''


def run(args, cwd, env, timeout=180):
    subprocess.run(args, cwd=cwd, env=env, check=True, timeout=timeout)


def snapshot(relative):
    target = ROOT / relative
    if target.is_file():
        return hashlib.sha256(target.read_bytes()).digest()
    if target.is_dir():
        files = sorted(p for p in target.rglob("*") if p.is_file() and not p.is_symlink())
        return tuple((p.relative_to(target).as_posix(), hashlib.sha256(p.read_bytes()).digest()) for p in files)
    return None


def validate():
    env = os.environ.copy()
    env["GOWORK"] = "off"
    run(["go", "test", "./pkg/connector/conformance"], ROOT, env)
    run(["go", "vet", "./pkg/connector/conformance"], ROOT, env)
    run(["go", "test", "-race", "./pkg/connector/conformance"], ROOT, env)
    run(["go", "mod", "verify"], ROOT, env)
    run(["go", "test", "./..."], EXAMPLE, env)
    run(["go", "vet", "./..."], EXAMPLE, env)
    run(["go", "test", "-race", "./..."], EXAMPLE, env)

    forbidden = (
        "github.com/Lephiziel/findrail/internal/", "modernc.org/sqlite",
        "modelcontextprotocol", "ledongthuc/pdf",
    )
    modules = (
        (ROOT, ["./pkg/connector/conformance"]),
        (EXAMPLE, [".", "./cmd/catalog-demo", "./cmd/catalog-scan"]),
    )
    for module, packages in modules:
        result = subprocess.run(
            ["go", "list", "-deps", *packages], cwd=module, env=env,
            check=True, timeout=180, text=True, capture_output=True,
        )
        disallowed = [p for p in result.stdout.splitlines() if any(x in p for x in forbidden)]
        if disallowed:
            raise SystemExit(f"forbidden dependency in {module}: {disallowed}")

    with tempfile.TemporaryDirectory(prefix="findrail-connector-kit-") as temp_dir:
        temp = pathlib.Path(temp_dir)
        consumer = temp / "consumer"
        consumer.mkdir()
        (consumer / "go.mod").write_text(
            "module example.com/consumer\n\ngo 1.26.0\n\n"
            "require github.com/Lephiziel/findrail v0.0.0\n\n"
            f"replace github.com/Lephiziel/findrail => {ROOT}\n",
            encoding="utf-8",
        )
        (consumer / "consumer_test.go").write_text(EXTERNAL_CONSUMER_TEST, encoding="utf-8")
        run(["go", "test", "-race", "./..."], consumer, env)

        for goos, suffix in (("linux", ""), ("windows", ".exe"), ("darwin", "")):
            build_env = env.copy()
            build_env.update({"GOOS": goos, "GOARCH": "amd64", "CGO_ENABLED": "0"})
            run(["go", "build", "-o", str(temp / f"demo-{goos}{suffix}"), "./cmd/catalog-demo"], EXAMPLE, build_env)
            run(["go", "build", "-o", str(temp / f"scan-{goos}{suffix}"), "./cmd/catalog-scan"], EXAMPLE, build_env)

        run(["go", "run", "./cmd/catalog-demo"], EXAMPLE, env, timeout=60)
        run(["go", "run", "./cmd/catalog-scan", "-h"], EXAMPLE, env, timeout=60)
        workspace = temp / "go.work"
        workspace.write_text(f"go 1.26.0\nuse {ROOT}\nuse {EXAMPLE}\n", encoding="utf-8")
        host_env = env.copy()
        host_env["GOWORK"] = str(workspace)
        run(["go", "test", "-race", "-tags=connector_kit", "./internal/connectorcheck"], ROOT, host_env)


def main():
    before = {path: snapshot(path) for path in PROTECTED}
    try:
        validate()
    finally:
        after = {path: snapshot(path) for path in PROTECTED}
        if before != after:
            raise SystemExit("protected/release paths changed during connector kit validation")


if __name__ == "__main__":
    main()
