#!/usr/bin/env python3

import argparse
import csv
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
from datetime import datetime, timezone
from urllib.parse import urlparse


ROOT = Path(__file__).resolve().parents[1]


def read_cases(path):
    with path.open(newline="", encoding="utf-8") as file:
        reader = csv.DictReader(file)
        required = {"name", "url", "height", "transport"}

        if not required.issubset(set(reader.fieldnames or [])):
            raise ValueError(
                "CSV requires columns: name,url,height,transport"
            )

        cases = []
        names = set()

        for line, row in enumerate(reader, start=2):
            name = row["name"].strip()
            url = row["url"].strip()
            transport = row["transport"].strip()

            if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,79}", name):
                raise ValueError(f"line {line}: invalid case name")

            if name in names:
                raise ValueError(f"line {line}: duplicate name {name!r}")
            names.add(name)

            parsed = urlparse(url)
            if (
                parsed.scheme != "https"
                or parsed.hostname not in {
                    "youtube.com",
                    "www.youtube.com",
                    "m.youtube.com",
                    "youtu.be",
                }
                or parsed.username is not None
                or parsed.password is not None
            ):
                raise ValueError(f"line {line}: invalid YouTube URL")

            height = int(row["height"])
            if height <= 0:
                raise ValueError(f"line {line}: height must be positive")

            if transport not in {"http", "hls"}:
                raise ValueError(f"line {line}: invalid transport")

            cases.append({
                "name": name,
                "url": url,
                "height": height,
                "transport": transport,
            })

    if not cases:
        raise ValueError("CSV contains no cases")

    return cases


def run_case(command, log_path, timeout):
    # A separate process group lets cancellation also reach FFmpeg.
    with log_path.open("wb") as log:
        process = subprocess.Popen(
            command,
            cwd=ROOT,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )

        try:
            code = process.wait(timeout=timeout)
            return ("PASS" if code == 0 else "FAIL"), code
        except subprocess.TimeoutExpired:
            stop_process(process)
            return "TIMEOUT", process.returncode
        except KeyboardInterrupt:
            stop_process(process)
            raise


def stop_process(process):
    try:
        os.killpg(process.pid, signal.SIGINT)
    except ProcessLookupError:
        return

    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()


def main():
    parser = argparse.ArgumentParser(
        description="Run goyt YouTube compatibility cases."
    )
    parser.add_argument(
        "--cases",
        type=Path,
        default=ROOT / "testdata" / "youtube-cases.csv",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=1900,
        help="Maximum seconds per case; default 1900",
    )
    args = parser.parse_args()

    if args.timeout <= 0:
        parser.error("--timeout must be positive")

    cases = read_cases(args.cases)

    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    run_dir = ROOT / "compat-results" / stamp
    run_dir.mkdir(parents=True)

    binary = run_dir / "goyt"

    print("Building canonical CLI...", flush=True)
    subprocess.run(
        ["go", "build", "-o", str(binary), "./cmd/goyt"],
        cwd=ROOT,
        check=True,
    )

    report_path = run_dir / "results.csv"
    failures = 0

    fields = [
        "name",
        "url",
        "height",
        "transport",
        "status",
        "exit_code",
        "elapsed_seconds",
        "size_bytes",
        "playback_check",
        "log",
        "output",
    ]

    with report_path.open("w", newline="", encoding="utf-8") as file:
        writer = csv.DictWriter(file, fieldnames=fields)
        writer.writeheader()
        file.flush()

        for index, case in enumerate(cases, start=1):
            output = run_dir / f"{case['name']}.mp4"
            log = run_dir / f"{case['name']}.log"

            command = [
                str(binary),
                "download",
                "-url", case["url"],
                "-transport", case["transport"],
                "-height", str(case["height"]),
                "-out", str(output),
                "-decode-check",
            ]

            print(
                f"[{index}/{len(cases)}] {case['name']}",
                flush=True,
            )
            started = time.monotonic()
            interrupted = False

            try:
                status, code = run_case(command, log, args.timeout)
            except KeyboardInterrupt:
                status, code = "INTERRUPTED", ""
                interrupted = True

            size = output.stat().st_size if output.is_file() else 0

            # A zero exit code must also produce a nonempty output.
            if status == "PASS" and size == 0:
                status = "FAIL"

            if status != "PASS":
                failures += 1

            writer.writerow({
                **case,
                "status": status,
                "exit_code": code,
                "elapsed_seconds": round(time.monotonic() - started, 2),
                "size_bytes": size,
                "playback_check": "PENDING" if status == "PASS" else "N/A",
                "log": str(log),
                "output": str(output),
            })
            file.flush()

            print(f"  {status}; log: {log.name}", flush=True)

            if interrupted:
                print(f"\nInterrupted. Results retained at {report_path}")
                return 130

    print(f"\nResults: {report_path}")
    print(f"Passed: {len(cases) - failures}; failed/timed out: {failures}")
    print("Playback checks remain manual; inspect lip-sync and seeking.")
    return 1 if failures else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f"Compatibility runner: {error}", file=sys.stderr)
        sys.exit(1)