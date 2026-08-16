#!/usr/bin/env python3

from pathlib import Path


CONFIG = Path(__file__).parent / "nginx" / "chat.la4rain.com.conf"
WEBSOCKET_ROUTES = (
    "/api/v1/speech/sessions",
    "/api/v1/dictation/sessions",
)
REQUIRED_DIRECTIVES = (
    "proxy_http_version 1.1;",
    "proxy_set_header Upgrade $http_upgrade;",
    'proxy_set_header Connection "upgrade";',
    "proxy_buffering off;",
)


def location_block(config: str, declaration: str, *, last: bool = False) -> str:
    marker = f"{declaration} {{"
    start = config.rfind(marker) if last else config.find(marker)
    if start < 0:
        raise AssertionError(f"missing Nginx block: {declaration}")

    depth = 0
    opened = False
    for index in range(start, len(config)):
        character = config[index]
        if character == "{":
            depth += 1
            opened = True
        elif character == "}":
            depth -= 1
            if opened and depth == 0:
                return config[start : index + 1]
    raise AssertionError(f"unterminated Nginx block: {declaration}")


def main() -> None:
    config = CONFIG.read_text(encoding="utf-8")
    for route in WEBSOCKET_ROUTES:
        block = location_block(config, f"location = {route}")
        for directive in REQUIRED_DIRECTIVES:
            if directive not in block:
                raise AssertionError(f"{route} is missing: {directive}")

    ordinary = location_block(config, "location /", last=True)
    if 'proxy_set_header Connection "";' not in ordinary:
        raise AssertionError("ordinary HTTP proxy must clear Connection")


if __name__ == "__main__":
    main()
