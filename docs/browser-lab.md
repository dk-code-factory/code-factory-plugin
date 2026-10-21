# Private browser test lab

For the complete real-Chrome workflow, current RDP access, installed plugin IDs,
recovery tests and evidence requirements, read [the agent E2E runbook](agent-e2e.md).
Use the signed-in Chrome flow below before considering the separate lab profile.

## Attach to Chrome already signed in on the remote desktop

When the owner has signed into ChatGPT in the installed Google Chrome, use that
session instead of asking them to sign into the separate lab again. Chrome 144+
supports [owner-approved auto-connect](https://developer.chrome.com/docs/devtools/agents/use-cases/auto-connect).
The owner opens `chrome://inspect/#remote-debugging`, enables remote debugging,
and accepts Chrome's native **Allow** prompt when the local agent connects.
The agent must not bypass either native control or export the browser profile.

The local Chrome DevTools MCP package is pinned to 1.11.0 under
`~/.local/lib/code-factory/browser-tools`. It can be installed independently of
the product build:

```sh
npm install --prefix ~/.local/lib/code-factory/browser-tools \
  --ignore-scripts --no-audit --no-fund chrome-devtools-mcp@1.11.0
node scripts/browser_control.mjs
```

The stdio controller initializes the MCP client without attaching. Send a JSON
line to request attachment (Chrome may show its native permission prompt):

```json
{"id":"attach","name":"list_pages","arguments":{}}
```

Further JSON lines select the returned page ID and call the advertised UI tools.
Use only the ChatGPT setup/test tab and the configured Code Factory approval
page. Do not inspect unrelated tabs, cookie stores, authorization headers, or
sign-in inputs. No trace, snapshot file, HAR, or profile export is needed. This
controller uses only local stdio, with usage statistics and CrUX reporting
disabled. It does not expose another network control endpoint. Send
`{"name":"exit"}` to disconnect; the owner can disable remote debugging after
testing. A successful MCP initialization alone does not prove browser attachment.

## Separate test profile

This opt-in test browser lets the owner reproduce ChatGPT's custom MCP app
registration on this Linux machine, then let an agent investigate the same test
session. It uses Playwright's installed Chromium and a separate profile; it never
reads or copies your usual browser cookies. Running it does not establish that
ChatGPT sign-in, Work access, registration, or Code Factory tools work.

Prerequisites: Node, `web/node_modules/playwright`, installed Playwright Chromium,
`Xvfb`, `xauth`, and an existing owner-verified
`~/.local/share/code-factory/tailscale.json`. No credentials belong in chat.

```sh
node --test scripts/test_browser_lab.mjs
node scripts/browser_lab.mjs --check
node scripts/browser_lab.mjs
```

The browser normally runs on its own Xvfb display, without touching your desktop.
An explicit `--headless` option skips Xvfb. Keep the process running, or have the
agent install an opt-in user service with `UMask=0077`. It must not enable user
lingering. SIGTERM/SIGINT close the browser and Xvfb and remove the runtime lock.
The private profile persists under
`~/.local/state/code-factory/browser-lab/profile` (parent mode 0700). Browser
storage contains the test account's login state: retain it only as long as useful.

The UI binds only `127.0.0.1:8790`. The Chromium debugger binds only
`127.0.0.1:9223`. Never publish either listener through Funnel, open network
binds, or forward the debugger. A same-user local process can use the debugger:
the local OS account is a trust boundary.

Before exposing the UI, inspect `tailscale serve status --json`. Refuse if port
8445 is occupied by another route or has Funnel enabled. Preserve every existing
route. Add only the dedicated private Serve route:

```sh
tailscale serve --bg --yes --https=8445 http://127.0.0.1:8790
```

Open `https://YOUR-TAILSCALE-HOST:8445` from your owner-connected browser. The
expected host is derived from the configured approval URL. Every UI request
requires Tailscale's exact `Tailscale-User-Login` owner identity and expected Host.
POST requests also require the exact HTTPS Origin, an HttpOnly Secure SameSite
session cookie and a CSRF token. Forwarding through a public proxy is unsupported.

Sign in manually through the screenshot viewport. Click fields on the image,
enter text in the password-style input and click **Send text**. The input clears
immediately; use **Tab**, **Enter**, and **Scroll** controls as needed. Screenshots
refresh after actions or through **Refresh**, and are never written to disk.
Input values, browser errors, request headers/bodies, cookies, tokens, URL queries
and fragments are not emitted in diagnostics. Auth may navigate to other origins
through redirects or links; the navigation box itself permits only ChatGPT,
OpenAI auth and the configured gateway/approval origins. Browser downloads are
disabled. Some authentication flows, passkeys or anti-bot challenges may not work
through this limited viewport UI; record that blocker without weakening controls.

The agent must pause browser inspection and automation during sign-in. When
finished, explicitly tell it **signed in**. Only then may it connect Playwright:

```js
import { chromium } from './web/node_modules/playwright/index.mjs';
const browser = await chromium.connectOverCDP('http://127.0.0.1:9223');
const context = browser.contexts()[0];
const page = context.pages().at(-1);
// Reproduce the custom MCP registration after the owner confirms sign-in.
// Do not export the profile, screenshots, traces, HAR, headers or page bodies.
```

The UI's status area keeps at most 100 failing ChatGPT response records in memory:
sanitized origin/path, HTTP status, timestamp and an allowlisted server request
ID if present. It intentionally omits request and response bodies. The agent can
use those records to distinguish app-creation backend failures from gateway
OAuth problems. No request evidence is proof of a successful live connection.

To stop, send SIGTERM to the lab process, then remove only its private route:

```sh
tailscale serve --https=8445 off
```

Verify that route still points to this lab before removing it. Preserve other
Serve/Funnel routes. To remove stored test login state, stop the process and
delete only the private browser-lab directory. If the lab reports an occupied
port or stale lock, inspect the PID in `lab.lock` and Chromium's profile locks;
the launcher never kills unrelated processes or automatically removes stale
locks. Do not remove a live browser's locks. Reusing another browser profile is
unsupported.
