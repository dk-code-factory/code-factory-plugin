# Native Codex dependencies

The built-in dependency and plugin administration APIs have been retired. Code
Factory uses installed native Codex profiles, agent definitions and skills. Manage
those installations with Codex tooling; no application update loop replaces the
owner's executable, authentication or enabled plugins.

Configuration chat can inspect native settings and propose concrete edits. Every
edit requires explicit owner confirmation and applies at a safe turn boundary.
See [plugin configuration](plugin.md#conversations-and-configuration).

Compatibility checks validate the app-server and experimental client-tool schema
before starting a model turn. A missing profile, agent definition, skill or
required schema is a visible error. Fixture compatibility checks do not establish
installed-host or real-provider acceptance.
