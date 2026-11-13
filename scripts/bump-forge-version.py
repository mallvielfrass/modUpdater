#!/usr/bin/env python3
import re
import sys
from pathlib import Path

VER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
LINE = re.compile(r"^(mod_version=)([^\r\n]*)([ \t]*)(\r?\n?)$", re.M)


def die(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)


def main():
    path = Path(__file__).resolve().parent.parent / "forge-mod" / "gradle.properties"
    text = path.read_text()
    matches = list(LINE.finditer(text))
    if len(matches) != 1 or not VER.fullmatch(matches[0].group(2)):
        die("версия не X.Y.Z")
    current = matches[0].group(2)
    major, minor, patch = current.split(".")
    new = "%s.%s.%d" % (major, minor, int(patch) + 1)
    path.write_text(LINE.sub(lambda m: m.group(1) + new + m.group(3) + m.group(4), text, count=1))
    print("%s -> %s" % (current, new))


if __name__ == "__main__":
    main()
