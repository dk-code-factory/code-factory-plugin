# Portable package schemas

Canonical Agent Plugins 1.0.0 JSON schemas, fetched 2026-10-19 for offline build
validation. Sources:

- https://agent-plugins.org/schemas/1.0.0/plugin.schema.json
- https://agent-plugins.org/schemas/1.0.0/mcp.schema.json

OpenAI-specific presentation uses the plugin extension namespace. The portable
MCP connection uses standard transport fields; OAuth is discovered from the
running endpoint rather than embedding credentials or nonportable auth headers.
