// Paint an actionable shell before parsing the SDK, React or secondary panels.
// The complete resource remains inline and content-addressed.
import { readFile, writeFile } from "node:fs/promises";
import { gzipSync } from "node:zlib";
const path = new URL("../internal/web/dist/index.html", import.meta.url);
let html = await readFile(path, "utf8");
const scripts = [
  ...html.matchAll(/<script\b[^>]*type="module"[^>]*>([\s\S]*?)<\/script>/g),
];
if (scripts.length !== 1)
  throw new Error("Expected exactly one bundled UI module");
const runtime =
    '<script type="application/x-code-factory" id="factory-runtime" data-encoding="gzip-base64">' +
    gzipSync(Buffer.from(scripts[0][1]), { level: 9 }).toString("base64") +
    "</script>";
// Keep the large inert payload after the actionable shell as well: parsing its
// text in the head delays navigation even though no JavaScript executes there.
html = html.replace(scripts[0][0], "").replace(/[ \t]+$/gm, "");
const bodyEnd = html.lastIndexOf("</body>");
if (bodyEnd < 0) throw new Error("Missing UI document body");
const loader = `<script>
requestAnimationFrame(function(){requestAnimationFrame(function(){requestAnimationFrame(async function(){
var runtime=document.getElementById("factory-runtime"),script=document.createElement("script");
try{
var bytes=Uint8Array.from(atob(runtime.textContent),function(c){return c.charCodeAt(0)});
script.type="module";script.textContent=await new Response(new Blob([bytes]).stream().pipeThrough(new DecompressionStream("gzip"))).text();
runtime.remove();document.body.appendChild(script);
}catch(error){var status=document.querySelector('[aria-label="Connection status"]')||document.getElementById("boot-settings");status.textContent="Could not load Code Factory. Reopen to retry.";status.setAttribute("role","alert");console.error(error)}
})})});
</script>`;
// Leave another frame between shell paint and runtime compilation so expensive
// SDK startup cannot occupy the shell's paint/completion task on slower hosts.
html = html.slice(0, bodyEnd) + runtime + loader + html.slice(bodyEnd);
await writeFile(path, html);
