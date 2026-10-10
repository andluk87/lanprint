#!/usr/bin/env python3
"""Send one supported document using IPP Print-Job, no third-party packages."""
import argparse
import http.client
import pathlib
import struct
import sys


def attribute(tag, name, value):
    name, value = name.encode("utf-8"), value.encode("utf-8")
    return bytes([tag]) + struct.pack(">H", len(name)) + name + struct.pack(">H", len(value)) + value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", required=True)
    parser.add_argument("--port", type=int, default=8631)
    parser.add_argument("--file", required=True, type=pathlib.Path)
    parser.add_argument("--format", choices=["text/plain", "image/png", "image/jpeg", "application/octet-stream"], required=True)
    args = parser.parse_args()
    data = args.file.read_bytes()
    if not data or len(data) > 32 * 1024 * 1024:
        parser.error("document must be nonempty and at most 32 MiB")
    body = struct.pack(">BBHI", 1, 1, 2, 1) + b"\x01"
    body += attribute(0x47, "attributes-charset", "utf-8")
    body += attribute(0x48, "attributes-natural-language", "en")
    body += attribute(0x45, "printer-uri", f"ipp://{args.host}:{args.port}/ipp/print")
    body += attribute(0x42, "job-name", args.file.name)
    body += attribute(0x49, "document-format", args.format)
    body += b"\x03" + data
    connection = http.client.HTTPConnection(args.host, args.port, timeout=35)
    try:
        connection.request("POST", "/ipp/print", body, {"Content-Type": "application/ipp"})
        response = connection.getresponse()
        result = response.read()
        if response.status != 200 or len(result) < 9:
            raise RuntimeError(f"HTTP {response.status}: {result!r}")
        status = struct.unpack_from(">H", result, 2)[0]
        if status >= 0x0100:
            raise RuntimeError(f"IPP error 0x{status:04x}: {result!r}")
        print("Job accepted by LANPrint. Check Windows queue and physical output.")
    finally:
        connection.close()


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
