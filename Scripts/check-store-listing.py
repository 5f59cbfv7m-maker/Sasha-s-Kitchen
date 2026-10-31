#!/usr/bin/env python3
"""Проверяет лимиты App Store Connect в docs/store-listing.md."""
import re, sys
from pathlib import Path

text = (Path(__file__).resolve().parents[1] / "docs/store-listing.md").read_text()
LIMITS = {"Subtitle": 30, "Promotional text": 170, "Keywords": 100}
bad = False
for block in re.split(r"\n## ", text)[1:]:
    lang = block.splitlines()[0]
    for field, limit in LIMITS.items():
        m = re.search(rf"\*\*{field}:\*\* `?([^`\n]+)`?", block)
        value = m.group(1).strip() if m else ""
        if not value or len(value) > limit or (field == "Keywords" and re.search(r", | ,", value)):
            print(f"{lang}: {field} — {len(value)}/{limit}: {value}"); bad = True
    desc = "\n".join(l[2:] for l in block.splitlines() if l.startswith(">"))
    if not 0 < len(desc) <= 4000:
        print(f"{lang}: Description — {len(desc)}/4000"); bad = True
    subtitle = re.search(r"Subtitle:\*\* (.+)", block).group(1)
    print(f"{lang[:24]:24} subtitle {len(subtitle):3}  description {len(desc)}")
sys.exit(bad)
