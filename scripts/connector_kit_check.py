#!/usr/bin/env python3
"""Run connector kit checks with an isolated temporary Go workspace."""
import os
import pathlib
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
EXAMPLE = ROOT / "examples/connectors/catalog"
PROTECTED = ["go.mod", "go.sum", "internal/evaluation", "examples/evaluation", "docs/search-evaluation.md"]

def run(args, cwd, env=None):
    subprocess.run(args, cwd=cwd, env=env, check=True, timeout=180)

def main():
    before = {p: (ROOT / p).read_bytes() if (ROOT / p).is_file() else None for p in PROTECTED}
    env = os.environ.copy()
    env["GOWORK"] = "off"
    run(["go", "test", "./pkg/connector/conformance"], ROOT, env)
    run(["go", "vet", "./pkg/connector/conformance"], ROOT, env)
    run(["go", "test", "-race", "./pkg/connector/conformance"], ROOT, env)
    run(["go", "test", "./..."], EXAMPLE, env)
    run(["go", "vet", "./..."], EXAMPLE, env)
    run(["go", "test", "-race", "./..."], EXAMPLE, env)
    forbidden = ("github.com/Lephiziel/findrail/internal/", "modernc.org/sqlite",
                 "modelcontextprotocol", "ledongthuc/pdf")
    for module, packages in ((ROOT, ["./pkg/connector/conformance"]),
                             (EXAMPLE, [".", "./cmd/catalog-demo", "./cmd/catalog-scan"])):
        deps = subprocess.run(["go", "list", "-deps", *packages], cwd=module,
                              env=env, check=True, timeout=180, text=True,
                              capture_output=True).stdout.splitlines()
        bad = [name for name in deps if any(token in name for token in forbidden)]
        if bad:
            raise SystemExit(f"forbidden dependency in {module}: {bad}")
    with tempfile.TemporaryDirectory(prefix="findrail-connector-kit-") as temp:
        consumer = pathlib.Path(temp) / "consumer"
        consumer.mkdir()
        (consumer / "go.mod").write_text(
            "module example.com/consumer\n\ngo 1.26.0\n\n"
            "require github.com/Lephiziel/findrail v0.0.0\n\n"
            f"replace github.com/Lephiziel/findrail => {ROOT}\n"
        )
        (consumer / "consumer_test.go").write_text('''package consumer
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
 sentinel:=context.Canceled
 _,err:=(adapter{}).Scan(context.Background(),func(connector.Document)error{return sentinel})
 if err==nil {t.Fatal("consumer callback failure was swallowed")}
}
''')
        run(["go", "test", "-race", "./..."], consumer, env)
        for goos, suffix in (("linux", ""), ("windows", ".exe"), ("darwin", "")):
            buildenv = env.copy(); buildenv.update({"GOOS": goos, "GOARCH": "amd64", "CGO_ENABLED": "0"})
            run(["go", "build", "-o", str(pathlib.Path(temp) / f"demo-{goos}{suffix}"), "./cmd/catalog-demo"], EXAMPLE, buildenv)
            run(["go", "build", "-o", str(pathlib.Path(temp) / f"scan-{goos}{suffix}"), "./cmd/catalog-scan"], EXAMPLE, buildenv)
        run(["go", "run", "./cmd/catalog-demo"], EXAMPLE, env)
        work = pathlib.Path(temp) / "go.work"
        work.write_text(f"go 1.26.0\nuse {ROOT}\nuse {EXAMPLE}\n")
        hostenv = env.copy(); hostenv["GOWORK"] = str(work)
        run(["go", "test", "-race", "-tags=connector_kit", "./internal/connectorcheck"], ROOT, hostenv)
    after = {p: (ROOT / p).read_bytes() if (ROOT / p).is_file() else None for p in PROTECTED}
    if before != after:
        raise SystemExit("protected paths changed during check")

if __name__ == "__main__":
    main()
