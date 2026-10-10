#!/usr/bin/env python3
"""Compiled synthetic journey for committed source indexing reports."""
import json
import pathlib
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

# Every HTTP request in this journey is loopback-only; ignore ambient proxies.
urllib.request.install_opener(urllib.request.build_opener(urllib.request.ProxyHandler({})))

binary = pathlib.Path(sys.argv[1]).resolve()
with tempfile.TemporaryDirectory(prefix="findrail-diagnostics-") as temporary:
    base = pathlib.Path(temporary)
    data, source = base / "index", base / "source"
    missing_index=base/"must-not-be-created"
    source.mkdir()
    (source / "note.md").write_text("synthetic committed note", encoding="utf-8")
    (source / "picture.png").write_bytes(b"synthetic unsupported image")
    (source / "secret-token.txt").write_text("must never be sampled", encoding="utf-8")
    (source / "~$private_key.docx").write_bytes(b"synthetic Word lock file")
    docx=source/"synthetic.docx"
    with zipfile.ZipFile(docx,"w") as archive:
        archive.writestr("[Content_Types].xml",'<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>')
        archive.writestr("_rels/.rels",'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>')
        archive.writestr("word/document.xml",'<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>syntheticdocxmarker</w:t></w:r></w:p></w:body></w:document>')
    # Exercise an inventory much larger than the report's retained sample.
    for index in range(240):
        (source / f"synthetic-{index:04d}.png").write_bytes(b"synthetic unsupported image")
    hidden = source / ".secret"
    hidden.mkdir()
    (hidden / "not-enumerated.md").write_text("hidden", encoding="utf-8")

    def run(*args, ok=True):
        result = subprocess.run([str(binary), *map(str, args)], text=True, capture_output=True)
        if ok and result.returncode:
            raise AssertionError(f"command failed: {args}: {result.stderr}")
        if not ok and not result.returncode:
            raise AssertionError(f"command unexpectedly succeeded: {args}")
        return result

    indexed = json.loads(run("index", "--data-dir", data, "--report", "--json", source).stdout)
    assert indexed["report"]["committed"] and "examples" not in indexed["report"]
    sid = indexed["source"]["id"]
    summary_args = ("source-report", "--data-dir", data, "--source", sid, "--json")
    summary = json.loads(run(*summary_args).stdout)
    assert summary["available"] is True
    assert "picture.png" not in json.dumps(summary)
    details = json.loads(run(*summary_args, "--show-paths").stdout)
    report = details["report"]
    assert report["committed"] and report["complete"]
    assert report["indexed_documents"] == report["updated_documents"] + report["unchanged_documents"]
    assert report["skipped_files"] >= 243 and report["pruned_directories"] >= 1
    assert report["observed_files_known"] and report["observed_directories_known"] and report["observed_entries_known"]
    assert report["observed_entries"]==report["observed_files"]+report["observed_directories"]
    reason_counts={item["code"]:item["count"] for item in report["reasons"]}
    assert reason_counts.get("office_lock_file")==1 and reason_counts.get("hidden_entry")==1
    assert indexed["skipped"]==report["skipped_files"]+report["skipped_entries"]+report["pruned_directories"]
    assert report["redacted_samples"]>=3
    assert len(report.get("examples", [])) <= 200 and report["examples_omitted"] > 0
    encoded = json.dumps(report, ensure_ascii=False).encode()
    assert len(encoded) <= 65536
    paths = [item["path"] for item in report.get("examples", [])]
    assert "picture.png" in paths and "secret-token.txt" not in paths and "~$private_key.docx" not in paths and ".secret" not in paths
    # A new process reads the same committed record without scanning the source.
    restarted = json.loads(run(*summary_args).stdout)
    assert restarted["report"]["report_id"] == report["id"]

    # A fatal PDF extraction returns a stable incomplete attempt, leaks no
    # source root/path by default, and preserves the prior committed report.
    (source/"broken.pdf").write_bytes(b"%PDF-invalid")
    failed=run("index","--data-dir",data,"--json",source,ok=False)
    failure=json.loads(failed.stdout)
    assert failure["committed"] is False and failure["failure"]["code"]=="scan_failed",failure
    assert failure["result"]["attempt"]["committed"] is False and failure["result"]["attempt"]["failure_code"]=="scan_failed",failure
    assert str(source) not in failed.stdout and "broken.pdf" not in failed.stdout
    failed_details=run("index","--data-dir",data,"--json","--show-paths",source,ok=False)
    assert json.loads(failed_details.stdout)["result"]["attempt"]["failure_path"]=="broken.pdf"
    assert json.loads(run(*summary_args).stdout)["report"]["report_id"]==report["id"]
    (source/"broken.pdf").unlink()
    missing=run("source-report","--data-dir",missing_index,"--source",sid,"--json",ok=False)
    assert not missing_index.exists()

    # Use the actual start manager to atomically Configure DOCX and observe a
    # watcher refresh. Polling is bounded and only uses loopback.
    with socket.socket() as probe:
        probe.bind(("127.0.0.1",0));port=probe.getsockname()[1]
    process=subprocess.Popen([str(binary),"start","--no-open","--data-dir",str(data),"--addr",f"127.0.0.1:{port}"],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    base_url=f"http://127.0.0.1:{port}"
    def get(url):
        with urllib.request.urlopen(base_url+url,timeout=3) as response:return json.load(response)
    def mutate(url,token,payload=None):
        request=urllib.request.Request(base_url+url,data=None if payload is None else json.dumps(payload).encode(),method="POST",headers={"Origin":base_url,"Content-Type":"application/json","Sec-Fetch-Site":"same-origin","X-Findrail-Token":token})
        with urllib.request.urlopen(request,timeout=4) as response:return json.load(response) if response.status!=204 else {}
    def eventually(fn,timeout=15):
        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            try:
                if fn():return
            except (urllib.error.URLError,TimeoutError,ConnectionError):pass
            time.sleep(.05)
        raise AssertionError("diagnostics smoke timed out")
    try:
        eventually(lambda:get("/healthz").get("status")=="ok")
        eventually(lambda:(lambda statuses:len(statuses)==1 and all(s["state"]=="idle" for s in statuses))(get("/api/v1/sync")["sources"]))
        durable=json.loads(run(*summary_args).stdout)["report"]
        http_summary=get(f"/api/v1/sources/{sid}/report")
        http_details=get(f"/api/v1/sources/{sid}/report?include_paths=true")
        assert http_summary["report"]["report_id"]==durable["report_id"]==http_details["report"]["id"]
        assert "picture.png" not in json.dumps(http_summary) and "picture.png" in json.dumps(http_details)
        token=get("/api/v1/session")["token"]
        cancel_root=base/"cancel-inventory";cancel_root.mkdir()
        for index in range(10000):(cancel_root/f"skip-{index:05d}.png").touch()
        accepted=mutate("/api/v1/sources",token,{"type":"folder","path":str(cancel_root)})["job"]
        mutate("/api/v1/jobs/"+accepted["id"]+"/cancel",token)
        canceled={};cancel_deadline=time.monotonic()+10
        while time.monotonic()<cancel_deadline:
            canceled=get("/api/v1/jobs/"+accepted["id"])
            if canceled["status"] in ("succeeded","failed","canceled"):break
            time.sleep(.02)
        assert canceled["status"]=="canceled" and canceled["error_code"]=="canceled" and canceled["progress"]["failure_code"]=="canceled" and not canceled["progress"]["committed"] and canceled["progress"]["partial"],canceled
        assert all(item["root"]!=str(cancel_root) for item in get("/api/v1/sources")["sources"])

        accepted=mutate(f"/api/v1/sources/{sid}/configure",token,{"max_docx_bytes":0})["job"]
        job={}
        # Keep the loop straightforward for Python versions used in CI.
        deadline=time.monotonic()+20
        while time.monotonic()<deadline:
            job=get("/api/v1/jobs/"+accepted["id"])
            if job["status"] in ("succeeded","failed","canceled"):break
            time.sleep(.05)
        assert job["status"]=="succeeded" and job["progress"]["committed"] and job["progress"]["report_id"],job
        configured_report=json.loads(run(*summary_args).stdout)["report"]
        assert configured_report["operation"]=="configure" and any(r["code"]=="format_disabled" for r in configured_report["reasons"]),configured_report
        (source/"note.md").write_text("synthetic committed note watcherfresh",encoding="utf-8")
        eventually(lambda:get("/api/v1/search?q=watcherfresh")["total"]==1)
        watcher_report=json.loads(run(*summary_args).stdout)["report"]
        assert watcher_report["operation"]=="refresh" and watcher_report["report_id"]!=configured_report["report_id"],watcher_report

        archive=base/"source.findrail.zip"
        run("export-source","--data-dir",data,"--source",sid,"--output",archive,"--json")
        imported=json.loads(run("import-source","--data-dir",data,"--name","Frozen diagnostics fixture","--json",archive).stdout)
        archive_id=imported["source_id"]
        archived_report=json.loads(run("source-report","--data-dir",data,"--source",archive_id,"--json").stdout)
        assert archived_report["available"] is False and archived_report["availability"]=="frozen_origin_report_not_in_portable_v1"
        assert get(f"/api/v1/sources/{archive_id}/report")["availability"]=="frozen_origin_report_not_in_portable_v1"
        assert any(s["id"]==archive_id and s["kind"]=="archive" for s in get("/api/v1/sources")["sources"])
        assert all(s["source_id"]!=archive_id for s in get("/api/v1/sync")["sources"])
    finally:
        if process.poll() is None:process.terminate()
        process.communicate(timeout=8)
    run("forget","--data-dir",data,sid)
    run(*summary_args,ok=False)
    run("forget","--data-dir",data,archive_id)
    print(f"diagnostics smoke: {report['observed_files']} observed files, {report['observed_entries']} observed entries, {report['skipped_files']} skipped files, {report['skipped_entries']} skipped entries, {report['pruned_directories']} pruned directories, {len(paths)} retained examples, {len(encoded)} report bytes; failure rollback, configure, watcher refresh, archive unavailability, restart and removal passed")
