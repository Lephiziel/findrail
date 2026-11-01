#!/usr/bin/env python3
"""Bounded CLI smoke for root ignore decisions; uses only a private temp tree."""
import json, os, pathlib, socket, subprocess, sys, tempfile, time, urllib.parse, urllib.request

binary = pathlib.Path(sys.argv[1]).resolve()
if not binary.exists() and pathlib.Path(str(binary) + ".exe").exists():
    binary = pathlib.Path(str(binary) + ".exe")
with tempfile.TemporaryDirectory(prefix="findrail ignore ") as temp:
    root = pathlib.Path(temp) / "notes with spaces"
    data = pathlib.Path(temp) / "private index"
    root.mkdir()
    (root / "skip.md").write_text("uniquetoken hidden", encoding="utf-8")
    (root / "keep.md").write_text("uniquetoken retained", encoding="utf-8")
    (root / ".findrailignore").write_text("skip.md\n", encoding="utf-8")
    args = [str(binary), "explain-path", str(root), "--path", "skip.md", "--path", "keep.md", "--json"]
    result = subprocess.run(args, capture_output=True, text=True, timeout=10, check=True)
    payload = json.loads(result.stdout)
    assert payload["version"] == 1 and payload["policy_present"]
    assert [row["status"] for row in payload["results"]] == ["excluded_user_rule", "eligible_by_path_policy"]
    def run(*argv, success=True):
        completed = subprocess.run([str(binary), *map(str, argv)], capture_output=True, text=True, timeout=20)
        assert (completed.returncode == 0) == success, completed.stderr
        return completed
    run("index", "--data-dir", data, root)
    assert "1 matches" in run("search", "--data-dir", data, "retained").stdout
    assert "0 matches" in run("search", "--data-dir", data, "hidden").stdout
    (root / ".findrailignore").write_text("[invalid\n", encoding="utf-8")
    run("index", "--data-dir", data, root, success=False)
    assert "1 matches" in run("search", "--data-dir", data, "retained").stdout
    (root / ".findrailignore").write_text("", encoding="utf-8")
    run("index", "--data-dir", data, root)
    assert "1 matches" in run("search", "--data-dir", data, "hidden").stdout
    (root / ".findrailignore").write_text("skip.md\n!skip.md\n", encoding="utf-8")
    run("index", "--data-dir", data, root)
    assert "1 matches" in run("search", "--data-dir", data, "hidden").stdout
    bad = subprocess.run([str(binary), "explain-path", str(root), "--path", "../secret"], capture_output=True, timeout=10)
    assert bad.returncode != 0

    # Native watcher lifecycle: root policy create/atomic replace/removal,
    # failed scan report retention, newly traversable subtree, forget and stop.
    watch_root = pathlib.Path(temp) / "watched notes"
    watch_data = pathlib.Path(temp) / "watch index"
    generated = watch_root / "generated" / "nested"
    generated.mkdir(parents=True)
    (watch_root / "stable.md").write_text("watcher stable marker", encoding="utf-8")
    (generated / "old.md").write_text("watcher old marker", encoding="utf-8")
    run("index", "--data-dir", watch_data, watch_root)
    sources = json.loads(run("sources", "--data-dir", watch_data, "--json").stdout)["sources"]
    source_id = sources[0]["id"]
    def report_id():
        payload = json.loads(run("source-report", "--data-dir", watch_data, "--source", source_id, "--json").stdout)
        return payload.get("report", {}).get("report_id")
    def count(query):
        return json.loads(run("search", "--data-dir", watch_data, "--json", query).stdout)["total"]
    baseline = report_id()
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    def start_server():
        return subprocess.Popen([str(binary), "serve", "--addr", f"127.0.0.1:{port}", "--data-dir", str(watch_data), "--sync-interval", "1h"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    server = start_server()
    def sync_status():
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/api/v1/sync", timeout=2) as response:
            return json.loads(response.read())
    def get_json(path):
        with urllib.request.urlopen(f"http://127.0.0.1:{port}{path}", timeout=2) as response:
            return json.loads(response.read())
    def wait_for(check, label, timeout=12):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if server.poll() is not None:
                raise AssertionError(f"server exited during {label}: {server.stderr.read()}")
            try:
                if check():
                    return
            except (OSError, ValueError, KeyError, IndexError):
                pass
            time.sleep(0.05)
        raise AssertionError(f"native watcher did not complete {label} before deadline")
    def atomic_policy(contents):
        temporary = watch_root / ".findrailignore.editor-temp"
        temporary.write_text(contents, encoding="utf-8")
        os.replace(temporary, watch_root / ".findrailignore")
    try:
        wait_for(lambda: sync_status().get("enabled"), "sync startup")
        wait_for(lambda: report_id() != baseline, "initial reconciliation")
        evidence_search = get_json("/api/v1/search?" + urllib.parse.urlencode({"q": "watcher stable"}))
        assert evidence_search["total"] == 1
        evidence = get_json("/api/v1/documents/" + evidence_search["results"][0]["id"])
        assert evidence["path"] == "stable.md" and "watcher stable marker" in evidence["text"]
        atomic_policy("generated/\n")
        wait_for(lambda: count("old") == 0, "policy create and pruning")
        committed = report_id()
        (generated / "new.md").write_text("watcher rediscovered marker", encoding="utf-8")
        time.sleep(0.2)
        assert count("rediscovered") == 0, "excluded directory was traversed"
        atomic_policy("[malformed\n")
        wait_for(lambda: any(item.get("state") == "error" for item in sync_status().get("sources", [])), "malformed policy failure")
        assert report_id() == committed, "failed scan replaced durable report"
        assert count("stable") == 1 and count("old") == 0, "failed scan changed prior searchable snapshot"
        atomic_policy("!generated/\n")
        wait_for(lambda: count("rediscovered") == 1, "policy repair and pruned subtree discovery")
        assert report_id() != committed
        atomic_policy("old.md\n")
        wait_for(lambda: count("old") == 0 and count("rediscovered") == 1, "rule precedence after repair")
        before_restart = report_id()
        server.terminate()
        server.wait(timeout=5)
        server = start_server()
        wait_for(lambda: sync_status().get("enabled"), "watcher restart")
        wait_for(lambda: report_id() != before_restart, "restart reconciliation")
        assert count("old") == 0 and count("rediscovered") == 1, "restart lost root ignore behavior"
        (watch_root / ".findrailignore").unlink()
        wait_for(lambda: count("old") == 1, "policy removal reindex")
        run("forget", "--data-dir", watch_data, source_id)
        (watch_root / "stable.md").write_text("forgotten never returns", encoding="utf-8")
        wait_for(lambda: all(item.get("source_id") != source_id for item in sync_status().get("sources", [])), "forgotten worker shutdown")
        assert count("forgotten") == 0, "forgotten source was re-registered"
    finally:
        server.terminate()
        try:
            server.wait(timeout=5)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait(timeout=5)
print("ignore policy explanation smoke passed")
