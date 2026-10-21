#!/usr/bin/env python3
"""Bounded mixed-load lifecycle soak against an explicitly prepared binary."""
import argparse
import contextvars
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
ACTIVE_CONTEXT = contextvars.ContextVar("soak_context", default=(0, 0, "setup"))
HTTP_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))
FAILURES = {"setup": "setup_failed", "process": "process_failed", "http": "http_unexpected",
            "paging": "paging_invariant", "refresh": "refresh_invariant", "cancel": "cancel_invariant",
            "collection": "collection_invariant", "restart": "restart_invariant", "integrity": "integrity_failed",
            "deadline": "deadline_exceeded", "interrupt": "interrupted", "workspace": "unsafe_workspace",
            "sync": "sync_failed", "sync_timeout": "sync_timeout"}
KNOWN_API_CODES = frozenset({"cursor_stale", "cursor_expired", "cursor_session_changed", "cursor_invalid",
                             "cursor_mismatch", "search_page_unavailable", "bundle_busy", "bundle_timeout"})
MAX_PAGING_WINDOW_ATTEMPTS = 3
PAGING_WINDOW_TIMEOUT = 60
SYNC_POLL_INTERVAL = .1
SHORT_HARD_SECONDS = 180
FINAL_CLEANUP_BUDGET = 60
SHORT_CLEANUP_BUDGET = 10
LONG_FINALIZE_BUDGET = 15


def set_context(case, cycle=None, generation=None):
    old_cycle, old_generation, _ = ACTIVE_CONTEXT.get()
    ACTIVE_CONTEXT.set((old_cycle if cycle is None else cycle,
                        old_generation if generation is None else generation, case))


def current_context():
    return ACTIVE_CONTEXT.get()


def valid_api_code(value):
    return value if isinstance(value, str) and value in KNOWN_API_CODES else "unknown"


class SoakFailure(Exception):
    def __init__(self, code, cycle=None, generation=None, case=None, http_status=None, api_error_code=None):
        super().__init__(code)
        active_cycle, active_generation, active_case = current_context()
        self.code = code
        self.cycle = active_cycle if cycle is None else cycle
        self.generation = active_generation if generation is None else generation
        self.case = case or active_case
        self.http_status = http_status
        self.api_error_code = valid_api_code(api_error_code) if api_error_code is not None else None


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
                raise SoakFailure("process_failed")
            if time.monotonic()>=expires:
                raise SoakFailure("process_failed")
            time.sleep(.01)
        for reader in readers: reader.join(timeout=1)
        if overflow.is_set() or any(reader.is_alive() for reader in readers): raise SoakFailure("process_failed")
        return p.returncode, bytes(outputs[0])
    finally:
        # The leader may already be gone while a descendant still holds either
        # pipe or keeps working. Always clean the process group, not just a live
        # direct child.
        tree_clean=stop_tree(p)
        for pipe in (p.stdout,p.stderr):
            if pipe: pipe.close()
        if p.returncode==0 and not tree_clean:
            raise SoakFailure("process_failed")


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


def record_stop(report, process):
    clean=stop_tree(process)
    report["clean_exit"]=report.get("clean_exit",True) and clean
    return clean


def fail_if_unclean(report):
    if report.get("status")=="passed" and not report.get("clean_exit",False):
        report["status"]="failed"
        report["failure_codes"]=["process_failed"]
        report["counters"]["unexpected_errors"]+=1


def http(url, timeout, token=None, method="GET", body=None, allowed=()):
    cycle, generation, case = current_context()
    parsed=urllib.parse.urlsplit(url)
    try:
        loopback=parsed.hostname=="localhost" or ipaddress.ip_address(parsed.hostname).is_loopback
    except (ValueError,TypeError): loopback=False
    if parsed.scheme!="http" or not loopback or parsed.username or parsed.password:
        raise SoakFailure("http_unexpected", cycle, generation, case)
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
            if len(raw) > MAX_OUTPUT:
                raise SoakFailure("http_unexpected", cycle, generation, case, http_status=r.status)
            try: obj=json.loads(raw) if raw else None
            except (ValueError, UnicodeDecodeError):
                raise SoakFailure("http_unexpected", cycle, generation, case, http_status=r.status)
            return r.status, obj
    except urllib.error.HTTPError as e:
        raw = e.read(MAX_OUTPUT + 1)
        try:
            parsed_body = json.loads(raw) if len(raw) <= MAX_OUTPUT else None
        except (ValueError, UnicodeDecodeError):
            parsed_body = None
        code = valid_api_code(parsed_body.get("code")) if isinstance(parsed_body, dict) else "unknown"
        if e.code in allowed:
            return e.code, {"code": code}
        raise SoakFailure("http_unexpected", cycle, generation, case, http_status=e.code,
                          api_error_code=code)
    except (urllib.error.URLError, TimeoutError, ValueError):
        raise SoakFailure("http_unexpected", cycle, generation, case)


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


def profile_limits(profile, duration_seconds):
    if profile == "short":
        return 180, SHORT_HARD_SECONDS
    if profile == "long" and 1 <= duration_seconds <= 3600-FINAL_CLEANUP_BUDGET:
        return duration_seconds, min(3600, duration_seconds+FINAL_CLEANUP_BUDGET)
    raise SoakFailure("setup_failed")


def cycle_is_required(profile, cycle, minimum_cycles, target_end, now):
    return cycle <= minimum_cycles or (profile == "long" and now < target_end)


def finalize_metrics(report, latency_samples):
    report["latency_ms"] = {key: latency_summary(values) for key, values in latency_samples.items() if values}
    report["counters"]["operations"].update({key: len(values) for key, values in latency_samples.items()})


def failure_case(error):
    case = {"case": error.case, "status": "incomplete" if error.code == "interrupted" else "failed", "cycle": error.cycle,
            "generation": error.generation, "failure_code": error.code}
    if error.http_status is not None:
        case["http_status"] = error.http_status
    if error.api_error_code is not None:
        case["api_error_code"] = valid_api_code(error.api_error_code)
    return case


def wait_for_sync(fetch_statuses, expected_source_ids, hard_deadline, *, timeout=60,
                  after_success=None, stopped=lambda: False, now=time.monotonic,
                  sleep=time.sleep):
    """Wait for an observed successful startup scan of every requested source."""
    expected = set(expected_source_ids)
    if not expected:
        raise SoakFailure("sync_failed")
    after_success = after_success or {}
    end = min(hard_deadline, now() + timeout)
    last_statuses = {}
    while now() < end:
        if stopped():
            raise SoakFailure("process_failed")
        try:
            response = fetch_statuses(min(5, end - now()))
        except SoakFailure:
            if stopped():
                raise SoakFailure("process_failed")
            sleep(min(SYNC_POLL_INTERVAL, max(0, end - now())))
            continue
        if not isinstance(response, dict) or response.get("enabled") is not True:
            raise SoakFailure("sync_failed")
        records = response.get("sources")
        if not isinstance(records, list):
            raise SoakFailure("sync_failed")
        last_statuses = {item.get("source_id"): item for item in records
                         if isinstance(item, dict) and isinstance(item.get("source_id"), str)}
        if any(last_statuses.get(source_id, {}).get("state") == "error" for source_id in expected):
            raise SoakFailure("sync_failed")
        ready = True
        for source_id in expected:
            status = last_statuses.get(source_id)
            if not status:
                ready = False
                break
            progress = status.get("progress")
            marker = after_success.get(source_id)
            same_success = (isinstance(marker, dict) and
                            status.get("last_success_at") == marker.get("last_success_at"))
            sequence = progress.get("sequence") if isinstance(progress, dict) else None
            unchanged = same_success and (type(sequence) is not int or sequence <= marker.get("sequence", -1))
            if (status.get("state") != "idle" or not status.get("last_success_at") or
                    unchanged or
                    not isinstance(progress, dict) or progress.get("phase") != "done" or
                    progress.get("committed") is not True or progress.get("partial") is True):
                ready = False
                break
        if ready:
            return {source_id: {"last_success_at": last_statuses[source_id]["last_success_at"],
                                "sequence": last_statuses[source_id]["progress"].get("sequence", 0)}
                    for source_id in expected}
        sleep(min(SYNC_POLL_INTERVAL, max(0, end - now())))
    if stopped():
        raise SoakFailure("process_failed")
    raise SoakFailure("sync_timeout")


def collect_paging_window(fetch_page, expected_total, hard_deadline, *, max_attempts=MAX_PAGING_WINDOW_ATTEMPTS,
                          timeout=PAGING_WINDOW_TIMEOUT, now=time.monotonic, record_latency=lambda _x: None,
                          record_restart=lambda: None):
    """Return one complete page generation; only cursor_stale restarts the window."""
    end = min(hard_deadline, now() + timeout)
    for attempt in range(1, max_attempts + 1):
        ids, cursor, saved_cursor = [], None, None
        while True:
            remaining = end - now()
            if remaining <= 0:
                raise SoakFailure("deadline_exceeded", case="paging")
            try:
                status, page, elapsed_ms = fetch_page(cursor, min(15, remaining))
            except SoakFailure as error:
                if error.http_status == 409 and error.api_error_code == "cursor_stale":
                    if hasattr(error, "elapsed_ms"):
                        record_latency(error.elapsed_ms)
                    if attempt == max_attempts:
                        raise SoakFailure("paging_invariant", error.cycle, error.generation, "paging",
                                          409, "cursor_stale")
                    record_restart()
                    break
                raise
            record_latency(elapsed_ms)
            if status == 409:
                code = page.get("code") if isinstance(page, dict) else "unknown"
                if code != "cursor_stale":
                    raise SoakFailure("paging_invariant", case="paging", http_status=409,
                                      api_error_code=code)
                if attempt == max_attempts:
                    raise SoakFailure("paging_invariant", case="paging", http_status=409,
                                      api_error_code=code)
                record_restart()
                break
            if status != 200 or not isinstance(page, dict):
                raise SoakFailure("paging_invariant", case="paging", http_status=status)
            results = page.get("results")
            page_total, offset = page.get("total"), page.get("offset")
            if (type(page_total) is not int or page_total != expected_total or type(offset) is not int or
                    offset != len(ids) or not isinstance(results, list) or
                    any(not isinstance(item, dict) or not isinstance(item.get("id"), str) or not item.get("id")
                        for item in results)):
                raise SoakFailure("paging_invariant", case="paging")
            ids.extend(item["id"] for item in results)
            next_cursor = page.get("next_cursor")
            if next_cursor is not None and (not isinstance(next_cursor, str) or not next_cursor):
                raise SoakFailure("paging_invariant", case="paging")
            if saved_cursor is None:
                saved_cursor = next_cursor
            if next_cursor is None:
                if len(ids) != expected_total or len(set(ids)) != expected_total:
                    raise SoakFailure("paging_invariant", case="paging")
                return ids, saved_cursor
            cursor = next_cursor
    raise SoakFailure("paging_invariant", case="paging")


def run(args):
    if Path(args.output).expanduser().resolve(strict=False)==Path(args.binary).expanduser().resolve(strict=False):
        raise SoakFailure("unsafe_workspace")
    ACTIVE_CONTEXT.set((0, 0, "setup"))
    started = time.monotonic()
    duration, hard_seconds = profile_limits(args.profile, args.duration_seconds)
    target_end = started + duration
    deadline = started + hard_seconds
    cleanup_budget = LONG_FINALIZE_BUDGET if args.profile == "long" else SHORT_CLEANUP_BUDGET
    work_deadline = deadline - cleanup_budget
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
              "limits": {"duration_seconds": duration, "target_duration_seconds": duration,
                          "hard_seconds": hard_seconds, "work_deadline_seconds": hard_seconds-cleanup_budget,
                          "cleanup_budget_seconds": cleanup_budget,
                          "subprocess_output_bytes": MAX_OUTPUT, "request_timeout_seconds": 15,
                          "paging_window_attempts": MAX_PAGING_WINDOW_ATTEMPTS,
                          "paging_window_timeout_seconds": PAGING_WINDOW_TIMEOUT,
                          "sync_barrier_timeout_seconds": 60},
              "observations": {"rss_samples": [], "database_bytes": [], "wal_bytes": [], "fd_samples": []},
               "clean_exit": True,
               "counters": {"operations": {}, "completed_cycles": 0, "restarts": 0, "unexpected_errors": 0,
                            "expected_busy_responses": 0, "canceled_refreshes": 0,
                            "paging_window_restarts": 0}, "cases": [], "failure_codes": []}
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
                return remaining_time(work_deadline)
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
            while cycle_is_required(args.profile, cycle, expected_cycles, target_end, time.monotonic()):
                generation = cycle
                set_context("server_ready", cycle, generation)
                address = f"127.0.0.1:{port()}"; base = "http://" + address
                p = subprocess.Popen([str(binary), "start", "--no-open", "--data-dir", str(data), "--addr", address],
                                     stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                     start_new_session=(os.name == "posix"))
                try:
                    token = None
                    set_context("server_ready")
                    end = min(work_deadline, time.monotonic() + 20)
                    while time.monotonic() < end:
                        if p.poll() is not None: raise SoakFailure("process_failed", cycle, generation)
                        try:
                            _, sess = http(base + "/api/v1/session", 1)
                            token = sess["token"]; break
                        except SoakFailure: time.sleep(.05)
                    if not token: raise SoakFailure("process_failed", cycle, generation)
                    startup_sync = wait_for_sync(
                        lambda timeout: http(base+"/api/v1/sync", timeout)[1],
                        (sid, isolated_sid), work_deadline, stopped=lambda: p.poll() is not None)
                    if "isolated_forget_readd" in cycle_schedule(cycle):
                        set_context("isolated_forget_readd")
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
                        wait_for_sync(lambda timeout: http(base+"/api/v1/sync", timeout)[1],
                                      (isolated_sid,), work_deadline,
                                      after_success={isolated_sid: startup_sync.get(isolated_sid)},
                                      stopped=lambda: p.poll() is not None)
                    set_context("concurrent_reads")
                    outcomes=[]; latency=[]; readerr=[]
                    def reader():
                        set_context("concurrent_reads", cycle, generation)
                        for _ in range(3):
                            t=time.perf_counter()
                            try:
                                status,obj=http(base+"/api/v1/search?"+urllib.parse.urlencode({"q":"commonmarker","limit":10}), remaining())
                                if status != 200 or not isinstance(obj, dict) or obj.get("total") != corpus_size:
                                    readerr.append(SoakFailure("http_unexpected",cycle,generation,"concurrent_reads",status))
                                latency.append((time.perf_counter()-t)*1000)
                            except SoakFailure as error:
                                error.elapsed_ms=(time.perf_counter()-t)*1000
                                latency.append(error.elapsed_ms)
                                readerr.append(error)
                    threads=[threading.Thread(target=reader) for _ in range(args.concurrency)]
                    for t in threads:t.start()
                    for t in threads:t.join(timeout=remaining())
                    latency_samples["concurrent_search"].extend(latency)
                    report["counters"]["operations"]["concurrent_search"] = report["counters"]["operations"].get("concurrent_search",0)+len(latency)
                    if any(t.is_alive() for t in threads): raise SoakFailure("http_unexpected",cycle,generation,"concurrent_reads")
                    if readerr: raise readerr[0]
                    set_context("paging")
                    def fetch_page(cursor, timeout):
                        query={"q":"commonmarker","limit":20}
                        if cursor: query["cursor"]=cursor
                        page_started=time.perf_counter()
                        try:
                            status, page=http(base+"/api/v1/search/pages?"+urllib.parse.urlencode(query),timeout,allowed=(409,))
                        except SoakFailure as error:
                            error.elapsed_ms=(time.perf_counter()-page_started)*1000
                            raise
                        return status, page, (time.perf_counter()-page_started)*1000
                    ids, saved_cursor=collect_paging_window(
                        fetch_page, corpus_size, work_deadline,
                        record_latency=latency_samples["paging"].append,
                        record_restart=lambda: report["counters"].__setitem__(
                            "paging_window_restarts", report["counters"]["paging_window_restarts"]+1))
                    outcomes.append({"case":"paging","status":"passed","generation":generation})
                    # Save a quoted entry using the documented authenticated collection API.
                    set_context("saved_collection")
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
                    set_context("refresh_validate")
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
                        set_context("cursor_fence")
                        fence_status,fence=http(base+"/api/v1/search/pages?"+urllib.parse.urlencode({"q":"commonmarker","limit":20,"cursor":saved_cursor}),remaining(),allowed=(409,))
                        if fence_status!=409 or fence.get("code") not in ("cursor_stale","cursor_session_changed"):
                            raise SoakFailure("paging_invariant",cycle,generation)
                    # Establish a committed baseline, then cancel an HTTP-managed refresh.
                    cancel_file=src/"cancelled-scan.md"; cancel_file.write_text("cancelbaseline\n",encoding="utf-8")
                    rc,_=invoke([str(binary),"index","--json","--data-dir",str(data),str(src)],remaining())
                    if rc: raise SoakFailure("cancel_invariant",cycle,generation)
                    cancel_file.write_text("cancelcandidate " + ("filler "*90000),encoding="utf-8")
                    set_context("cancel_refresh")
                    accepted=None
                    busy_until=min(work_deadline,time.monotonic()+5)
                    while time.monotonic()<busy_until:
                        set_context("cancel_refresh_submit")
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
                    set_context("cancel_refresh_poll")
                    for _ in range(100):
                        _, job=http(job_url,remaining())
                        if job.get("status")=="running": break
                        if job.get("status") in ("succeeded","failed","canceled"): break
                        time.sleep(min(.01,remaining()))
                    if job.get("status")!="running": raise SoakFailure("cancel_invariant",cycle,generation)
                    concurrent_cancel=[]
                    def cancel_read():
                        set_context("cancel_refresh_read", cycle, generation)
                        try:
                            code,result=http(base+"/api/v1/search?"+urllib.parse.urlencode({"q":"commonmarker","limit":1}),remaining())
                            concurrent_cancel.append(code==200 and result.get("total")==corpus_size)
                        except SoakFailure as error: concurrent_cancel.append(error)
                    cancel_reader=threading.Thread(target=cancel_read,daemon=True)
                    cancel_reader.start()
                    set_context("cancel_refresh_cancel")
                    cancel_status,_=http(base+f"/api/v1/jobs/{urllib.parse.quote(job_id,safe='')}/cancel",remaining(),token,"POST",allowed=(404,))
                    if cancel_status!=204: raise SoakFailure("cancel_invariant",cycle,generation)
                    cancel_reader.join(timeout=remaining())
                    if cancel_reader.is_alive():
                        raise SoakFailure("cancel_invariant",cycle,generation)
                    if not concurrent_cancel or concurrent_cancel[0] is not True:
                        if concurrent_cancel and isinstance(concurrent_cancel[0], SoakFailure):
                            raise concurrent_cancel[0]
                        raise SoakFailure("cancel_invariant",cycle,generation)
                    set_context("cancel_refresh_verify")
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
                    set_context("integrity")
                    stopped=p; p=None
                    if not record_stop(report,stopped): raise SoakFailure("process_failed",cycle,generation)
                    report["counters"]["restarts"]+=1
                    integrity_started=time.perf_counter()
                    rc,raw=invoke([str(binary),"doctor","--json","--integrity","--data-dir",str(data)],remaining())
                    latency_samples["restart_integrity"].append((time.perf_counter()-integrity_started)*1000)
                    if rc: raise SoakFailure("integrity_failed",cycle,generation)
                    integrity=json.loads(raw); checks={c.get("id"):c.get("status") for c in integrity.get("checks",[])}
                    if checks.get("integrity_quick_check")!="ok" or checks.get("integrity_foreign_key_check")!="ok": raise SoakFailure("integrity_failed",cycle,generation)
                    address=f"127.0.0.1:{port()}"; base="http://"+address
                    p=subprocess.Popen([str(binary),"start","--no-open","--data-dir",str(data),"--addr",address],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=(os.name=="posix"))
                    # Poll session until ready; then collection re-open and provenance check.
                    restart_ready_deadline=min(work_deadline,time.monotonic()+15)
                    while time.monotonic()<restart_ready_deadline:
                        if p.poll() is not None: raise SoakFailure("restart_invariant",cycle,generation)
                        try: _,sess=http(base+"/api/v1/session",1); token=sess["token"]; break
                        except SoakFailure: time.sleep(.05)
                    else: raise SoakFailure("restart_invariant",cycle,generation)
                    wait_for_sync(lambda timeout: http(base+"/api/v1/sync", timeout)[1],
                                  (sid, isolated_sid), work_deadline,
                                  stopped=lambda: p.poll() is not None)
                    set_context("collection_restore")
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
                    if p is not None:
                        stopped=p; p=None
                        if not record_stop(report,stopped):
                            raise SoakFailure("process_failed",cycle,generation)
                if args.profile=="short" and report["counters"]["completed_cycles"]>=3: break
                cycle += 1
            # Long profile runs complete real mixed-load cycles until the target
            # duration. It never substitutes a quiet observation tail for load.
            if args.profile=="long" and (time.monotonic()-started<duration or report["counters"]["completed_cycles"]<12): raise SoakFailure("deadline_exceeded")
            report["elapsed_seconds"]=round(time.monotonic()-started,3); report["status"]="passed"
            report["clean_exit"] = report.get("clean_exit", True)
    except KeyboardInterrupt:
        report["failure_codes"]=[FAILURES["interrupt"]]
        report["status"]="incomplete"
        cycle, generation, case = current_context()
        report["cases"].append({"case":case,"status":"incomplete","cycle":cycle,
                                "generation":generation,"failure_code":FAILURES["interrupt"]})
    except SoakFailure as e:
        report["status"]="failed" if e.code!="interrupted" else "incomplete"
        report["failure_codes"]=[e.code]
        if e.code != "interrupted": report["counters"]["unexpected_errors"]+=1
        report["cases"].append(failure_case(e))
    except Exception:
        report["status"]="failed"; report["failure_codes"]=["setup_failed"]; report["counters"]["unexpected_errors"]+=1
        cycle, generation, case = current_context()
        report["cases"].append({"case":case,"status":"failed","cycle":cycle,
                                "generation":generation,"failure_code":"setup_failed"})
    finally:
        if p is not None:
            stopped=p; p=None
            record_stop(report,stopped)
        fail_if_unclean(report)
        report["elapsed_seconds"]=round(time.monotonic()-started,3)
        report.setdefault("clean_exit", False)
        finalize_metrics(report, latency_samples)
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
    if not 1<=a.concurrency<=16 or (a.profile=="long" and not 1<=a.duration_seconds<=3600-FINAL_CLEANUP_BUDGET): p.error("invalid bounded workload limits")
    previous=signal.getsignal(signal.SIGTERM)
    signal.signal(signal.SIGTERM,lambda _signum,_frame: (_ for _ in ()).throw(KeyboardInterrupt()))
    try: return run(a)
    finally: signal.signal(signal.SIGTERM,previous)


if __name__=="__main__": raise SystemExit(main())
