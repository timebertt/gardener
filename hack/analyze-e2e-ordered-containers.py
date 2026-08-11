#!/usr/bin/env python3
# SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
#
# SPDX-License-Identifier: Apache-2.0

"""Analyze execution times of Ginkgo Ordered containers from Prow e2e junit reports.

The script fetches junit.xml artifacts from the latest N successful runs of a
Prow job, sums the durations of the individual [It] specs that belong to each
Ordered container, and writes aggregated statistics per container to a CSV file.

Supports both CI jobs (prefix "ci-") and PR jobs (prefix "pull-"). The history
and artifact URLs are derived automatically from the job name.

Important caveats:
- The reported durations are sums of individual [It] testcase times as reported
  by Ginkgo. The CI runs tests in parallel (ParallelTotal: 5), so these sums do
  NOT represent wall-clock container execution time.
- [BeforeAll], [AfterAll] and other setup/teardown nodes are not represented as
  dedicated testcases in the junit report and are therefore not included in the
  container totals.
- Skipped testcases are excluded by default because their reported time is 0.
"""

import argparse
import csv
import json
import re
import shutil
import statistics
import sys
import tempfile
import urllib.error
import urllib.request
from pathlib import Path
from xml.etree import ElementTree


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def parse_args():
    parser = argparse.ArgumentParser(
        description="Analyze Ginkgo Ordered container execution times from Prow junit reports.",
    )
    parser.add_argument(
        "--job-name",
        default="ci-gardener-e2e-kind",
        help="Prow job name to analyze (default: %(default)s). Jobs starting with 'pull-' are treated as PR jobs.",
    )
    parser.add_argument(
        "--count",
        type=int,
        default=10,
        help="Number of latest successful runs to analyze (default: %(default)s).",
    )
    parser.add_argument(
        "--output",
        default="e2e-ordered-container-stats.csv",
        help="Output CSV file path (default: %(default)s).",
    )
    parser.add_argument(
        "--source-root",
        default=None,
        help="Path to the Gardener repository root. Defaults to the parent of the directory containing this script.",
    )
    parser.add_argument(
        "--include-skipped",
        action="store_true",
        help="Include skipped testcases in duration sums. By default they are excluded.",
    )
    parser.add_argument(
        "--keep-junit",
        action="store_true",
        help="Keep downloaded junit.xml files in the temporary directory instead of deleting them.",
    )
    parser.add_argument(
        "--build-id",
        nargs="+",
        default=None,
        help="Specific Prow build IDs to analyze. If set, --count is ignored.",
    )
    return parser.parse_args()


# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------

def fetch_text(url, timeout=60):
    with urllib.request.urlopen(url, timeout=timeout) as response:
        return response.read().decode("utf-8")


def fetch_file(url, dest, timeout=120):
    with urllib.request.urlopen(url, timeout=timeout) as response:
        with open(dest, "wb") as f:
            f.write(response.read())


# ---------------------------------------------------------------------------
# Prow history parsing
# ---------------------------------------------------------------------------

ALL_BUILDS_RE = re.compile(r"var allBuilds\s*=\s*(\[.*?\]);", re.DOTALL)
OLDER_LINK_RE = re.compile(r'href="(/job-history/[^"]+)"[^>]*>&lt;- Older Runs</a>')


def is_pr_job(job_name):
    """Return True if the job name follows the Prow PR naming convention."""
    return job_name.startswith("pull-")


def job_history_url(job_name):
    """Return the Prow job-history URL for a CI or PR job name."""
    if is_pr_job(job_name):
        return f"https://prow.gardener.cloud/job-history/gs/gardener-prow/pr-logs/directory/{job_name}"
    return f"https://prow.gardener.cloud/job-history/gs/gardener-prow/logs/{job_name}"


def artifact_url(spyglass_link):
    """Convert a Prow spyglass link into a direct GCS artifact base URL.

    The junit.xml artifact is appended by the caller.
    """
    return spyglass_link.replace("/view/gs/", "https://storage.googleapis.com/").rstrip("/")


def extract_successful_builds(job_name, count):
    """Parse the Prow job-history HTML page(s) and return build records.

    Each record contains at least 'id' and 'spyglass_link'. PR jobs also
    include the originating pull request number under 'pr'.
    """
    builds = []
    url = job_history_url(job_name)

    while url and len(builds) < count:
        page = fetch_text(url)

        match = ALL_BUILDS_RE.search(page)
        if not match:
            raise RuntimeError(f"Could not find allBuilds JSON in {url}")

        page_builds = json.loads(match.group(1))
        for build in page_builds:
            if build.get("Result") == "SUCCESS":
                record = {
                    "id": build["ID"],
                    "spyglass_link": build["SpyglassLink"],
                }
                refs = build.get("Refs") or {}
                pulls = refs.get("pulls") or []
                if pulls:
                    record["pr"] = pulls[0].get("number")
                builds.append(record)
            if len(builds) >= count:
                break

        older_match = OLDER_LINK_RE.search(page)
        url = f"https://prow.gardener.cloud{older_match.group(1)}" if older_match else None

        if url and len(builds) < count:
            print(f"Following older runs page: {url}")

    if not builds:
        raise RuntimeError(f"Could not find any successful build records for job {job_name}")

    return builds[:count]


# ---------------------------------------------------------------------------
# junit.xml parsing
# ---------------------------------------------------------------------------

LABEL_BLOCK_RE = re.compile(r"\s*\[([^\]]+)\]\s*$")
IT_PREFIX_RE = re.compile(r"^\[It\]\s*")
SYSTEM_ERR_IT_RE = re.compile(
    r"[>\[]\s*Enter\s+\[It\]\s+(.+?)\s+-\s+/.*?:\d+\s+@"
)


def extract_labels(name):
    """Extract and return the trailing Ginkgo label block(s) as a sorted list.

    Input:  "... Create Shoot [Shoot, default, basic]"
    Output: ["Shoot", "default", "basic"]
    """
    labels = set()
    while True:
        match = LABEL_BLOCK_RE.search(name)
        if not match:
            break
        labels.update(label.strip() for label in match.group(1).split(","))
        name = name[: match.start()]
    return sorted(labels)


def strip_labels(name):
    """Remove the trailing label block(s) (e.g. ' [Seed, default]')."""
    while True:
        match = LABEL_BLOCK_RE.search(name)
        if not match:
            break
        name = name[: match.start()]
    return name.strip()


def extract_it_description_from_system_err(system_err_text):
    """Return the [It] description from the system-err block, or None."""
    if not system_err_text:
        return None
    match = SYSTEM_ERR_IT_RE.search(system_err_text)
    if match:
        return match.group(1).strip()
    return None


def infer_container_path(testcase_name, system_err_text):
    """Return the containing container path for a testcase.

    The testcase name looks like:
      [It] Shoot Tests Create Hibernate Wake up and Delete Shoot Shoot with workers Create Shoot [Shoot, default, basic]

    The system-err block contains a line like:
      > Enter [It] Create Shoot - /path/to/file.go:line @ ...

    We strip the [It] prefix and labels, then remove the exact [It] description
    to obtain the container path.
    """
    name = strip_labels(testcase_name)
    name = IT_PREFIX_RE.sub("", name)
    name = name.strip()

    it_desc = extract_it_description_from_system_err(system_err_text)
    if not it_desc:
        return None

    if name.endswith(it_desc):
        container = name[: -len(it_desc)]
        return container.strip()

    return None


def parse_junit_file(path):
    """Parse a junit.xml file and yield (container_path, labels, duration_seconds)."""
    tree = ElementTree.parse(path)
    root = tree.getroot()

    for testcase in root.iter("testcase"):
        status = testcase.get("status", "")
        if status == "skipped" and not args_include_skipped:
            continue

        name = testcase.get("name", "")
        time_str = testcase.get("time", "0") or "0"
        try:
            duration = float(time_str)
        except ValueError:
            duration = 0.0

        labels = extract_labels(name)

        system_err_elem = testcase.find("system-err")
        system_err = system_err_elem.text if system_err_elem is not None else ""

        container = infer_container_path(name, system_err)
        if container is None:
            container = infer_container_path_fallback(name, labels)

        if container is None:
            continue

        yield container, labels, duration


def infer_container_path_fallback(name, labels):
    """Fallback when system-err is missing. Not very reliable."""
    name = strip_labels(name)
    name = IT_PREFIX_RE.sub("", name)
    name = name.strip()

    # Ginkgo It descriptions are rarely more than ~12 words. As a crude
    # heuristic, assume the last 1-8 words are the It description and return
    # the longest viable prefix that matches a known container. The labels are
    # used to help disambiguate duplicate container paths.
    parts = name.split()
    key_prefix = (None, None)
    for it_words in range(1, min(12, len(parts))):
        candidate = " ".join(parts[:-it_words])
        key = (candidate, frozenset(labels))
        if key in known_containers:
            return candidate
    return None


# ---------------------------------------------------------------------------
# Source-tree discovery of Ordered containers
# ---------------------------------------------------------------------------

# Matches Ginkgo v2 container declarations that use an anonymous func literal
# as their body, e.g.
#   Describe("Seed Tests", Label("Seed", "default"), Ordered, func() {
#   Context("Shoot with workers", Label("basic"), Ordered, func(ctx SpecContext) {
CONTAINER_CALL_RE = re.compile(
    r"\b(Describe|Context|When)\s*\(\s*"
    r'("(?:\\.|[^"\\])*"|`(?:\\.|[^`\\])*`)'
    r"(?P<args>(?:\s*,\s*(?:Ordered|Label\s*\([^)]*\)|[A-Za-z_][A-Za-z0-9_]*))*)"
    r"\s*,\s*func\s*\([^)]*\)\s*\{",
    re.DOTALL,
)

LABEL_CALL_RE = re.compile(
    r"Label\s*\(\s*(?P<labels>[^)]*)\s*\)",
    re.DOTALL,
)

STRING_LITERAL_RE = re.compile(
    r'"(?:\\.|[^"\\])*"|`(?:\\.|[^`\\])*`',
    re.DOTALL,
)
LINE_COMMENT_RE = re.compile(r"//.*?$")
BLOCK_COMMENT_RE = re.compile(r"/\*.*?\*/", re.DOTALL)
ORDERED_RE = re.compile(r"\bOrdered\b")


def mask_strings_and_comments(source):
    """Return a copy of source where string literals and comments are replaced by spaces.

    The returned string has the same length and (crucially) the same brace
    positions as the original, so that brace-depth counting is not confused by
    braces embedded in strings or comments.
    """
    masked = list(source)

    for match in BLOCK_COMMENT_RE.finditer(source):
        for i in range(match.start(), match.end()):
            if source[i] not in "\r\n":
                masked[i] = " "

    for match in LINE_COMMENT_RE.finditer(source):
        for i in range(match.start(), match.end()):
            if source[i] not in "\r\n":
                masked[i] = " "

    for match in STRING_LITERAL_RE.finditer(source):
        for i in range(match.start(), match.end()):
            if source[i] not in "\r\n":
                masked[i] = " "

    return "".join(masked)


def parse_go_string_literal(token):
    """Unescape a Go double-quoted or backtick raw string literal."""
    if token.startswith("`"):
        return token[1:-1]
    # Double-quoted string: strip surrounding quotes and unescape common escapes.
    value = token[1:-1]
    value = value.replace('\\"', '"').replace("\\n", "\n").replace("\\t", "\t").replace("\\\\", "\\")
    return value


def parse_label_args(args_text):
    """Parse a Label(...) call body and return the list of string label values."""
    labels = []
    for match in STRING_LITERAL_RE.finditer(args_text):
        labels.append(parse_go_string_literal(match.group(0)))
    return labels


def extract_container_labels(args_text):
    """Return the set of label values declared for a single container call."""
    labels = set()
    for match in LABEL_CALL_RE.finditer(args_text):
        labels.update(parse_label_args(match.group("labels")))
    return labels


def find_ordered_containers_in_file(source):
    """Return a list of (container_path, inherited_labels) for each Ordered container.

    The caller provides the relative path separately.
    """
    masked = mask_strings_and_comments(source)

    # Find all container declarations with the index of their opening body brace.
    matches = []
    for match in CONTAINER_CALL_RE.finditer(source):
        keyword = match.group(1)
        title = parse_go_string_literal(match.group(2))
        args = match.group("args")
        is_ordered = ORDERED_RE.search(args) is not None
        own_labels = extract_container_labels(args)
        # The regex match ends exactly at the opening '{' of the func body.
        brace_index = match.end() - 1
        matches.append((brace_index, keyword, title, is_ordered, own_labels))

    matches.sort(key=lambda item: item[0])
    match_iter = iter(matches)
    next_match = next(match_iter, None)

    ordered = []
    # stack entries: (entry_depth, title, inherited_labels)
    stack = []
    brace_depth = 0

    for i, char in enumerate(masked):
        while next_match is not None and next_match[0] == i:
            entry_depth = brace_depth + 1
            title = next_match[2]
            is_ordered = next_match[3]
            own_labels = next_match[4]
            parent_labels = stack[-1][2] if stack else set()
            inherited_labels = parent_labels | own_labels
            stack.append((entry_depth, title, inherited_labels))
            if is_ordered:
                path = " ".join(item[1] for item in stack)
                ordered.append((path, inherited_labels))
            next_match = next(match_iter, None)

        if char == "{":
            brace_depth += 1
        elif char == "}":
            brace_depth -= 1
            while stack and stack[-1][0] > brace_depth:
                stack.pop()

    return ordered


def discover_ordered_containers(source_root, e2e_dir):
    """Scan the e2e directory for Ordered containers and return a dict mapping
    (container path, label frozenset) -> relative source file path.
    """
    mapping = {}
    e2e_path = Path(source_root) / e2e_dir
    if not e2e_path.exists():
        raise RuntimeError(f"E2E source directory not found: {e2e_path}")

    for go_file in e2e_path.rglob("*.go"):
        relative = go_file.relative_to(Path(source_root))
        try:
            source = go_file.read_text(encoding="utf-8")
        except OSError as exc:
            print(f"Warning: failed to read {relative}: {exc}", file=sys.stderr)
            continue

        ordered_paths = find_ordered_containers_in_file(source)
        for container_path, labels in ordered_paths:
            key = (container_path, frozenset(labels))
            if key in mapping and mapping[key] != str(relative):
                print(
                    f"Warning: duplicate ordered container '{container_path}' with labels "
                    f"[{', '.join(sorted(labels))}] in {relative} "
                    f"(already found in {mapping[key]})",
                    file=sys.stderr,
                )
                continue
            mapping[key] = str(relative)

    return mapping


# ---------------------------------------------------------------------------
# Statistics aggregation
# ---------------------------------------------------------------------------

def percentile(values, pct):
    if not values:
        return 0.0
    if len(values) == 1:
        return values[0]
    k = (len(values) - 1) * (pct / 100.0)
    f = int(k)
    c = f + 1 if f + 1 < len(values) else f
    if f == c:
        return values[f]
    return values[f] + (values[c] - values[f]) * (k - f)


def aggregate(container_durations):
    """Compute statistics from a list of durations."""
    values = sorted(container_durations)
    return {
        "count": len(values),
        "mean_seconds": statistics.mean(values) if values else 0.0,
        "min_seconds": values[0] if values else 0.0,
        "max_seconds": values[-1] if values else 0.0,
        "p50_seconds": percentile(values, 50),
        "p95_seconds": percentile(values, 95),
    }


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def make_download_dir():
    """Create a temporary directory for downloaded junit.xml files.

    The directory is created outside the source tree so that it is never tracked
    by git and is guaranteed to be on a writable volume.
    """
    return Path(tempfile.mkdtemp(prefix="gardener-junit-analysis."))


def main():
    global args_include_skipped, known_containers

    args = parse_args()
    args_include_skipped = args.include_skipped

    if args.source_root:
        source_root = Path(args.source_root).resolve()
    else:
        source_root = Path(__file__).resolve().parent.parent

    print(f"Using source root: {source_root}")

    print("Discovering Ordered containers in source tree...")
    known_containers = discover_ordered_containers(source_root, "test/e2e/gardener")
    print(f"Found {len(known_containers)} Ordered containers.")

    if args.build_id:
        build_records = [{"id": bid, "spyglass_link": ""} for bid in args.build_id]
    else:
        print(f"Fetching latest {args.count} successful runs of {args.job_name}...")
        build_records = extract_successful_builds(args.job_name, args.count)

    build_ids = [b["id"] for b in build_records]
    print(f"Analyzing runs: {', '.join(build_ids)}")

    download_dir = make_download_dir()
    print(f"Using temporary download directory: {download_dir}")
    try:
        per_run_container_durations = {}  # key -> list of (build_id, total_seconds)
        container_metadata = {}  # key -> (container_path, file, labels)

        for build in build_records:
            build_id = build["id"]
            if build.get("spyglass_link"):
                junit_url = f"{artifact_url(build['spyglass_link'])}/artifacts/junit.xml"
            else:
                # Fall back for manually supplied build IDs.
                if is_pr_job(args.job_name):
                    raise RuntimeError(
                        "Cannot construct artifact URL for PR job from --build-id alone; "
                        "spyglass link is required. Provide a full spyglass link or omit --build-id."
                    )
                junit_url = (
                    f"https://storage.googleapis.com/gardener-prow/logs/{args.job_name}/{build_id}/artifacts/junit.xml"
                )
            local_path = download_dir / f"{build_id}-junit.xml"

            try:
                fetch_file(junit_url, local_path)
            except urllib.error.HTTPError as exc:
                print(f"Warning: could not download {junit_url}: {exc.code}", file=sys.stderr)
                continue

            run_totals = {}
            for container, labels, duration in parse_junit_file(local_path):
                key = (container, frozenset(labels))
                if key not in known_containers:
                    continue
                run_totals[key] = run_totals.get(key, 0.0) + duration
                container_metadata[key] = (
                    container,
                    known_containers[key],
                    labels,
                )

            for key, total in run_totals.items():
                per_run_container_durations.setdefault(key, []).append((build_id, total))

        if not per_run_container_durations:
            print("No Ordered container durations found. Exiting.", file=sys.stderr)
            sys.exit(1)

        rows = []
        for key, values in per_run_container_durations.items():
            container, file_path, labels = container_metadata[key]
            stats = aggregate([duration for _, duration in values])
            rows.append(
                {
                    "container": container,
                    "labels": ", ".join(sorted(labels)),
                    "file": file_path,
                    **stats,
                }
            )

        # Sort by mean execution time descending.
        rows.sort(key=lambda r: r["mean_seconds"], reverse=True)

        output_path = Path(args.output).resolve()
        output_path.parent.mkdir(parents=True, exist_ok=True)
        with open(output_path, "w", newline="") as f:
            fieldnames = [
                "container",
                "labels",
                "file",
                "count",
                "mean_seconds",
                "min_seconds",
                "max_seconds",
                "p50_seconds",
                "p95_seconds",
            ]
            writer = csv.DictWriter(f, fieldnames=fieldnames)
            writer.writeheader()
            writer.writerows(rows)

        print(f"Wrote CSV to {output_path.resolve()}")

        if args.keep_junit:
            print(f"Downloaded junit files kept in {download_dir}")
        else:
            shutil.rmtree(download_dir, ignore_errors=True)

    except Exception:
        shutil.rmtree(download_dir, ignore_errors=True)
        raise


if __name__ == "__main__":
    main()
