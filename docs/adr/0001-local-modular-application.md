# ADR 0001: local modular application

Status: accepted for the foundation.

Use one Go module and one executable with internal feature packages. Compose
storage, sources, and transports at the CLI boundary. This reduces installation
cost while leaving room for multiple clients and adapters.

Separate services are justified only by measured deployment or scaling needs.
The connector contract is the first experimental public package; other internals
remain free to evolve.
