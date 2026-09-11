import { createServer } from "node:http";

const port = Number(process.env.PORT || 3000);
const greeting = process.env.GREETING || "hello";
const started = Date.now();

createServer((req, res) => {
  if (req.url === "/health") { res.writeHead(200, { "content-type": "text/plain" }); return res.end("ok"); }
  res.writeHead(200, { "content-type": "application/json" });
  res.end(JSON.stringify({ greeting, uptimeSeconds: Math.round((Date.now() - started) / 1000), node: process.version }));
}).listen(port, "0.0.0.0", () => console.log(`listening on ${port}`));
