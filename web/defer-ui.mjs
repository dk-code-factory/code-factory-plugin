// Paint an actionable shell before parsing the SDK, React or secondary panels.
// The complete resource remains inline and content-addressed.
import { readFile, writeFile } from "node:fs/promises";
const path = new URL("../internal/web/dist/index.html", import.meta.url);
let html = await readFile(path, "utf8");
const scripts = [
  ...html.matchAll(/<script\b[^>]*type="module"[^>]*>([\s\S]*?)<\/script>/g),
];
if (scripts.length !== 1)
  throw new Error("Expected exactly one bundled UI module");
html = html.replace(
  scripts[0][0],
  () =>
    '<script type="application/x-code-factory" id="factory-runtime">' +
    scripts[0][1] +
    "</script>",
);
html = html.replace(
  "</body>",
  `<script>
requestAnimationFrame(function(){requestAnimationFrame(function(){setTimeout(function(){
var runtime=document.getElementById("factory-runtime"),script=document.createElement("script");
script.type="module";script.textContent=runtime.textContent;runtime.remove();document.body.appendChild(script);
},0)})});
</script></body>`,
);
await writeFile(path, html);
