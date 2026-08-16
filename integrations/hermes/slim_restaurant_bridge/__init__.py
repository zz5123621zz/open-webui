"""Hermes 0.19.x entry point for the slim restaurant bridge."""

from .bridge import pre_gateway_dispatch


def register(ctx) -> None:
    ctx.register_hook("pre_gateway_dispatch", pre_gateway_dispatch)


__all__ = ["pre_gateway_dispatch", "register"]
