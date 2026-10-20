#!/usr/bin/env python3
"""Generate a bounded synthetic corpus and measure Findrail through its CLI."""
import argparse
import hashlib
import json
import math
import os
import platform
import random
import signal
import socket
import sqlite3
import subprocess
import tempfile
import threading
import time
import sys
import urllib.parse
import urllib.request
import urllib.error
from pathlib import Path

FIXTURE_VERSION = 2
MAX_BYTES = 256 * 1024 * 1024
MAX_SECONDS = 1800
QUERIES = {"common": "commonmark", "rare": "raretoken"}


def digest_file(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def pdf_bytes(text):
    safe = text.encode("ascii", "replace").decode("ascii").replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)")
    stream = f"BT /F1 10 Tf 40 760 Td ({safe}) Tj ET".encode()
    objs = [b"<< /Type /Catalog /Pages 2 0 R >>", b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
            b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
            b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
            b"<< /Length %d >>\nstream\n" % len(stream) + stream + b"\nendstream"]
    out = bytearray(b"%PDF-1.4\n")
    offsets = [0]
    for i, obj in enumerate(objs, 1):
        offsets.append(len(out)); out.extend(f"{i} 0 obj\n".encode() + obj + b"\nendobj\n")
    xref = len(out); out.extend(f"xref\n0 {len(offsets)}\n0000000000 65535 f \n".encode())
    for off in offsets[1:]: out.extend(f"{off:010d} 00000 n \n".encode())
    out.extend(f"trailer << /Size {len(offsets)} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n".encode())
    return bytes(out)


def make_docx(text):
    import io, zipfile
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
        def add(name, text):
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            z.writestr(info, text)
        add("[Content_Types].xml", '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>')
        add("_rels/.rels", '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>')
        import html
        add("word/document.xml", '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>'+html.escape(text)+'</w:t></w:r></w:p></w:body></w:document>')
    return buf.getvalue()


def generate(root, count, seed, deadline):
    rng = random.Random(seed)
    total = 0
    written = 0
    for i in range(count):
        if time.monotonic() > deadline: raise TimeoutError("generation time cap exceeded")
        kind = ("txt", "go", "docx", "pdf")[i % 4]
        # Controlled frequency distribution; every document remains bounded.
        token = "commonmark" if i % 2 == 0 else "ordinary"
        rare = " raretoken" if i % 97 == 0 else ""
        # Include a long document of each format even in the 100-file smoke.
        words = 8192 if i % 1000 < 4 else 1 + rng.randrange(24)
        body = f"Synthetic fixture v{FIXTURE_VERSION} документ Ω\npackage sample\n{token}{rare} item{i} " + ("longtext " * words)
        data = body.encode() if kind in ("txt", "go") else make_docx(body) if kind == "docx" else pdf_bytes(body)
        total += len(body.encode())
        written += len(data)
        if total > MAX_BYTES or written > MAX_BYTES: raise ValueError("corpus byte cap exceeded before writing next file")
        suffix = ".md" if kind == "txt" else "." + kind
        target = root / f"doc-{i:06d}{suffix}"
        with target.open("xb") as f: f.write(data)
    return total


def run(argv, timeout):
    child = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                             stderr=subprocess.PIPE, text=True, shell=False)
    try:
        stdout, stderr = child.communicate(timeout=timeout)
        if child.returncode:
            raise subprocess.CalledProcessError(child.returncode, argv)
        return subprocess.CompletedProcess(argv, child.returncode, stdout, stderr)
    finally:
        stop_process(child)


def stop_process(child):
    if child.poll() is None:
        child.terminate()
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait(timeout=5)


def latency_summary(samples):
    ordered = sorted(samples)
    middle = len(ordered) // 2
    median = ordered[middle] if len(ordered) % 2 else (ordered[middle - 1] + ordered[middle]) / 2
    return {"p50": round(median, 3), "p95": round(ordered[math.ceil(len(ordered) * .95) - 1], 3)}


def database_sizes(data_dir):
    return {name: (data_dir / name).stat().st_size if (data_dir / name).exists() else 0
            for name in ("findrail.db", "findrail.db-wal", "findrail.db-shm")}


def directory_file_bytes(root):
    return sum(path.stat().st_size for path in root.iterdir() if path.is_file())


def server_rss_bytes(pid):
    # Observe only the actual server child; never report Python's RSS as its own.
    if platform.system() == "Linux":
        try:
            with Path(f"/proc/{pid}/status").open(encoding="ascii") as status:
                for line in status:
                    if line.startswith("VmRSS:"):
                        return int(line.split()[1]) * 1024
        except (OSError, ValueError):
            pass
    return None


def host_processor():
    value = platform.processor().strip()
    if not value and platform.system() == "Linux":
        try:
            with Path("/proc/cpuinfo").open(encoding="ascii", errors="replace") as cpuinfo:
                for line in cpuinfo:
                    if line.lower().startswith(("model name", "hardware")) and ":" in line:
                        value = line.split(":", 1)[1].strip()
                        break
        except OSError:
            pass
    return value[:100] if value else "unavailable"


def remaining_timeout(deadline, maximum):
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise TimeoutError("harness time cap exceeded")
    return min(maximum, remaining)


def paging_profile(base_url, expected, deadline):
    """Exercise real page sizes and traverse only the API's 1,000-result window."""
    profiles = {}
    reference_order = None
    for page_size in (20, 50, 100):
        ids, sizes, latencies, previous = [], [], [], None
        cursor = None
        while len(ids) < min(expected, 1000):
            params = {"q": QUERIES["common"], "limit": page_size}
            if cursor: params["cursor"] = cursor
            started = time.perf_counter()
            with urllib.request.urlopen(base_url + "/api/v1/search/pages?" + urllib.parse.urlencode(params),
                                        timeout=remaining_timeout(deadline, 10)) as response:
                raw = response.read((512 << 10) + 1)
            latencies.append((time.perf_counter() - started) * 1000); sizes.append(len(raw))
            if len(raw) > 512 << 10: raise RuntimeError("page exceeded encoded response cap")
            page = json.loads(raw)
            if page["total"] != expected or page["offset"] != len(ids): raise RuntimeError("paging fixture shape changed")
            batch = [item["id"] for item in page["results"]]
            if not batch or batch[0] == previous: raise RuntimeError("paging order contains duplicate boundary")
            ids.extend(batch); previous = batch[-1]; cursor = page["next_cursor"]
            if cursor is None:
                expected_end = "window_limit" if expected > 1000 else "complete"
                if page.get("end_reason") != expected_end:
                    raise RuntimeError("paging returned an unexpected window end reason")
                break
        target = min(expected, 1000)
        if len(ids) != target or len(set(ids)) != target: raise RuntimeError("paging lost or duplicated results")
        if expected > 1000 and cursor is not None: raise RuntimeError("paging exceeded the result window")
        if expected <= 1000 and cursor is not None: raise RuntimeError("paging window did not complete")
        if reference_order is not None and ids != reference_order:
            raise RuntimeError("paging order changed with page size")
        reference_order = ids
        profiles[str(page_size)] = {"pages": len(sizes), "returned": len(ids),
                                    "window_complete": expected <= 1000,
                                    "sample_count": len(latencies), "warmup_requests": 0,
                                    "max_response_bytes": max(sizes), "latency_ms": latency_summary(latencies)}
    legacy = profiles["100"]
    target = min(expected, 1000)
    return {"window_limit": 1000, "total": expected, "pages": legacy["pages"],
            "returned": target, "beyond_237": target > 237,
            "max_response_bytes": legacy["max_response_bytes"],
            "latency_ms": legacy["latency_ms"], "page_sizes": profiles}


def refresh_profile(binary, data_dir, source, base_url, expected, deadline):
    """Measure one committed refresh and one canceled full scan under HTTP reads."""
    if os.name != "posix":
        raise RuntimeError("refresh cancellation workload requires POSIX SIGTERM")
    index_args = [str(binary), "index", "--json", "--data-dir", str(data_dir),
                  "--max-bytes", str(1 << 20), "--max-pdf-bytes", str(1 << 20), str(source)]
    first = source / "doc-000000.md"
    first.write_bytes(first.read_bytes() + b"\nrefreshpassageone\n")
    started = time.perf_counter()
    refreshed = run(index_args, remaining_timeout(deadline, MAX_SECONDS))
    refresh_ms = (time.perf_counter() - started) * 1000
    refresh_result = json.loads(refreshed.stdout)
    if int(refresh_result.get("updated", 0)) != 1:
        raise RuntimeError("successful refresh did not update exactly one synthetic document")

    # Change a second document, then exercise read availability during the scan
    # that will be canceled. The saved source/index are owned by this harness.
    second = source / "doc-000001.go"
    original = second.read_bytes()
    second.write_bytes(original + b"\n// refreshpassagetwo\n")
    stop_reads = threading.Event()
    read_ready = threading.Event()
    read_samples = []
    read_errors = []

    def read_loop():
        while not stop_reads.is_set():
            try:
                started_read = time.perf_counter()
                with urllib.request.urlopen(base_url + "/api/v1/search?" +
                                            urllib.parse.urlencode({"q": QUERIES["common"], "limit": 1}),
                                            timeout=remaining_timeout(deadline, 15)) as response:
                    obj = json.loads(response.read(1 << 20))
                actual_total = int(obj.get("total", -1))
                if actual_total != expected:
                    read_errors.append("count_mismatch_expected_" + str(expected) +
                                       "_actual_" + str(actual_total))
                    return
                read_samples.append((time.perf_counter() - started_read) * 1000)
                read_ready.set()
            except urllib.error.HTTPError as exc:
                read_errors.append("http_" + str(exc.code))
                return
            except urllib.error.URLError as exc:
                read_errors.append("connection_error")
                return
            except json.JSONDecodeError:
                read_errors.append("invalid_json")
                return
            except BaseException as exc:
                read_errors.append(type(exc).__name__)
                return

    reader = threading.Thread(target=read_loop, name="resource-refresh-read", daemon=True)
    child = subprocess.Popen(index_args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                             stderr=subprocess.PIPE, text=True, shell=False)
    reader_started = False
    try:
        reader.start()
        reader_started = True
        canceled_started = time.perf_counter()
        # Keep the refresh alive until both the minimum overlap and a real read
        # completion have been observed; never wait beyond the shared deadline.
        overlap_deadline = time.monotonic() + min(5, remaining_timeout(deadline, 5))
        while time.monotonic() - canceled_started < .25 or not read_ready.is_set():
            if read_errors:
                raise RuntimeError("concurrent read failed during refresh: " + read_errors[0])
            if time.monotonic() >= overlap_deadline:
                raise TimeoutError("no concurrent read completed during refresh")
            remaining_timeout(deadline, 1)
            time.sleep(.02)
        if child.poll() is not None:
            raise RuntimeError("refresh completed before cancellation could be measured")
        child.terminate()
        child.communicate(timeout=remaining_timeout(deadline, 10))
        canceled_ms = (time.perf_counter() - canceled_started) * 1000
        if child.returncode == 0:
            raise RuntimeError("canceled refresh unexpectedly committed successfully")
    finally:
        stop_reads.set()
        # Cleanup has its own bounded allowance even when the work deadline
        # expired. A join/start failure must not bypass child termination.
        try:
            if reader_started:
                reader.join(timeout=20)
        finally:
            stop_process(child)
    if reader.is_alive():
        raise RuntimeError("concurrent read sampler did not stop")
    if not read_samples:
        raise RuntimeError("no read search completed during refresh: " + (read_errors[0] if read_errors else "unknown"))
    if read_errors:
        raise RuntimeError("concurrent read failed during refresh: " + read_errors[0])
    with sqlite3.connect((data_dir / "findrail.db").as_uri() + "?mode=ro", uri=True) as db:
        stored = db.execute("SELECT content FROM documents WHERE path='doc-000001.go'").fetchone()
    if stored is None or "refreshpassagetwo" in stored[0]:
        raise RuntimeError("canceled refresh did not preserve the prior committed document")
    return {"successful_refresh_wall_ms": round(refresh_ms, 3),
            "successful_refresh_samples": 1,
            "successful_refresh_updated_documents": int(refresh_result.get("updated", 0)),
            "canceled_refresh_wall_ms": round(canceled_ms, 3),
            "canceled_refresh_samples": 1,
            "canceled_refresh_exit_code": child.returncode,
            "concurrent_read_samples": len(read_samples),
            "concurrent_read_latency_ms": latency_summary(read_samples),
            "concurrent_read_errors": 0,
            "canceled_scan_preserved_previous_content": True}


def bundle_boundary_profile(binary, data_dir, source_id, deadline):
    """Preview exactly 2 MiB of quoted JSON, then reject one byte beyond it.

    Uses the harness's existing synthetic source and management API. Nothing is
    imported, and the report contains sizes/timings only, never quotes or claims.
    """
    with sqlite3.connect((data_dir / "findrail.db").as_uri() + "?mode=ro", uri=True) as db:
        path, media, content_hash, body = db.execute("SELECT path,media_type,content_hash,content FROM documents WHERE path='doc-000000.md'").fetchone()
    quote = body[100:100 + 65536]
    if len(quote.encode()) != 65536: raise RuntimeError("boundary fixture must contain a full ASCII window")
    entry = {"source_alias": "s1", "relative_path": path, "media_type": media, "content_hash": content_hash,
             "page": 0, "start_char": 100, "text": quote, "note": ""}
    bundle = {"format": "findrail-evidence-bundle", "version": 1, "title": "Synthetic boundary",
              "sources": [{"alias": "s1", "kind": "filesystem", "display_name": "Synthetic"}], "entries": [entry] * 32}
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0)); port = sock.getsockname()[1]
    url = f"http://127.0.0.1:{port}"
    server = subprocess.Popen([str(binary), "start", "--no-open", "--data-dir", str(data_dir), "--addr", f"127.0.0.1:{port}"],
                              stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        while True:
            if server.poll() is not None: raise RuntimeError("boundary management server exited")
            try:
                with urllib.request.urlopen(url + "/api/v1/session", timeout=1) as response: token = json.load(response)["token"]
                break
            except OSError:
                remaining_timeout(deadline, 1); time.sleep(.05)
        payload = json.dumps({"bundle": bundle, "mappings": {"s1": source_id}}, separators=(",", ":")).encode()
        headers = {"Origin": url, "Content-Type": "application/json", "X-Findrail-Token": token, "Sec-Fetch-Site": "same-origin"}
        started = time.perf_counter()
        request = urllib.request.Request(url + "/api/v1/collections/import/preview", data=payload, headers=headers)
        with urllib.request.urlopen(request, timeout=remaining_timeout(deadline, 15)) as response: raw = response.read((256 << 10) + 1)
        elapsed = (time.perf_counter() - started) * 1000
        result = json.loads(raw)
        if len(raw) > 256 << 10 or len(result["entries"]) != 32 or any(e["status"] != "quota_conflict" for e in result["entries"]):
            raise RuntimeError("codec boundary did not report the collection's stricter retained-text quota")
        # A retained entry contains both evidence text and excerpt. Thus 1 MiB
        # of quotes reaches the existing collection's 2 MiB text quota; 2 MiB
        # is valid codec input but cannot fit that collection. Keep both limits.
        accepted_bundle = dict(bundle, entries=[entry] * 16)
        accepted_payload = json.dumps({"bundle": accepted_bundle, "mappings": {"s1": source_id}}, separators=(",", ":")).encode()
        request = urllib.request.Request(url + "/api/v1/collections/import/preview", data=accepted_payload, headers=headers)
        with urllib.request.urlopen(request, timeout=remaining_timeout(deadline, 15)) as response:
            accepted = json.loads(response.read((256 << 10) + 1))
        if len(accepted["entries"]) != 16 or any(e["status"] != "match" for e in accepted["entries"]):
            raise RuntimeError("retained-text collection boundary was not previewed completely")
        bundle["entries"] = [entry] * 32 + [dict(entry, text="x", start_char=100)]
        rejected = json.dumps({"bundle": bundle, "mappings": {"s1": source_id}}, separators=(",", ":")).encode()
        request = urllib.request.Request(url + "/api/v1/collections/import/preview", data=rejected, headers=headers)
        try:
            urllib.request.urlopen(request, timeout=remaining_timeout(deadline, 15)).close()
            raise RuntimeError("JSON quote quota accepted one byte beyond the boundary")
        except urllib.error.HTTPError as error:
            status = error.code; error.close()
            if status != 413: raise RuntimeError("JSON quote quota returned an unexpected status")
        rss = server_rss_bytes(server.pid)
    finally:
        stop_process(server)
    with sqlite3.connect((data_dir / "findrail.db").as_uri() + "?mode=ro", uri=True) as db:
        if db.execute("SELECT count(*) FROM evidence_collections").fetchone()[0] != 0:
            raise RuntimeError("JSON preview persisted a collection")
    return {"codec_boundary_selected_text_bytes": 2 << 20, "codec_boundary_entries": 32,
            "codec_boundary_status": "quota_conflict", "accepted_selected_text_bytes": 1 << 20, "accepted_entries": 16,
            "request_bytes": len(payload), "preview_response_bytes": len(raw),
            "preview_wall_ms": round(elapsed, 3), "one_byte_over_quota_status": status,
            "sampled_server_rss_bytes": rss, "persistent_collections_created": 0}


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--binary", required=True, type=Path)
    p.add_argument("--documents", required=True, type=int, choices=(100, 1000, 10000, 100000))
    p.add_argument("--seed", required=True, type=int)
    p.add_argument("--queries", type=int, default=5)
    p.add_argument("--report", required=True, type=Path)
    p.add_argument("--samples-report", type=Path, help="optional content-free warm HTTP sample sidecar")
    p.add_argument("--compare", type=Path, help="optional second prepared binary; rerun identical seed/workload on this host")
    p.add_argument("--source-cycle-boundaries", action="store_true", help="measure cursor pages and the JSON quote boundary on a source candidate")
    p.add_argument("--allow-large", action="store_true", help="required for 100000-document profile")
    a = p.parse_args()
    if a.documents == 100000 and not a.allow_large: p.error("100000 documents requires --allow-large")
    if not 1 <= a.queries <= 100: p.error("--queries must be 1..100")
    binary = a.binary.resolve(strict=True)
    deadline = time.monotonic() + min(MAX_SECONDS, 300 if a.documents <= 1000 else MAX_SECONDS)
    report = {"schema": 1, "fixture": {"version": FIXTURE_VERSION, "seed": a.seed, "documents_requested": a.documents},
              "binary": {"sha256": digest_file(binary)}, "environment": {"os": platform.system(), "arch": platform.machine(), "processor": host_processor(), "python": platform.python_version()},
              "metrics": {}, "errors": [], "unavailable": ["index_peak_rss: not measured"]}
    try:
        with tempfile.TemporaryDirectory(prefix="findrail-resource-") as temp:
            base = Path(temp); source = base / "source"; data_dir = base / "data"; source.mkdir(); data_dir.mkdir()
            extracted_bytes = generate(source, a.documents, a.seed, deadline)
            report["fixture"].update(generated_text_input_bytes=extracted_bytes,
                                     generated_source_file_bytes=directory_file_bytes(source),
                                     corpus_sha256=digest_file_manifest(source))
            t = time.perf_counter()
            indexed = run([str(binary), "index", "--json", "--data-dir", str(data_dir), "--max-bytes", str(1<<20), "--max-pdf-bytes", str(1<<20), str(source)], remaining_timeout(deadline, MAX_SECONDS))
            index_json = json.loads(indexed.stdout)
            report["workload"] = {"indexed_documents": int(index_json.get("seen", 0)), "updated_documents": int(index_json.get("updated", 0)), "unchanged_documents": int(index_json.get("unchanged", 0)), "skipped_documents": int(index_json.get("skipped", 0)), "synthetic_pdf_pages": a.documents // 4}
            if report["workload"]["indexed_documents"] != a.documents:
                raise RuntimeError("incomplete indexed corpus; baseline rejected")
            report["metrics"]["index_wall_ms"] = round((time.perf_counter()-t)*1000, 3)
            # Capture these files at the stated lifecycle point, before doctor
            # and any additional SQLite connection can affect WAL/SHM files.
            sizes = database_sizes(data_dir)
            report["metrics"]["database_files_after_index_bytes"] = sizes
            report["metrics"]["database_bytes_after_index_before_search"] = sum(sizes.values())
            ver = run([str(binary), "doctor", "--json", "--data-dir", str(data_dir)], remaining_timeout(deadline, 10))
            doctor = json.loads(ver.stdout)
            report["binary"].update(version=str(doctor.get("version", "unknown"))[:80], revision=str(doctor.get("revision", "unknown"))[:80], go_version=str(doctor.get("go_version", "unknown"))[:40])
            with sqlite3.connect((data_dir / "findrail.db").as_uri() + "?mode=ro", uri=True) as db:
                report["binary"]["schema"] = int(db.execute("PRAGMA user_version").fetchone()[0])
                report["workload"]["extracted_text_bytes"] = int(db.execute("SELECT coalesce(sum(length(CAST(content AS BLOB))), 0) FROM documents").fetchone()[0])
            latencies = []
            result_counts = []
            rss_samples = []
            expected_counts = {"common": a.documents // 2, "rare": (a.documents - 1) // 97 + 1}
            per_query = {}
            query_samples = {}
            with socket.socket() as sock:
                sock.bind(("127.0.0.1", 0)); port = sock.getsockname()[1]
            base_url = f"http://127.0.0.1:{port}"
            remaining_timeout(deadline, MAX_SECONDS)
            server = subprocess.Popen([str(binary), "serve", "--no-sync", "--data-dir", str(data_dir), "--addr", f"127.0.0.1:{port}"], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, shell=False)
            try:
                startup = time.perf_counter()
                deadline_ready = min(deadline, time.monotonic() + 30)
                while True:
                    if server.poll() is not None: raise RuntimeError("server exited before becoming ready")
                    try:
                        with urllib.request.urlopen(base_url + "/healthz", timeout=1) as response:
                            if response.status == 200: break
                    except Exception:
                        if time.monotonic() >= deadline_ready: raise TimeoutError("loopback server startup timed out")
                report["metrics"]["server_startup_ms"] = round((time.perf_counter()-startup)*1000, 3)
                for qid, query in QUERIES.items():
                    query_latencies = []
                    warm_samples = []
                    # The first request for each query warms it; it is excluded
                    # from the subsequent --queries warm latency samples.
                    for n in range(a.queries + 1):
                        url = base_url + "/api/v1/search?" + urllib.parse.urlencode({"q": query, "limit": "5"})
                        t = time.perf_counter()
                        with urllib.request.urlopen(url, timeout=remaining_timeout(deadline, 60)) as response:
                            output = response.read(1 << 20)
                            if len(output) >= (1 << 20): raise RuntimeError("search response exceeded harness read bound")
                        latency = (time.perf_counter()-t)*1000
                        if n > 0:
                            query_latencies.append(latency)
                            warm_samples.append(round(latency, 3))
                            latencies.append(latency)
                        obj = json.loads(output)
                        count = int(obj.get("total", 0))
                        result_counts.append(count)
                        if count != expected_counts[qid] or len(obj.get("results", [])) != min(5, count):
                            raise RuntimeError("synthetic query returned an unexpected result shape")
                        rss = server_rss_bytes(server.pid)
                        if rss is not None:
                            rss_samples.append(rss)
                        if n == 0 and qid == "common":
                            report["metrics"]["first_query_after_server_start_ms"] = round(latency, 3)
                            report["metrics"]["bounded_search_response_bytes"] = len(output)
                            results = obj.get("results", [])
                            if results:
                                evidence_url = base_url + "/api/v1/documents/" + urllib.parse.quote(str(results[0]["id"]), safe="")
                                with urllib.request.urlopen(evidence_url, timeout=remaining_timeout(deadline, 30)) as evidence:
                                    evidence_body = evidence.read(1 << 20)
                                    if len(evidence_body) >= (1 << 20): raise RuntimeError("evidence response exceeded harness read bound")
                                report["metrics"]["bounded_evidence_response_bytes"] = len(evidence_body)
                            else:
                                raise RuntimeError("synthetic common query produced no evidence")
                    per_query[qid] = {"repeats": len(query_latencies), "latency_ms": latency_summary(query_latencies)}
                    query_samples[qid] = warm_samples
                if a.source_cycle_boundaries:
                    report["metrics"]["paging_window"] = paging_profile(base_url, expected_counts["common"], deadline)
                    if a.documents >= 10000:
                        report["metrics"]["refresh_cycle"] = refresh_profile(
                            binary, data_dir, source, base_url, expected_counts["common"], deadline)
                    else:
                        report["metrics"]["refresh_cycle"] = {
                            "status": "not_measured", "reason": "long-session refresh requires 10000+ documents"}
            finally:
                stop_process(server)
            if a.source_cycle_boundaries:
                report["metrics"]["evidence_bundle_boundary"] = bundle_boundary_profile(binary, data_dir, index_json["source"]["id"], deadline)
            report["metrics"].update(search_repeats=len(latencies), warm_http_request_latency_ms=latency_summary(latencies), warm_queries=per_query, synthetic_query_ids=list(QUERIES), known_result_shape=bool(result_counts and all(x > 0 for x in result_counts)), result_count_min=min(result_counts), result_count_max=max(result_counts))
            if rss_samples:
                report["metrics"]["max_sampled_server_rss_bytes"] = max(rss_samples)
                report["metrics"]["server_rss_sample_count"] = len(rss_samples)
            else:
                report["unavailable"].append("server_rss: Linux procfs unavailable or unsupported platform")
            report["lifecycle"] = {"database_size": "immediately after index process exit, before doctor and server startup; separate DB+WAL+SHM file sizes", "startup": "fresh loopback server process until health endpoint responds", "first_query": "first HTTP query after server startup; not a cold OS cache", "latency": "--queries requests per query after one excluded warmup per query; one persistent loopback server", "rss": "Linux /proc/server-pid/status VmRSS after each query; sampled maximum, not process peak or index/PDF-worker RSS", "response_bytes": "bounded HTTP search and evidence JSON payloads, read with a 1 MiB cap"}
        if a.compare:
            other = a.compare.resolve(strict=True)
            with tempfile.TemporaryDirectory(prefix="findrail-resource-compare-") as temp:
                other_report = Path(temp) / "report.json"
                run([os.sys.executable, str(Path(__file__).resolve()), "--binary", str(other), "--documents", str(a.documents), "--seed", str(a.seed), "--queries", str(a.queries), "--report", str(other_report)] + (["--allow-large"] if a.documents == 100000 else []) + (["--source-cycle-boundaries"] if a.source_cycle_boundaries else []), remaining_timeout(deadline, MAX_SECONDS))
                measured = json.loads(other_report.read_text(encoding="utf-8"))
            if measured.get("status") != "success" or measured.get("fixture", {}).get("corpus_sha256") != report["fixture"]["corpus_sha256"]:
                raise RuntimeError("comparison workload differs or second run was incomplete")
            report["comparison"] = {"same_host": True, "binary": measured["binary"], "metrics": measured["metrics"], "fixture_sha256_matches": True}
            report["status"] = "success"
    except BaseException as exc:
        report["status"] = "failed"
        reason = str(exc)[:160] if isinstance(exc, (RuntimeError, ValueError, TimeoutError)) else "operation failed; command details are intentionally omitted"
        report["errors"] = [type(exc).__name__ + ": " + reason]
        print("Resource check failed; see the content-free report for its status.", file=sys.stderr)
        return 1
    finally:
        a.report.parent.mkdir(parents=True, exist_ok=True)
        a.report.write_text(json.dumps(report, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
        if a.samples_report:
            a.samples_report.parent.mkdir(parents=True, exist_ok=True)
            sample_status = "success" if not report.get("errors") and len(locals().get("query_samples", {})) == len(QUERIES) else "failed"
            sidecar = {"schema": 1, "kind": "search_warm_samples", "status": sample_status,
                       "binary": report.get("binary", {}), "fixture": report.get("fixture", {}),
                       "units": "ms", "method": "nearest-rank percentile: sorted[ceil(p*n)-1]; first request per family excluded",
                       "warmup": {"excluded": True, "count_per_family": 1},
                       "families": {key: {"samples": values, "count": len(values),
                                          "p50": latency_summary(values)["p50"] if values else None,
                                          "p95": latency_summary(values)["p95"] if values else None}
                                    for key, values in locals().get("query_samples", {}).items()}}
            a.samples_report.write_text(json.dumps(sidecar, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")


def digest_file_manifest(root):
    h = hashlib.sha256()
    for path in sorted(root.iterdir()):
        h.update(path.name.encode()); h.update(b"\0")
        with path.open("rb") as f:
            for block in iter(lambda: f.read(65536), b""): h.update(block)
    return h.hexdigest()


def canceled(signum, frame):
    raise KeyboardInterrupt("harness canceled")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, canceled)
    sys.exit(main())
