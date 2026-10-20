# Continue development through the real Chrome session

This runbook records the working arrangement on this computer as of
**2026-10-20**. Verify live state before changing it. The agent runs commands and
browser tests; the owner supplies initial sign-in and native Chrome consent.
Do not turn routine local work into instructions for the owner to execute.

## Machine and access

| Item | Current value |
| --- | --- |
| Workspace | `/extra/dkropachev/code-factory-plugin` |
| OS/account | Ubuntu 26.04.1 x86-64; `dkropachev` |
| Ethernet | `192.168.0.100` |
| Wi-Fi | `192.168.0.101` |
| Tailscale IPv4 | `100.83.203.0` |
| Tailscale hostname | `dkropachev-ai-rig.tail62c9f9.ts.net` |
| Remote desktop | RDP `192.168.0.100:3389`; system `gnome-remote-desktop.service` |
| Browser | Google Chrome Stable; installed through Google's official Debian package |
| Node at setup | `~/.nvm/versions/node/v24.16.0/bin/node`; use `command -v node` to recheck |

The owner connects from another computer on the LAN using an RDP client
(Windows Remote Desktop Connection, Linux Remmina, or another compatible client).
First authenticate as **`remote`**. At the Ubuntu login screen, sign in as
**`dkropachev`**. The server uses GNOME Remote Login, not xrdp.

The RDP credential record is **`~/.local/state/remote-desktop/credentials.json`**,
mode 0600 in a private directory. It contains `host`, `username`, and `password`.
Retrieve a credential only when needed for authorized authentication; keep it in
memory and use stdin or a protected client mechanism. Never print the file or put
its password in commands, this document, screenshots, chat, or test evidence.
At the owner's last explicit password change, RDP and Ubuntu login passwords were
set equal. This file tracks RDP; do not assume Linux still matches after later
rotation. Do not reset either password without the owner's instruction.

RDP is enabled at boot. Useful read-only checks:

```sh
ip -j -4 address show scope global
tailscale ip -4
systemctl is-active gnome-remote-desktop.service
sudo -n grdctl --system status
ss -lnt '( sport = :3389 or sport = :9222 or sport = :9223 )'
```

Do not use `grdctl status --show-credentials` in output. A private temporary
password page on port 8446 was used during installation and expired; it is not the
normal access mechanism. Remote desktop control does not itself attach an agent
to Chrome. Use the browser controller below.

## Installed application and durable state

| Item | Current value |
| --- | --- |
| Last verified daemon | `0.1.0-850eb63cda16` |
| Complete browser package | `0.2.2` |
| Stable launcher | `~/.local/bin/code-factory` |
| Versioned releases | `~/.local/lib/code-factory` |
| Application data | `$XDG_DATA_HOME/code-factory`, normally `~/.local/share/code-factory` |
| Persistent plugin | `~/.local/lib/code-factory/plugin` |
| Daemon | `code-factory.service`, loopback 8787 |
| OAuth gateway | `code-factory-gateway.service`, public loopback 8788 and private loopback 8789 |
| Public MCP | `https://dkropachev-ai-rig.tail62c9f9.ts.net/code-factory/mcp` |
| Owner approval | `https://dkropachev-ai-rig.tail62c9f9.ts.net:8444/approve` (flow supplies its query) |
| Installed complete plugin | `https://chatgpt.com/plugins/Plugin_34caeb5c66048191a1ac0c8f01c31e73` |
| Registered remote app | `asdk_app_6ad792b279348191b76d3443615b66e0` |
| Dedicated private fixtures | `dkropachev/code-factory-pilot-20261019`; issues 1/3 and PR 2 |

These IDs are observations, not templates for another account. Reuse working
resources. The complete package has **Apps 1 + Skills 2**, a connected account, and
**Try in chat**. The remote app's standalone wrapper is separate from that
package. A `Plugin_...` package ID cannot be passed as the remote app ID.

Private data includes `runtime.json`, `tailscale.json`, `registered-app.json`,
SQLite/WAL, retained worktrees, the vault, owner token, and OAuth authorization
state. Do not dump this directory. Runtime records the resolved provider paths;
the daemon uses authenticated **Codex 0.161.0**, even when global `codex` is newer.
Do not replace GitHub/Codex sign-ins. Do not enable Jira or switch back to the
OpenAI tunnel without a new task requiring it.

```sh
readlink -f ~/.local/bin/code-factory
systemctl --user is-active code-factory.service code-factory-gateway.service
~/.local/bin/code-factory doctor
tailscale serve status --json
```

Preserve unrelated port 443 `/` → loopback 8644 and private 8443 → loopback 8081.
Code Factory uses `/code-factory` plus its scoped RFC metadata paths on public 443.
Private 8444 is owner consent. Legacy 10000 remains for rollback compatibility.
The separate browser lab, when running, uses private 8445. Never use
`tailscale serve reset`, `tailscale funnel reset`, or whole-port 443 `off` to
repair/remove this app. Keep existing login/lingering policy.

## Attach to the owner's signed-in Chrome

Use this flow instead of launching another browser or copying cookies.

1. Reuse the installed Google Chrome on the remote desktop.
2. The owner signs into ChatGPT manually. Do not inspect during sign-in. Wait for
   explicit confirmation that sign-in is finished.
3. The owner opens `chrome://inspect/#remote-debugging` and enables remote
   debugging. The default Chrome listener is loopback 9222.
4. Start the local controller. Request attachment and let the owner accept
   Chrome's native **Allow** prompt when required. Do not bypass native consent.

```sh
# If dependencies are missing, install them before requesting browser access.
cd /extra/dkropachev/code-factory-plugin
# npm ci --prefix web
# npm install --prefix ~/.local/lib/code-factory/browser-tools \
#   --ignore-scripts --no-audit --no-fund chrome-devtools-mcp@1.11.0
node scripts/browser_control.mjs
```

Keep the process in a persistent interactive tool session (`exec_command` with
`tty:true` and a short yield). Send JSON lines through that session's stdin:

```json
{"id":"attach","name":"list_pages","arguments":{}}
```

The controller prints schemas for its permitted tools on startup. `ready:true`
means the MCP client initialized, not that Chrome attached. Attachment requires
a successful operation against the intended signed-in browser. Use returned page
IDs; do not reuse old terminal session IDs or assume page1 is still the test tab.
Chrome's new auto-connect listener may not offer normal `/json/version` HTTP
discovery; use `--autoConnect` through this controller. Missing
`DevToolsActivePort` usually means the native debugging switch is not enabled.

The controller uses local stdio and disables usage statistics and CrUX reporting.
Never expose 9222/9223 through LAN binds or Tailscale. Do not restart Chrome with
another profile, copy its cookies, enable insecure certificate handling in the
real browser, or change unrelated browser settings to make attachment succeed.

Use only the relevant ChatGPT test tabs and private approval page. Preserve the
owner's unrelated tabs and conversations. Do not repeatedly ask for permission
after the user has signed in and Chrome has approved this connection.

## Control protocol and output handling

Examples below use **illustrative** page IDs and element UIDs. Inspect current
schemas and obtain fresh IDs after navigation, a new tool response, or widget
replacement.

```json
{"id":"plugins","name":"navigate_page","arguments":{"pageId":1,"url":"https://chatgpt.com/plugins"}}
{"id":"snapshot","name":"take_snapshot","arguments":{"pageId":1}}
{"id":"click","name":"click","arguments":{"pageId":1,"uid":"CURRENT_UID"}}
{"id":"read","name":"evaluate_script","arguments":{"pageId":1,"function":"() => ({path:location.pathname,title:document.title})","waitForStableDom":false}}
```

Parse controller JSON lines; ignore echoed commands without `result`. Mask URL
queries **and fragments before returning output to the model or evidence**:
OAuth approval and callback URLs carry secret request/state/code values. Do not
print raw tool results. For text blocks, a useful URL sanitizer is:

```js
function safeURLs(text) {
  return text.replace(/https?:\/\/[^\s"'<>)]*/g, raw => {
    try { const u = new URL(raw); return u.origin + u.pathname; }
    catch { return '[URL]'; }
  });
}
```

This only sanitizes URLs; it does not make arbitrary headers, bodies or input
values safe. Never request those during authentication. Avoid full snapshots of
the chat sidebar. Filter snapshots to the current dialog or the subtree beginning
with `Iframe "Code Factory"`; omit unrelated titles and large asset URLs. Prefer
small explicit DOM summaries. Browser automation can return asynchronously:
drain outstanding responses, match their request IDs, and apply the same filter
to delayed snapshot results rather than accidentally printing the entire page.

ChatGPT keeps hidden cached `<main>` elements. To inspect the current screen:

```js
() => [...document.querySelectorAll('main')]
  .filter(el => el.checkVisibility())
  .map(el => el.innerText.slice(-4000))
```

The widget has nested cross-origin frames. Use a fresh widget element UID in
`evaluate_script.args`; the tool selects that element's frame. Inspect its
`ownerDocument`, not the top-level ChatGPT document. For example, with the
**Message agent** textarea UID:

```json
{"id":"agent-state","name":"evaluate_script","arguments":{"pageId":3,"args":["CURRENT_TEXTAREA_UID"],"function":"(input) => ({status:input.ownerDocument.querySelector('.detail .status')?.innerText,transcript:input.ownerDocument.querySelector('.transcript')?.innerText.slice(-2000),alerts:[...input.ownerDocument.querySelectorAll('[role=alert]')].map(e=>e.innerText)})","waitForStableDom":false}}
```

Chrome DevTools MCP `fill` was observed to report success inside the app iframe
while React reset the value and left Send disabled. Do not diagnose that alone
as an application failure. Native editing worked:

```js
async (input) => {
  input.focus();
  input.select();
  const inserted = input.ownerDocument.execCommand('insertText', false,
    'Reply UI_FOLLOWUP_OK. Do not edit files or contact external systems.');
  await new Promise(resolve => setTimeout(resolve, 100));
  return { inserted, length: input.value.length };
}
```

Then click the current **Send message** UID. For ChatGPT's contenteditable
composer, preserve the selected plugin mention: focus it, collapse a DOM Range
at the end, and use native `execCommand('insertText',false,text)` before Enter.
Replacing the whole composer can remove the plugin mention. Wait for actual
React updates and agent state changes rather than treating a click as completion.

## Build and connection checks

```sh
make verify
make connection-test
make connection-sdk-test
# Optional real-model suite; consumes the authenticated Codex account:
CODE_FACTORY_REAL_CODEX=1 make verify
```

`make verify` covers build, vet/types, race/protocol/browser tests, packaging,
installer lifecycle and compiled smoke. The deployed connection commands test
public TLS, OAuth and MCP with ephemeral tokens that they revoke. SDK tests force
fresh connections and verify each actual public peer address. `--tailnet` is
explicitly local-only and cannot prove internet access or ChatGPT acceptance.
Use live `tailscale.json` as the endpoint source; never default new work to 10000.

Build source changes into a versioned release, then install that release before
claiming the real Work app exercised those changes. Follow
[operations.md](operations.md) for checksums and upgrades. Use the **new extracted
binary** for the first upgrade from an old installer lacking gateway-profile
snapshot support. Do not overwrite the stable launcher with an unversioned build.
A rollback archives post-upgrade SQLite state: do not casually roll back a live
installation after the owner has created new work. Use fixtures or a coordinated
test before new work begins, and preserve the pre-rollback archive.

## Real browser E2E procedure

1. **Preflight.** Read acceptance and current service/profile state. Confirm the
   owner-approved Chrome session works. Identify only the private pilot repo and
   explicitly identified test runs. Record installed version and test start time.
2. **Reuse the connected plugin.** Open the installed complete plugin URL above,
   choose **Try in chat**, and select **Work**. In a fresh chat, use its starter or
   ask it to call `open_factory` for GitHub issues/PRs without launching work.
   Verify the actual Code Factory iframe says Connected and shows pilot data.
3. **Observe the UI directly.** Verify sync timestamps and visible polling. The
   conversational model may say it cannot see an embedded dashboard while the
   widget is visibly working. Only browser observation establishes rendering.
   Check actual host CSP settings; do not disable CSP to hide failures.
4. **Inbox semantics.** For full acceptance, use identifiable new pilot fixtures
   to check initial unread baseline, later New items, revision-aware unread,
   independent actions, priority override/label/inference, links and PR detail.
   Listing/opening must not launch an agent. Restore test overrides deliberately.
5. **Explicit launch.** From a pilot issue, edit/review a prepared snapshot. Use
   Plan mode, read-only sandbox and publishing authority `ask`. Confirm the
   isolated worktree and only then Start. The setup skill can also explicitly
   launch its own read-only verification; do not accidentally start a duplicate.
6. **Real question and UI answer.** Request: “Use request_user_input to ask me to
   choose Continue or Stop and wait. After my answer, reply UI_ANSWER_OK and the
   option. Do not edit files or contact external systems.” Confirm native
   `item/tool/requestUserInput` reaches attention. Click the widget's attention
   button, choose Continue, then Answer. Verify the actual agent transcript.
7. **Follow-up.** Send a widget chat message requesting `UI_FOLLOWUP_OK` and the
   selected option. Verify the response and idle status. This must reach real
   Codex 0.161.0, not the fake process or just an initialization handshake.
8. **Recovery.** With a new test question pending, reload/reopen the Work chat and
   verify attention persists. Before a service restart, check there are no
   unrelated live/idle app-server sessions. Restart only the intended service,
   verify old attention becomes stale, and verify a duplicate/stale answer is
   rejected. Explicitly Resume the same run; verify retained worktree/history,
   a fresh question ID, and a successful new UI answer. Restart the gateway
   separately and verify authenticated widget polling recovers without new login.
9. **Finish and record.** Leave test runs idle or explicitly interrupted, retain
   useful history/worktrees, and note every result and blocker. Do not delete
   dirty worktrees, close the owner's Chrome, or log the owner out as cleanup.

The successful historical test run is
`09f11628428c72dea969f0f860b6334d`, in
`~/.local/share/code-factory/worktrees/09f11628428c72dea969f0f860b6334d`.
Its question, UI answer, follow-up and restart recovery are recorded in
[the dated browser evidence](evidence/2026-10-20-work-browser-acceptance.json).
Do not assume that run is still a disposable test or blindly reuse its IDs.

## Install or update the complete browser package

Only repeat registration when the existing connection is absent or broken.
The supported flow is **Plugins → Add → Add custom MCP server → Server URL →
OAuth → Create as a plugin**. Use the current 443 endpoint and approve on private
8444 as the Tailscale owner. Never enter keys in chat or disable authentication.
Capture the observed remote app ID, then:

```sh
code-factory link-plugin --app-id OBSERVED_ASDK_APP_ID
python3 scripts/package_plugin.py --registered-app-id OBSERVED_ASDK_APP_ID
```

`/code-factory/plugin-browser.zip` serves the linked browser package. It contains
`.app.json`, both skills and branding; it omits the desktop MCP launch files.
`/code-factory/plugin.zip` is the separate portable/local-host package. Do not
upload the latter expecting it to create a browser OAuth connection.

In ChatGPT choose **Add → Upload plugin archive**. The observed dialog is
**New Plugin**, with `input[type=file][name=plugin-archive]`, accepting ZIP.
The controller does not expose `upload_file`. A proven UI automation alternative
is to load **only the generated intended ZIP** in a temporary 0600 function source
file, construct a `File` from its base64 bytes, place it in `new DataTransfer()`,
assign `input.files`, and dispatch a bubbling `change` event. Invoke this through
`evaluate_script.sourcePath` pointing under `/tmp`. Return only filename and size;
never expose encoded contents or upload private state/profile files. Then click
**Add plugin**, inspect the imported Apps/Skills/version, and **Install plugin**.

Test the actual installed package in a fresh Work conversation, including its
setup skill and starter prompt. A working standalone connector or ZIP validation
alone does not prove the complete package works. Keep creation/installation
private; do not publish to a public directory as part of testing.

## Events, blockers and truthful acceptance

Current observation: after Refresh tools and in fresh Work chats, the host said
MCP Events subscriptions were unavailable; zero subscriptions were stored.
This is not proof of a permanent account restriction or server defect. Recheck
when host capabilities change. Do not replace host callbacks with scheduled
polling and call that successful Events acceptance.

When available, confirm discovery of `inbox.changed`, `attention.required` and
`run.state`. Ask a Work chat to subscribe to `attention.required` with an
`entity` filter for the **current test run ID**, read that run on arrival, and
show its pending question without answering it. The host must create the actual
callback and signing secret. Verify a stored subscription and verification
timestamp before triggering attention. Verify signed delivery, the actual Work
response, other-entity filtering, renewal/restart, and host unsubscribe. Never
manufacture a subscription through a local fixture and present it as Work proof.

Read SQLite only with `mode=ro` when needed. Inspect explicit test run IDs and
only nonsecret subscription fields such as event name, filters, timestamps,
cursor and status. Do not print whole rows: subscription data can contain callback
URLs and secrets. `verifiedAt`/`deliveredAt` alone do not prove a Work response.

The remaining readiness checks are authoritative in [acceptance.md](acceptance.md):
real Events lifecycle, one-hour connected soak, coordinated logout/login,
remaining current-version inbox/priority/cleanup matrix and host CSP enforcement.
Use verified/failed/unverified separately. A short session with deliberate
restarts is not a one-hour soak.

Record nonsecret evidence tied to the installed version:

```sh
code-factory record-evidence --checkpoint question.answer --state verified \
  --evidence 'DATE: Work conversation reference, observed UI result and test run ID'
```

Use only supported checkpoint IDs from `internal/setup/evidence.go`. Update
`docs/evidence/` and `docs/acceptance.md`; never mark the pilot ready because local
tests or the core workflow passed while required host checks remain missing.

## Known failures and recovery rules

- **App creation 500 after 30s/120s on 10000:** the 443 scoped-path A/B succeeded.
  Preserve the current standard 443 arrangement. This does not establish a general
  ChatGPT port ban; do not blame an OpenAI outage without independent evidence.
- **Private form 403, Invalid approval origin:** native forms under `no-referrer`
  can send `Origin:null`; ordinary fetch-based SDK tests can miss this. Keep the
  tested private GET `strict-origin` policy and strict Origin/nonce checks.
- **OAuth 303 blocked in Chrome:** form-action must permit the exact validated
  ChatGPT callback as well as self. Do not allow arbitrary redirect origins.
- **Public TLS EOF/reset:** test every public DNS address with fresh verified TLS
  and SNI. Tailnet DNS can bypass ingress; HTTPS socket pooling can test the same
  IP twice. The independent scripts account for both. A Tailscale restart affects
  all node services; do not loop restarts or change ACLs speculatively.
- **Old answer after restart:** rejection is expected. Resume explicitly and
  answer the newly created request; never replay the expired one automatically.
- **Missing browser access:** require only the missing native enable/Allow or
  owner sign-in. Do not ask the owner to reproduce the whole failing workflow.

The optional isolated browser lab remains documented in
[browser-lab.md](browser-lab.md). It uses a different profile, loopback 9223,
private 8445 UI and its own Xvfb. Prefer the already signed-in real Chrome; never
mistake the lab's Chromium for the owner's authenticated Chrome session.

References: [official OpenAI testing flow](https://developers.openai.com/plugins/deploy/connect-chatgpt),
[official MCP Events tests](https://developers.openai.com/plugins/build/mcp-events#test-in-chatgpt),
[Chrome owner-approved auto-connect](https://developer.chrome.com/docs/devtools/agents/use-cases/auto-connect),
[GNOME remote login](https://help.gnome.org/gnome-help/remote-login.html).
