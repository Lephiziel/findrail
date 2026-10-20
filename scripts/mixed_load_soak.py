#!/usr/bin/env python3
"""Bounded mixed-load lifecycle soak against an explicitly prepared binary."""
import argparse
import hashlib
import ipaddress
import json
import math
import os
import platform
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

MAX_OUTPUT = 1 << 20
ACTIVE_CASE = "setup"
HTTP_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))
FAILURES = {"setup": "setup_failed", "process": "process_failed", "http": "http_unexpected",
            "paging": "paging_invariant", "refresh": "refresh_invariant", "cancel": "cancel_invariant",
            "collection": "collection_invariant", "restart": "restart_invariant", "integrity": "integrity_failed",
            "deadline": "deadline_exceeded", "interrupt": "interrupted", "workspace": "unsafe_workspace"}


class SoakFailure(Exception):
    def __init__(self, code, cycle=0, generation=0, case=None, http_status=None):
        super().__init__(code)
        self.code, self.cycle, self.generation = code, cycle, generation
        self.case = case
        self.http_status = http_status


def sha(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def invoke(argv, timeout, cwd=None):
    p = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                         cwd=cwd, start_new_session=(os.name == "posix"), shell=False)
    outputs = [bytearray(), bytearray()]
    overflow = threading.Event()
    def drain(pipe, target):
        while True:
            chunk=pipe.read(65536)
            if not chunk: return
            if len(target)+len(chunk)>MAX_OUTPUT:
                overflow.set()
                return
            target.extend(chunk)
    readers=[threading.Thread(target=drain,args=(p.stdout,outputs[0]),daemon=True),
             threading.Thread(target=drain,args=(p.stderr,outputs[1]),daemon=True)]
    for reader in readers: reader.start()
    expires=time.monotonic()+timeout
    try:
        while p.poll() is None:
            if overflow.is_set():
                stop_tree(p)
                raise SoakFailure("process_failed")
            if time.monotonic()>=expires:
                stop_tree(p)
                raise SoakFailure("process_failed")
            time.sleep(.01)
        for reader in readers: reader.join(timeout=1)
        if overflow.is_set() or any(reader.is_alive() for reader in readers): raise SoakFailure("process_failed")
        return p.returncode, bytes(outputs[0])
    finally:
        if p.poll() is None: stop_tree(p)
        for pipe in (p.stdout,p.stderr):
            if pipe: pipe.close()


def stop_tree(p):
    try:
        if os.name == "posix": os.killpg(p.pid, signal.SIGTERM)
        elif p.poll() is None: p.terminate()
    except ProcessLookupError: pass
    try: p.wait(timeout=3)
    except subprocess.TimeoutExpired: pass
    forced = False
    try:
        if os.name == "posix": os.killpg(p.pid, signal.SIGKILL)
        elif p.poll() is None: p.kill()
        forced = True
    except ProcessLookupError: pass
    try: p.wait(timeout=3)
    except subprocess.TimeoutExpired: forced = True
    return not forced and p.returncode == 0


def http(url, timeout, token=None, method="GET", body=None, allowed=()):
    parsed=urllib.parse.urlsplit(url)
    try:
        loopback=parsed.hostname=="localhost" or ipaddress.ip_address(parsed.hostname).is_loopback
    except (ValueError,TypeError): loopback=False
    if parsed.scheme!="http" or not loopback or parsed.username or parsed.password:
        raise SoakFailure("http_unexpected",case=ACTIVE_CASE)
    headers = {}
    if token:
        headers.update({"Origin": url.split("/api/", 1)[0], "X-Findrail-Token": token,
                        "Sec-Fetch-Site": "same-origin", "Content-Type": "application/json"})
    elif body is not None:
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
    try:
        with HTTP_OPENER.open(req, timeout=timeout) as r:
            raw = r.read(MAX_OUTPUT + 1)
            if len(raw) > MAX_OUTPUT: raise SoakFailure("http_unexpected", case=ACTIVE_CASE)
            try: obj=json.loads(raw) if raw else None
            except (ValueError, UnicodeDecodeError): raise SoakFailure("http_unexpected", case=ACTIVE_CASE)
            return r.status, obj
    except urllib.error.HTTPError as e:
        raw = e.read(MAX_OUTPUT + 1)
        try: obj = json.loads(raw)
        except Exception: obj = {}
        if e.code in allowed: return e.code, obj
        raise SoakFailure("http_unexpected", case=ACTIVE_CASE, http_status=e.code)
    except (urllib.error.URLError, TimeoutError, ValueError):
        raise SoakFailure("http_unexpected", case=ACTIVE_CASE)


def port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0)); return s.getsockname()[1]


def proc_metrics(pid):
    result = {"rss_bytes": None, "fd_count": None}
    if platform.system() == "Linux":
        try:
            for line in Path(f"/proc/{pid}/status").read_text().splitlines():
                if line.startswith("VmRSS:"):
                    parts=line.split()
                    result["rss_bytes"]=parse_rss_kb(parts[1]) if len(parts)>1 else None
            result["fd_count"] = len(list(Path(f"/proc/{pid}/fd").iterdir()))
        except (OSError, ValueError): pass
    return result


def parse_rss_kb(value):
    try:
        amount=int(value)
        return amount*1024 if amount>=0 else None
    except (TypeError,ValueError,OverflowError):
        return None


def latency_summary(samples):
    if not samples: return {"count": 0, "p50": None, "p95": None, "max": None}
    ordered=sorted(samples)
    return {"count":len(ordered),"p50":round(ordered[(len(ordered)-1)//2],3),
            "p95":round(ordered[math.ceil(len(ordered)*.95)-1],3),"max":round(max(ordered),3)}


def cycle_schedule(cycle):
    steps=("concurrent_reads","paging","successful_refresh","canceled_refresh","saved_collection_restart")
    return steps+("isolated_forget_readd",) if cycle%3==0 else steps


def remaining_time(deadline, now=None):
    remaining=deadline-(time.monotonic() if now is None else now)
    if remaining<=0: raise SoakFailure("deadline_exceeded")
    return min(15,remaining)


def run(args):
    if Path(args.output).expanduser().resolve(strict=False)==Path(args.binary).expanduser().resolve(strict=False):
        raise SoakFailure("unsafe_workspace")
    started = time.monotonic()
    duration = 180 if args.profile == "short" else args.duration_seconds
    deadline = started + (180 if args.profile == "short" else min(duration, 3600))
    expected_cycles = 3 if args.profile == "short" else 12
    corpus_size = args.documents or 100
    latency_samples = {"concurrent_search": [], "paging": [], "refresh": [], "cancel": [], "collection": [], "restart_integrity": []}
    binary = None
    report = {"schema": 1, "kind": "mixed_load_soak", "status": "incomplete",
              "binary": {"sha256": None, "version": None, "revision": None}, "source_sha": None,
              "environment": {"os": platform.system(), "os_release": platform.release(),
                              "arch": platform.machine(), "python": platform.python_version()},
              "profile": args.profile, "workload": {"seed": args.seed, "documents": corpus_size,
              "concurrency": args.concurrency, "target_cycles": expected_cycles},
              "limits": {"duration_seconds": duration, "hard_seconds": 180 if args.profile == "short" else 3600,
                         "subprocess_output_bytes": MAX_OUTPUT, "request_timeout_seconds": 15},
              "observations": {"rss_samples": [], "database_bytes": [], "wal_bytes": [], "fd_samples": []},
              "counters": {"operations": {}, "completed_cycles": 0, "restarts": 0, "unexpected_errors": 0,
                           "expected_busy_responses": 0, "canceled_refreshes": 0}, "cases": [], "failure_codes": []}
    p = None
    try:
        binary=args.binary.resolve(strict=True)
        if not binary.is_file() or not os.access(binary,os.X_OK): raise SoakFailure("setup_failed")
        repo_root=Path(__file__).resolve().parent.parent
        source_result=subprocess.run(["git","rev-parse","HEAD"],cwd=repo_root,capture_output=True,text=True,timeout=3)
        if source_result.returncode or len(source_result.stdout.strip())!=40: raise SoakFailure("setup_failed")
        report["source_sha"]=source_result.stdout.strip()
        dirty=subprocess.run(["git","status","--porcelain"],cwd=repo_root,capture_output=True,text=True,timeout=3)
        report["source_dirty"]=bool(dirty.stdout.strip()) if dirty.returncode==0 else None
        report["binary"]["sha256"]=sha(binary)
        with tempfile.TemporaryDirectory(prefix="findrail-mixed-") as td:
            root = Path(td); src = root / "source"; forgotten = root / "isolated-source"; data = root / "data"
            src.mkdir(mode=0o700); forgotten.mkdir(mode=0o700); data.mkdir(mode=0o700)
            (forgotten/"isolated.md").write_text("synthetic isolated forget readd marker\n",encoding="utf-8")
            fixture_hash=hashlib.sha256()
            for i in range(corpus_size):
                name=f"note-{i:05}.md"
                content=f"synthetic generation zero commonmarker seedmarker{args.seed} initialmarker{i} quote{i % 8}\n"
                (src / name).write_text(content,encoding="utf-8")
                fixture_hash.update(name.encode()+b"\0"+content.encode())
            fixture_hash.update(b"isolated-source/isolated.md\0synthetic isolated forget readd marker\n")
            report["fixture"]={"sha256":fixture_hash.hexdigest(),"documents":corpus_size,"format":"synthetic-markdown-v1"}
            def remaining():
                return remaining_time(deadline)
            rc, raw = invoke([str(binary), "doctor", "--json", "--data-dir", str(data)], remaining())
            rc, raw = invoke([str(binary), "index", "--json", "--data-dir", str(data), str(src)], remaining())
            if rc: raise SoakFailure("setup_failed")
            indexed = json.loads(raw); sid = indexed["source"]["id"]
            rc, raw = invoke([str(binary),"index","--json","--data-dir",str(data),str(forgotten)],remaining())
            if rc: raise SoakFailure("setup_failed")
            isolated_sid=json.loads(raw)["source"]["id"]
            rc, raw = invoke([str(binary), "doctor", "--json", "--data-dir", str(data)], remaining())
            doc = json.loads(raw); report["binary"].update(version=doc.get("version"), revision=doc.get("revision"))
            cycle = 1
            while cycle <= expected_cycles or (args.profile == "long" and time.monotonic() < started + duration):
                global ACTIVE_CASE
                generation = cycle
                address = f"127.0.0.1:{port()}"; base = "http://" + address
                p = subprocess.Popen([str(binary), "start", "--no-open", "--data-dir", str(data), "--addr", address],
                                     stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                     start_new_session=(os.name == "posix"))
                try:
                    token = None
                    ACTIVE_CASE = "server_ready"
                    end = min(deadline, time.monotonic() + 20)
                    while time.monotonic() < end:
                        if p.poll() is not None: raise SoakFailure("process_failed", cycle, generation)
                        try:
                            _, sess = http(base + "/api/v1/session", 1)
                            token = sess["token"]; break
                        except SoakFailure: time.sleep(.05)
                    if not token: raise SoakFailure("process_failed", cycle, generation)
                    if "isolated_forget_readd" in cycle_schedule(cycle):
                        rc,raw=invoke([str(binary),"search","--json","--data-dir",str(data),"isolated"],remaining())
                        if rc or json.loads(raw).get("total")!=1: raise SoakFailure("refresh_invariant",cycle,generation)
                        rc,_=invoke([str(binary),"forget","--data-dir",str(data),isolated_sid],remaining())
                        if rc: raise SoakFailure("refresh_invariant",cycle,generation)
                        rc,raw=invoke([str(binary),"search","--json","--data-dir",str(data),"isolated"],remaining())
                        if rc or json.loads(raw).get("total")!=0: raise SoakFailure("refresh_invariant",cycle,generation)
                        rc,raw=invoke([str(binary),"index","--json","--data-dir",str(data),str(forgotten)],remaining())
                        if rc or json.loads(raw)["source"]["id"]!=isolated_sid: raise SoakFailure("refresh_invariant",cycle,generation)
                        rc,raw=invoke([str(binary),"search","--json","--data-dir",str(data),"isolated"],remaining())
                        if rc or json.loads(raw).get("total")!=1: raise SoakFailure("refresh_invariant",cycle,generation)
                    ACTIVE_CASE = "concurrent_reads"
                    outcomes=[]; latency=[]; readerr=[]
                    def reader():
                        for _ in range(3):
                            t=time.perf_counter()
                            try:
                                status,obj=http(base+"/api/v1/search?"+urllib.parse.urlencode({"q":"commonmarker","limit":10}), remaining())
                                if status != 200 or obj["total"] != corpus_size: readerr.append(1)
                                latency.append((time.perf_counter()-t)*1000)
                            except SoakFailure: readerr.append(1)
                    threads=[threading.Thread(target=reader) for _ in range(args.concurrency)]
                    for t in threads:t.start()
                    for t in threads:t.join(timeout=remaining())
                    if any(t.is_alive() for t in threads) or readerr: raise SoakFailure("http_unexpected",cycle,generation)
                    latency_samples["concurrent_search"].extend(latency)
                    report["counters"]["operations"]["concurrent_search"] = report["counters"]["operations"].get("concurrent_search",0)+len(latency)
                    ACTIVE_CASE = "paging"
                    cursor=None; ids=[]; saved_cursor=None
                    while True:
                        query={"q":"commonmarker","limit":20}
                        if cursor: query["cursor"]=cursor
                        page_started=time.perf_counter()
                        _, page=http(base+"/api/v1/search/pages?"+urllib.parse.urlencode(query),remaining())
                        latency_samples["paging"].append((time.perf_counter()-page_started)*1000)
                        if page["total"] != corpus_size or page["offset"] != len(ids): raise SoakFailure("paging_invariant",cycle,generation)
                        ids += [x["id"] for x in page["results"]]; cursor=page.get("next_cursor")
                        if saved_cursor is None: saved_cursor=cursor
                        if not cursor: break
                    if len(ids)!=corpus_size or len(set(ids))!=corpus_size: raise SoakFailure("paging_invariant",cycle,generation)
                    outcomes.append({"case":"paging","status":"passed","generation":generation})
                    # Save a quoted entry using the documented authenticated collection API.
                    ACTIVE_CASE = "saved_collection"
                    collection_started=time.perf_counter()
                    _, sr=http(base+"/api/v1/search?"+urllib.parse.urlencode({"q":"initialmarker1","limit":1}),remaining())
                    hit=sr["results"][0]
                    _, ev=http(base+"/api/v1/documents/"+urllib.parse.quote(hit["id"],safe=""),remaining())
                    _, col=http(base+"/api/v1/collections",remaining(),token,"POST",{"title":f"soak-{cycle}"})
                    _, pin=http(base+f"/api/v1/collections/{col['id']}/entries",remaining(),token,"POST",
                                {"revision":col["revision"],"document_id":hit["id"],"page":ev.get("page",0),"excerpt":ev.get("text","")[:256],"whole_preview":False,"original_identity":ev.get("original_identity", ""),"text_start_char":ev.get("text_start_char",0)})
                    latency_samples["collection"].append((time.perf_counter()-collection_started)*1000)
                    # Edit/add/delete fixture and force refresh through CLI; one writer per cycle.
                    target=src/"note-00000.md"; target.write_text(f"synthetic generation {generation} commonmarker changedmarker{generation}\n",encoding="utf-8")
                    (src/f"added-{cycle}.md").write_text(f"synthetic generation {generation} addedtoken{generation}\n",encoding="utf-8")
                    if cycle>1: (src/f"added-{cycle-1}.md").unlink(missing_ok=True)
                    refresh_started=time.perf_counter()
                    rc,_=invoke([str(binary),"index","--json","--data-dir",str(data),str(src)],remaining())
                    latency_samples["refresh"].append((time.perf_counter()-refresh_started)*1000)
                    if rc: raise SoakFailure("refresh_invariant",cycle,generation)
                    ACTIVE_CASE = "refresh_validate"
                    rc,raw=invoke([str(binary),"search","--json","--data-dir",str(data),f"changedmarker{cycle}"],remaining())
                    if rc or json.loads(raw).get("total") != 1: raise SoakFailure("refresh_invariant",cycle,generation)
                    rc,raw=invoke([str(binary),"search","--json","--data-dir",str(data),"initialmarker0"],remaining())
                    if rc or json.loads(raw).get("total") != 0: raise SoakFailure("refresh_invariant",cycle,generation)
                    rc,raw=invoke([str(binary),"search","--json","--data-dir",str(data),f"addedtoken{cycle}"],remaining())
                    if rc or json.loads(raw).get("total") != 1: raise SoakFailure("refresh_invariant",cycle,generation)
                    if cycle>1:
                        rc,raw=invoke([str(binary),"search","--json","--data-dir",str(data),f"addedtoken{cycle-1}"],remaining())
                        if rc or json.loads(raw).get("total") != 0: raise SoakFailure("refresh_invariant",cycle,generation)
                    if saved_cursor:
                        ACTIVE_CASE = "cursor_fence"
                        fence_status,fence=http(base+"/api/v1/search/pages?"+urllib.parse.urlencode({"q":"commonmarker","limit":20,"cursor":saved_cursor}),remaining(),allowed=(409,))
                        if fence_status!=409 or fence.get("code") not in ("cursor_stale","cursor_session_changed"):
                            raise SoakFailure("paging_invariant",cycle,generation)
                    # Establish a committed baseline, then cancel an HTTP-managed refresh.
                    cancel_file=src/"cancelled-scan.md"; cancel_file.write_text("cancelbaseline\n",encoding="utf-8")
                    rc,_=invoke([str(binary),"index","--json","--data-dir",str(data),str(src)],remaining())
                    if rc: raise SoakFailure("cancel_invariant",cycle,generation)
                    cancel_file.write_text("cancelcandidate " + ("filler "*90000),encoding="utf-8")
                    ACTIVE_CASE = "cancel_refresh"
                    accepted=None
                    busy_until=min(deadline,time.monotonic()+5)
                    while time.monotonic()<busy_until:
                        ACTIVE_CASE = "cancel_refresh_submit"
                        status,accepted=http(base+f"/api/v1/sources/{sid}/refresh",remaining(),token,"POST",allowed=(409,429,503))
                        if status==202: break
                        report["counters"]["expected_busy_responses"]+=1
                        time.sleep(min(.05,remaining()))
                    if not accepted or status!=202: raise SoakFailure("cancel_invariant",cycle,generation)
                    job_id=accepted.get("job",{}).get("id")
                    if not job_id: raise SoakFailure("cancel_invariant",cycle,generation)
                    cancel_started=time.perf_counter()
                    job_url=base+"/api/v1/jobs/"+urllib.parse.quote(job_id,safe="")
                    job=None
                    ACTIVE_CASE = "cancel_refresh_poll"
                    for _ in range(100):
                        _, job=http(job_url,remaining())
                        if job.get("status")=="running": break
                        if job.get("status") in ("succeeded","failed","canceled"): break
                        time.sleep(min(.01,remaining()))
                    if job.get("status")!="running": raise SoakFailure("cancel_invariant",cycle,generation)
                    concurrent_cancel=[]
                    def cancel_read():
                        try:
                            code,result=http(base+"/api/v1/search?"+urllib.parse.urlencode({"q":"commonmarker","limit":1}),remaining())
                            concurrent_cancel.append(code==200 and result.get("total")==corpus_size)
                        except SoakFailure: concurrent_cancel.append(False)
                    cancel_reader=threading.Thread(target=cancel_read,daemon=True)
                    cancel_reader.start()
                    ACTIVE_CASE = "cancel_refresh_cancel"
                    cancel_status,_=http(base+f"/api/v1/jobs/{urllib.parse.quote(job_id,safe='')}/cancel",remaining(),token,"POST",allowed=(404,))
                    if cancel_status!=204: raise SoakFailure("cancel_invariant",cycle,generation)
                    cancel_reader.join(timeout=remaining())
                    if cancel_reader.is_alive() or not concurrent_cancel or not concurrent_cancel[0]:
                        raise SoakFailure("cancel_invariant",cycle,generation)
                    ACTIVE_CASE = "cancel_refresh_verify"
                    for _ in range(100):
                        _, job=http(job_url,remaining())
                        if job.get("status") in ("succeeded","failed","canceled"): break
                        time.sleep(min(.01,remaining()))
                    if job.get("status")!="canceled": raise SoakFailure("cancel_invariant",cycle,generation)
                    report["counters"]["canceled_refreshes"]+=1
                    latency_samples["cancel"].append((time.perf_counter()-cancel_started)*1000)
                    rc,raw=invoke([str(binary),"search","--json","--data-dir",str(data),"cancelbaseline"],remaining())
                    if rc or json.loads(raw).get("total") != 1: raise SoakFailure("cancel_invariant",cycle,generation)
                    rc,raw=invoke([str(binary),"search","--json","--data-dir",str(data),"cancelcandidate"],remaining())
                    if rc or json.loads(raw).get("total") != 0: raise SoakFailure("cancel_invariant",cycle,generation)
                    cancel_file.write_text("cancelbaseline\n",encoding="utf-8")
                    rc,_=invoke([str(binary),"index","--json","--data-dir",str(data),str(src)],remaining())
                    if rc: raise SoakFailure("cancel_invariant",cycle,generation)
                    # Exercise cursor invalidation as an allowed explicit fence, not page mixing.
                    outcomes.append({"case":"refresh_cancel","status":"passed","generation":generation})
                    # Restart is an explicit lifecycle boundary. Keep source and data; re-open and validate saved provenance.
                    ACTIVE_CASE = "integrity"
                    stop_tree(p); p=None; report["counters"]["restarts"]+=1
                    integrity_started=time.perf_counter()
                    rc,raw=invoke([str(binary),"doctor","--json","--integrity","--data-dir",str(data)],remaining())
                    latency_samples["restart_integrity"].append((time.perf_counter()-integrity_started)*1000)
                    if rc: raise SoakFailure("integrity_failed",cycle,generation)
                    integrity=json.loads(raw); checks={c.get("id"):c.get("status") for c in integrity.get("checks",[])}
                    if checks.get("integrity_quick_check")!="ok" or checks.get("integrity_foreign_key_check")!="ok": raise SoakFailure("integrity_failed",cycle,generation)
                    address=f"127.0.0.1:{port()}"; base="http://"+address
                    p=subprocess.Popen([str(binary),"start","--no-open","--data-dir",str(data),"--addr",address],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=(os.name=="posix"))
                    # Poll session until ready; then collection re-open and provenance check.
                    while time.monotonic()<min(deadline,time.monotonic()+15):
                        if p.poll() is not None: raise SoakFailure("restart_invariant",cycle,generation)
                        try: _,sess=http(base+"/api/v1/session",1); token=sess["token"]; break
                        except SoakFailure: time.sleep(.05)
                    else: raise SoakFailure("restart_invariant",cycle,generation)
                    ACTIVE_CASE = "collection_restore"
                    _, restored=http(base+f"/api/v1/collections/{col['id']}",remaining(),token)
                    entries=restored.get("entries") or []
                    evidence=(entries[0].get("evidence") or {}) if entries else {}
                    if (not entries or evidence.get("id")!=hit["id"] or evidence.get("source_id")!=sid or
                            evidence.get("path")!="note-00001.md" or "quote1" not in evidence.get("text","") or
                            not evidence.get("content_hash")):
                        raise SoakFailure("collection_invariant",cycle,generation)
                    _, saved_collection=http(base+f"/api/v1/collections/{col['id']}",remaining(),token)
                    revision=saved_collection.get("revision",0)
                    delete_url=base+f"/api/v1/collections/{col['id']}?revision={revision}"
                    http(delete_url,remaining(),token,"DELETE")
                    outcomes.append({"case":"collection_restart_provenance","status":"passed","generation":generation})
                    sample={"elapsed_seconds":round(time.monotonic()-started,3),**proc_metrics(p.pid)}
                    report["observations"]["rss_samples"].append(sample)
                    report["observations"]["fd_samples"].append(sample["fd_count"])
                    db=data/"findrail.db"; wal=data/"findrail.db-wal"
                    report["observations"]["database_bytes"].append(db.stat().st_size if db.exists() else 0)
                    report["observations"]["wal_bytes"].append(wal.stat().st_size if wal.exists() else 0)
                    outcomes.append({"case":"cycle","status":"passed","generation":generation})
                    report["cases"].extend(outcomes); report["counters"]["completed_cycles"]+=1
                finally:
                    retain_for_final_sample=(args.profile=="long" and cycle>=expected_cycles and
                                             time.monotonic()+1.0>=started+duration)
                    if p is not None and not retain_for_final_sample:
                        clean=stop_tree(p); p=None
                        report["clean_exit"] = report.get("clean_exit", True) and clean
                if args.profile=="short" and report["counters"]["completed_cycles"]>=3: break
                cycle += 1
                if args.profile=="long" and time.monotonic()<started+duration:
                    time.sleep(min(1.0,max(0,min(deadline,started+duration)-time.monotonic())))
            if args.profile=="long":
                if p is None: raise SoakFailure("restart_invariant")
                while time.monotonic() < min(deadline, started + duration):
                    report["observations"]["rss_samples"].append({"elapsed_seconds":round(time.monotonic()-started,3),**proc_metrics(p.pid)})
                    report["observations"]["fd_samples"].append(report["observations"]["rss_samples"][-1]["fd_count"])
                    time.sleep(min(5, max(0, min(deadline, started + duration)-time.monotonic())))
                if time.monotonic()-started < duration: raise SoakFailure("deadline_exceeded")
            if args.profile=="long" and (time.monotonic()-started<duration or report["counters"]["completed_cycles"]<12): raise SoakFailure("deadline_exceeded")
            report["elapsed_seconds"]=round(time.monotonic()-started,3); report["status"]="passed"
            report["clean_exit"] = report.get("clean_exit", True)
            report["latency_ms"]={key:latency_summary(values) for key,values in latency_samples.items() if values}
            report["counters"]["operations"].update({key:len(values) for key,values in latency_samples.items()})
    except KeyboardInterrupt: report["failure_codes"]=[FAILURES["interrupt"]]; report["status"]="incomplete"
    except SoakFailure as e:
        report["status"]="failed" if e.code!="interrupted" else "incomplete"
        report["failure_codes"]=[e.code]; report["counters"]["unexpected_errors"]+=1
        failed={"case":e.case or ACTIVE_CASE,"status":"failed","cycle":e.cycle,"generation":e.generation,"failure_code":e.code}
        if e.http_status is not None: failed["http_status"]=e.http_status
        report["cases"].append(failed)
    except Exception:
        report["status"]="failed"; report["failure_codes"]=["setup_failed"]; report["counters"]["unexpected_errors"]+=1
        report["cases"].append({"case":ACTIVE_CASE,"status":"failed","cycle":report["counters"]["completed_cycles"]+1,"generation":report["counters"]["completed_cycles"]+1,"failure_code":"setup_failed"})
    finally:
        if p is not None:
            clean=stop_tree(p)
            report["clean_exit"] = report.get("clean_exit", True) and clean
        if report.get("status")=="passed" and not report.get("clean_exit",False):
            report["status"]="failed"
            report["failure_codes"]=["process_failed"]
            report["counters"]["unexpected_errors"]+=1
        report["elapsed_seconds"]=round(time.monotonic()-started,3)
        report.setdefault("clean_exit", False)
    atomic_report(args.output,report)
    return 0 if report["status"]=="passed" else 1


def atomic_report(path, report):
    path=Path(path).expanduser().absolute(); path.parent.mkdir(parents=True,exist_ok=True)
    fd,name=tempfile.mkstemp(prefix=path.name+".",dir=path.parent)
    try:
        with os.fdopen(fd,"w",encoding="utf-8") as f:
            json.dump(report,f,sort_keys=True,separators=(",",":"),allow_nan=False); f.write("\n"); f.flush(); os.fsync(f.fileno())
        os.replace(name,path)
    finally:
        try: os.unlink(name)
        except FileNotFoundError: pass


def main():
    p=argparse.ArgumentParser(description=__doc__); p.add_argument("--binary",required=True,type=Path)
    p.add_argument("--profile",choices=("short","long"),required=True); p.add_argument("--seed",type=int,default=42)
    p.add_argument("--duration-seconds",type=int,default=1800); p.add_argument("--documents",type=int,choices=(100,1000))
    p.add_argument("--concurrency",type=int,default=4); p.add_argument("--output",required=True,type=Path)
    a=p.parse_args()
    if not 1<=a.concurrency<=16 or (a.profile=="long" and not 1<=a.duration_seconds<=3600): p.error("invalid bounded workload limits")
    previous=signal.getsignal(signal.SIGTERM)
    signal.signal(signal.SIGTERM,lambda _signum,_frame: (_ for _ in ()).throw(KeyboardInterrupt()))
    try: return run(a)
    finally: signal.signal(signal.SIGTERM,previous)


if __name__=="__main__": raise SystemExit(main())
