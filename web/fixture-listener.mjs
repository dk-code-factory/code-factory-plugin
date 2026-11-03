import { once } from "node:events";

export async function listenFixture(server, port = 0) {
  // Vite's listen() substitutes its default for port zero. Bind its public HTTP
  // server directly; the actual listener owns the kernel allocation throughout.
  const listening = once(server.httpServer, "listening");
  server.httpServer.listen({ host: "127.0.0.1", port });
  await listening;
  return `http://127.0.0.1:${server.httpServer.address().port}`;
}
