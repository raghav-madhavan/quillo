"""Replace "Silo" with "Quillo" in user-visible web UI text.

Re-run after every upstream merge: on a conflict in a rebranded file, take
upstream's side and run this script again. Run from the repository root:

    python3 scripts/quillo-brand/rebrand_strings.py [--check]

It only touches string literals and JSX text in non-test web sources, and it
keeps references that name Silo itself rather than this server: protocol
headers (X-Silo-*), the Silo-Server GitHub org, and the official Silo apps,
which are still the clients people use with Quillo.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SOURCES = [ROOT / "web" / "src"]
EXTENSIONS = {".ts", ".tsx"}
SKIP_FILE = re.compile(r"(\.test\.|\.d\.ts$|/test/|/__tests__/)")

# A "Silo" that must stay: the next or previous text says it is Silo's own
# product, org, header or client.
KEEP_AFTER = re.compile(r"^(-Server|-Mutation|-Settings|-ChatGPT|-[A-Z]| app\b| apps\b| client|Remote|Mac|TV)")
KEEP_BEFORE = re.compile(r"(X-|official |the Silo-|/)$")

WORD = re.compile(r"\bSilo\b")
# Spans about services Silo itself runs (the shared skip-marker service, the
# push relay) keep the name: Quillo uses those services, it doesn't run them.
KEEP_SPAN = re.compile(r"marker|Push Relay", re.IGNORECASE)
# Header values are client identifiers the server may match on.
KEEP_LINE = re.compile(r"\"X-(Silo|Plex)-")
# string literals ("...", '...', `...`) and JSX text (>...<)
SPANS = re.compile(r"\"(?:[^\"\\\n]|\\.)*\"|'(?:[^'\\\n]|\\.)*'|`(?:[^`\\]|\\.)*`|>[^<>{}\n]*<")


def rebrand_span(span):
    if KEEP_SPAN.search(span):
        return span
    out, last = [], 0
    for m in WORD.finditer(span):
        before, after = span[: m.start()], span[m.end():]
        if KEEP_AFTER.match(after) or KEEP_BEFORE.search(before):
            continue
        out.append(span[last : m.start()])
        out.append("Quillo")
        last = m.end()
    out.append(span[last:])
    return "".join(out)


def rebrand(text):
    lines = text.split("\n")
    for i, line in enumerate(lines):
        stripped = line.lstrip()
        if stripped.startswith(("//", "*", "/*", "import ")) or KEEP_LINE.search(line):
            continue
        lines[i] = SPANS.sub(lambda m: rebrand_span(m.group(0)), line)
    return "\n".join(lines)


def main():
    check = "--check" in sys.argv
    changed = []
    for base in SOURCES:
        for path in sorted(base.rglob("*")):
            if path.suffix not in EXTENSIONS or SKIP_FILE.search(path.as_posix()):
                continue
            text = path.read_text(encoding="utf-8")
            new = rebrand(text)
            if new != text:
                changed.append(path.relative_to(ROOT))
                if not check:
                    path.write_text(new, encoding="utf-8")
    for p in changed:
        print(p)
    if check and changed:
        sys.exit(1)


if __name__ == "__main__":
    main()
