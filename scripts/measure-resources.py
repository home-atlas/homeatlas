#!/usr/bin/env python3
"""Compare a production binary's Core/Documents modes using disposable homes.

Python standard library only; macOS/Linux, ps RSS in KiB. No existing data is read.
Requires permission to open loopback sockets and inspect the child process.
"""

import argparse
import base64
import concurrent.futures
from contextlib import closing
import gzip
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import platform
import re
import socket
import sqlite3
import statistics
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def command(*args):
    return subprocess.check_output(args, text=True).strip()


def rss(pid):
    value = int(command("ps", "-o", "rss=", "-p", str(pid))) * 1024
    assert value > 0, "RSS unavailable"
    return value


def stop(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            raise RuntimeError("server did not shut down cleanly")
    assert process.returncode == 0, f"server exited with {process.returncode}"


def disk_bytes(directory):
    return sum(path.stat().st_size for path in directory.rglob("*") if path.is_file())


def samples(process, seconds):
    start = time.monotonic()
    result = []
    for index in range(5):
        time.sleep(max(0, start + seconds - 4 + index - time.monotonic()))
        result.append(rss(process.pid))
    return result


def measured_run(binary, module, idle_seconds, do_backup, backup_count=1):
    with tempfile.TemporaryDirectory(prefix="homeatlas-resources-") as temporary:
        directory = Path(temporary)
        data = directory / "data"
        jar = http.cookiejar.CookieJar()
        client = urllib.request.build_opener(
            urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(jar))
        env = {key: value for key, value in os.environ.items()
               if not key.startswith("HOMEATLAS_SMTP_")}
        env["HOMEATLAS_SMTP_ENABLED"] = "false"
        # Prevent an inherited tuning experiment from changing the comparison.
        for key in ("GOGC", "GOMEMLIMIT", "GOMAXPROCS", "GODEBUG"):
            env.pop(key, None)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        origin = f"http://127.0.0.1:{port}"

        def request(path, payload=None, headers=None):
            body = None if payload is None else json.dumps(payload).encode()
            request_headers = {"Origin": origin, **(headers or {})}
            if payload is not None:
                request_headers["Content-Type"] = "application/json"
            with client.open(urllib.request.Request(origin + path, data=body,
                             headers=request_headers), timeout=120) as response:
                return response.read()

        def start():
            before = time.perf_counter()
            process = subprocess.Popen(
                [str(binary), "-addr", f"127.0.0.1:{port}", "-public-url", origin,
                 "-data", str(data), "-modules", module], cwd=directory, env=env,
                stdout=subprocess.DEVNULL, stderr=log)
            try:
                deadline = time.monotonic() + 20
                while process.poll() is None and time.monotonic() < deadline:
                    try:
                        # A short timeout bounds polling error; no browser/render time.
                        with client.open(origin + "/api/health", timeout=.1) as response:
                            assert json.load(response)["status"] == "ok"
                        return process, (time.perf_counter() - before) * 1000
                    except (urllib.error.URLError, TimeoutError):
                        time.sleep(.005)
                raise RuntimeError("server failed readiness check; inspect the supplied binary")
            except BaseException:
                if process.poll() is None:
                    process.terminate()
                    process.wait(timeout=8)
                raise

        with (directory / "server.log").open("wb") as log:
            process, fresh_ms = start()
            try:
                # cwd is outside the repository: a development build cannot pass this.
                html = request("/").decode()
                asset = re.search(r'src="(/assets/[^\"]+\.js)"', html)
                assert asset and len(request(asset[1])) > 1000, "production assets not embedded"
                request("/api/setup", {
                    "code": (data / "setup-code").read_text().strip(),
                    "name": "Resource fixture", "username": "resource-fixture", "email": "",
                    "password": "Temporary resource fixture 2026",
                    "householdName": "Disposable resource fixture", "timezone": "UTC"})
                assert not json.loads(request("/api/auth/session"))["setupRequired"]
            finally:
                stop(process)

            with closing(sqlite3.connect(f"file:{data / 'homeatlas.db'}?mode=ro&immutable=1", uri=True)) as db:
                tables = {row[0] for row in db.execute("SELECT name FROM sqlite_schema WHERE type='table'")}
                assert ("documents" in tables) == (module == "documents")
                assert db.execute("SELECT count(*) FROM identity_memberships").fetchone()[0] == 1
            assert (data / "documents").exists() == (module == "documents")
            result = {"module": module, "fresh_start_ms": fresh_ms,
                      "data_bytes_after_setup_shutdown": disk_bytes(data),
                      "sqlite_bytes_after_setup_shutdown": (data / "homeatlas.db").stat().st_size}

            process, result["restart_ms"] = start()
            try:
                # The same persisted session and empty household exercise the normal APIs.
                assert json.loads(request("/api/auth/session"))["session"] is not None
                catalog = json.loads(request("/api/modules"))
                assert len(catalog) == (1 if module == "documents" else 0)
                if module == "documents":
                    assert json.loads(request("/api/documents")) == []
                result["idle_rss_samples_bytes"] = samples(process, idle_seconds)
                result["idle_rss_median_bytes"] = statistics.median(result["idle_rss_samples_bytes"])
                if do_backup:
                    def backup():
                        before = time.perf_counter()
                        archive = request("/api/household/backups", {}, {
                            "X-HomeAtlas-Backup-Version": "4",
                            "X-HomeAtlas-Backup-Password": base64.b64encode(
                                b"Disposable backup resource fixture 2026").decode()})
                        assert archive.startswith(b"age-encryption.org/v1\n"), "backup not encrypted"
                        return {"duration_ms": (time.perf_counter() - before) * 1000,
                                "archive_bytes": len(archive)}
                    result["backups"] = []
                    for _ in range(backup_count):
                        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
                            future = pool.submit(backup)
                            observed = [rss(process.pid)]
                            while not future.done():
                                time.sleep(.1)
                                observed.append(rss(process.pid))
                            measurement = {**future.result(), "rss_samples_bytes": observed,
                                           "sampled_peak_rss_bytes": max(observed)}
                        measurement["post_idle_rss_samples_bytes"] = samples(process, idle_seconds)
                        result["backups"].append(measurement)
                    # Preserve the original field for consumers of the baseline report.
                    result["backup"] = result["backups"][0]
            finally:
                stop(process)
        return result


def distribution(values):
    return {"median": statistics.median(values), "min": min(values), "max": max(values)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True, help="new JSON file, never overwritten")
    parser.add_argument("--runs", type=int, default=7)
    parser.add_argument("--idle-seconds", type=int, default=10)
    parser.add_argument("--backup", action="store_true", help="sample encrypted backups per mode in the final repetition")
    parser.add_argument("--backup-count", type=int, default=1,
                        help="consecutive backups per mode; requires --backup (default: 1)")
    args = parser.parse_args()
    if args.runs < 3 or args.idle_seconds < 5 or args.output.exists():
        parser.error("use at least 3 runs, idle >= 5 seconds, and a new output path")
    if args.backup_count < 1 or (args.backup_count != 1 and not args.backup):
        parser.error("--backup-count must be positive and requires --backup")
    binary = args.binary.resolve(strict=True)
    if platform.system() not in ("Darwin", "Linux"):
        parser.error("RSS sampler supports macOS/Linux only")
    data = binary.read_bytes()
    report = {
        "schema": 1, "completed": False,
        "measured_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "environment": {"os": platform.platform(), "architecture": platform.machine(),
                        "python": platform.python_version(), "cpu_count": os.cpu_count(),
                        "loadavg_start": os.getloadavg()},
        "artifact": {"sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
                     "gzip9_bytes": len(gzip.compress(data, compresslevel=9, mtime=0))},
        "method": {"runs_per_mode": args.runs, "idle_seconds": args.idle_seconds,
                   "readiness_poll_ms": 5, "rss_unit": "bytes (ps KiB * 1024)",
                   "fresh_scope": "Popen through HTTP health, new SQLite and all migrations",
                   "idle_scope": "restart, 1 owner, empty home, authenticated API reads, 5 RSS samples",
                   "cache": "one excluded warm-up per mode; no OS cache flush",
                   "backup_scope": "consecutive v4 encrypted snapshots per mode in the final repetition",
                   "backup_count_per_mode": args.backup_count if args.backup else 0,
                   "order": "alternating Core/Documents, one server at a time",
                   "smtp": False, "ai": False},
        "runs": []}
    del data
    # Warm-up is explicit and excluded; fresh-start measurements still apply migrations.
    for module in ("none", "documents"):
        measured_run(binary, module, 5, False)
        print(f"warm-up {module}: ok (excluded)", flush=True)
    for index in range(args.runs):
        modes = ("none", "documents") if index % 2 == 0 else ("documents", "none")
        for module in modes:
            row = measured_run(binary, module, args.idle_seconds, args.backup and index == args.runs - 1, args.backup_count)
            row["repetition"] = index + 1
            report["runs"].append(row)
            print(f"{index + 1}/{args.runs} {module}: fresh {row['fresh_start_ms']:.1f} ms, "
                  f"restart {row['restart_ms']:.1f} ms, RSS {row['idle_rss_median_bytes'] / 2**20:.2f} MiB", flush=True)
    report["summary"] = {}
    for module in ("none", "documents"):
        rows = [row for row in report["runs"] if row["module"] == module]
        assert len(rows) == args.runs
        report["summary"][module] = {
            field: distribution([row[field] for row in rows]) for field in
            ("fresh_start_ms", "restart_ms", "idle_rss_median_bytes",
             "data_bytes_after_setup_shutdown", "sqlite_bytes_after_setup_shutdown")}
    report["environment"]["loadavg_end"] = os.getloadavg()
    report["completed"] = True
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x") as out:
        json.dump(report, out, ensure_ascii=False, indent=2)
        out.write("\n")
    print(f"saved {args.output}", flush=True)


if __name__ == "__main__":
    main()
