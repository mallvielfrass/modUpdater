#!/usr/bin/env python3
import re
import sys
from pathlib import Path

VER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
LINE = re.compile(r'^(var Version = ")([^"]*)(")\s*$', re.M)


def die(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)


def main():
    path = Path(__file__).resolve().parent.parent / "server" / "internal" / "buildinfo" / "version.go"
    text = path.read_text()
    m = LINE.search(text)
    if not m or not VER.fullmatch(m.group(2)):
        die("версия не X.Y.Z")
    major, minor, patch = m.group(2).split(".")
    new = "%s.%s.%d" % (major, minor, int(patch) + 1)
    path.write_text(LINE.sub(lambda mm: mm.group(1) + new + mm.group(3), text, count=1))
    print("%s -> %s" % (m.group(2), new))


if __name__ == "__main__":
    main()
