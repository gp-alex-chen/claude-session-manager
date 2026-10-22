"""Forward TheOne events to your HTTP server; uses only Python's standard library."""

import json
import sys
import urllib.request


def main():
    url = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8787/events"
    payload = sys.stdin.buffer.read()
    event = json.loads(payload.decode("utf-8"))
    if event.get("schema_version") != 1:
        raise ValueError("Unsupported TheOne event schema")

    request = urllib.request.Request(
        url,
        data=payload,
        headers={"Content-Type": "application/json; charset=utf-8"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=5):
        pass


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
