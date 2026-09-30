const http = require("http");
const https = require("https");
const crypto = require("crypto");
const fs = require("fs");
const path = require("path");
const { URL } = require("url");

// 统一工程控制台前端服务：静态资源 + 凭据加密端点 + 老接口 / 模块命名空间反代。
//
// 与 /switchweb/server.cjs 的差异（仅部署参数与代理范围）：
//   1. 默认端口 18094（老壳 8091 / 老后端 8092 一律不动）；
//   2. 默认真实后端 http://127.0.0.1:18092（统一工程骨架 txapi），也可用 TXAPI_TARGET 指定；
//   3. 除 /itms/*（老接口）外，追加反代 /api/*（capabilities 与 /api/v2/* 模块命名空间）、
//      /client/*（TR069 Python 提供方）与 /healthz（存活探针）。
const PORT = Number(process.env.PORT || 18094);
const BACKEND_TARGET =
  process.env.BACKEND_TARGET || process.env.TXAPI_TARGET || "http://127.0.0.1:18092";
const TLS_CERT_PATH = String(process.env.TLS_CERT || "").trim();
const TLS_KEY_PATH = String(process.env.TLS_KEY || "").trim();
const ROOT = __dirname;
const SWITCH_LOGIN_KEY_ENV = "SWITCH_LOGIN_AES_KEY";
// 设备凭据加密密钥文件：默认与老壳一致（/home/switchfile/aeskey.key），
// 本地自检 / 演示用 SWITCH_LOGIN_KEY_FILE 指向骨架的隔离注册目录（$DEV_DIR/reg/aeskey.key），
// 从而「前端加密 → 后端解密」用的是同一把密钥，且不读写设备真实密钥文件。
const SWITCH_LOGIN_KEY_PATH =
  String(process.env.SWITCH_LOGIN_KEY_FILE || "").trim() || "/home/switchfile/aeskey.key";
// 需要转发到统一工程骨架（txapi）的请求前缀。
const PROXY_PREFIXES = ["/itms/", "/api/", "/client/"];
const PROXY_PATHS = ["/healthz"];

const MIME_TYPES = {
  ".css": "text/css; charset=utf-8",
  ".html": "text/html; charset=utf-8",
  ".ico": "image/x-icon",
  ".js": "application/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".png": "image/png",
  ".svg": "image/svg+xml; charset=utf-8",
  ".txt": "text/plain; charset=utf-8",
  ".webmanifest": "application/manifest+json; charset=utf-8"
};

function send(res, status, body, headers = {}) {
  res.writeHead(status, headers);
  res.end(body);
}

function sendJSON(res, status, payload) {
  send(res, status, JSON.stringify(payload), {
    "Content-Type": "application/json; charset=utf-8"
  });
}

// 返回给前端的消息中不得包含服务器目录、内部地址等信息。
function sanitizeClientMessage(message) {
  return String(message || "")
    .replace(
      /\/(?:dev|opt|home|etc|var|tmp|run|root|usr|srv|mnt|media|proc|sys|switchfile|txapikeygen|switchapi)(?:\/[A-Za-z0-9._-]+)*/g,
      "服务端文件"
    )
    .replace(/https?:\/\/[^\s"']+/g, "后端服务");
}

function readRequestBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on("data", (chunk) => {
      chunks.push(chunk);
    });
    req.on("end", () => {
      resolve(Buffer.concat(chunks).toString("utf8"));
    });
    req.on("error", reject);
  });
}

async function parseRequestFields(req) {
  const body = await readRequestBody(req);
  if (!body) {
    return {};
  }
  const contentType = String(req.headers["content-type"] || "").toLowerCase();
  if (contentType.includes("application/json")) {
    return JSON.parse(body);
  }
  const params = new URLSearchParams(body);
  return Object.fromEntries(params.entries());
}

function normalizeSwitchLoginKey(raw) {
  const value = String(raw || "").trim();
  if (value.length === 32) {
    return Buffer.from(value, "utf8");
  }
  if (value.length === 64 && /^[0-9a-f]+$/i.test(value)) {
    return Buffer.from(value, "hex");
  }

  const base64Decoded = Buffer.from(value, "base64");
  if (base64Decoded.length === 32 && base64Decoded.toString("base64") === value) {
    return base64Decoded;
  }
  throw new Error("invalid AES-256 key, expected 32-byte raw/base64 or 64-char hex");
}

function loadSwitchLoginKey() {
  const envValue = String(process.env[SWITCH_LOGIN_KEY_ENV] || "").trim();
  if (envValue) {
    return normalizeSwitchLoginKey(envValue);
  }

  fs.mkdirSync(path.dirname(SWITCH_LOGIN_KEY_PATH), { recursive: true, mode: 0o700 });
  try {
    const content = fs.readFileSync(SWITCH_LOGIN_KEY_PATH, "utf8");
    return normalizeSwitchLoginKey(content);
  } catch (error) {
    if (error.code !== "ENOENT") {
      throw error;
    }
  }

  const key = crypto.randomBytes(32);
  fs.writeFileSync(SWITCH_LOGIN_KEY_PATH, key.toString("hex"), { mode: 0o600 });
  return key;
}

function encryptSwitchSecret(plain) {
  const text = String(plain || "").trim();
  if (!text) {
    return "";
  }
  const key = loadSwitchLoginKey();
  const nonce = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv("aes-256-gcm", key, nonce);
  const encrypted = Buffer.concat([cipher.update(text, "utf8"), cipher.final()]);
  const authTag = cipher.getAuthTag();
  return Buffer.concat([nonce, encrypted, authTag]).toString("base64");
}

async function handleEncryptSwitchCredentials(req, res) {
  if (req.method !== "POST") {
    sendJSON(res, 405, { code: -1, message: "method not allowed" });
    return;
  }

  try {
    const fields = await parseRequestFields(req);
    sendJSON(res, 200, {
      code: 0,
      message: "success",
      data: {
        szUsername: encryptSwitchSecret(fields.szUsername),
        szPassword: encryptSwitchSecret(fields.szPassword)
      }
    });
  } catch (error) {
    sendJSON(res, 500, { code: -1, message: sanitizeClientMessage(error.message) || "encrypt failed" });
  }
}

function resolveFilePath(urlPathname) {
  const safePath = path.normalize(decodeURIComponent(urlPathname)).replace(/^(\.\.[/\\])+/, "");
  let filePath = path.join(ROOT, safePath);
  if (safePath === "/" || safePath === ".") {
    filePath = path.join(ROOT, "index.html");
  }
  return filePath;
}

function serveStatic(req, res) {
  const url = new URL(req.url, `http://${req.headers.host || "127.0.0.1"}`);
  let filePath = resolveFilePath(url.pathname);

  fs.stat(filePath, (statErr, stats) => {
    if (!statErr && stats.isDirectory()) {
      filePath = path.join(filePath, "index.html");
    }

    fs.readFile(filePath, (readErr, content) => {
      if (readErr) {
        fs.readFile(path.join(ROOT, "index.html"), (fallbackErr, fallback) => {
          if (fallbackErr) {
            send(res, 404, "Not Found", { "Content-Type": "text/plain; charset=utf-8" });
            return;
          }
          send(res, 200, fallback, { "Content-Type": MIME_TYPES[".html"], "Cache-Control": "no-store" });
        });
        return;
      }
      const ext = path.extname(filePath).toLowerCase();
      // 静态资源一律不缓存：改完 styles.css / app.js 刷新即生效，
      // 避免浏览器拿旧文件后误判“改动没生效”（文件本就是每次请求实时读盘）。
      send(res, 200, content, {
        "Content-Type": MIME_TYPES[ext] || "application/octet-stream",
        "Cache-Control": "no-store"
      });
    });
  });
}

function proxyRequest(req, res) {
  const target = new URL(req.url, BACKEND_TARGET);
  const transport = target.protocol === "https:" ? https : http;

  const proxy = transport.request(
    {
      protocol: target.protocol,
      hostname: target.hostname,
      port: target.port,
      path: `${target.pathname}${target.search}`,
      method: req.method,
      headers: {
        ...req.headers,
        host: target.host
      }
    },
    (proxyRes) => {
      res.writeHead(proxyRes.statusCode || 502, proxyRes.headers);
      proxyRes.pipe(res);
    }
  );

  proxy.on("error", (error) => {
    send(
      res,
      502,
      JSON.stringify({ code: -1, message: sanitizeClientMessage(`proxy error: ${error.message}`) }),
      { "Content-Type": "application/json; charset=utf-8" }
    );
  });

  req.pipe(proxy);
}

function handleRequest(req, res) {
  if (!req.url) {
    send(res, 400, "Bad Request", { "Content-Type": "text/plain; charset=utf-8" });
    return;
  }

  if (req.url === "/switchweb/api/encrypt-switch-credentials") {
    handleEncryptSwitchCredentials(req, res);
    return;
  }

  // 老接口 + 模块命名空间 + TR069 Python 提供方统一反代到 txapi。
  const pathname = req.url.split("?")[0];
  if (PROXY_PREFIXES.some((prefix) => pathname.startsWith(prefix)) || PROXY_PATHS.includes(pathname)) {
    proxyRequest(req, res);
    return;
  }

  serveStatic(req, res);
}

// createServer 根据是否配置 TLS 证书决定监听 HTTPS 还是 HTTP：
//   - 同时配置 TLS_CERT 与 TLS_KEY：使用 HTTPS（部署到新设备时要求通过 https://ip:54433 访问）；
//   - 都未配置：使用 HTTP（开发环境默认）；
//   - 只配置其中一个：直接报错退出，避免误以为已启用 HTTPS。
function createServer() {
  if (!TLS_CERT_PATH && !TLS_KEY_PATH) {
    return { server: http.createServer(handleRequest), scheme: "http" };
  }
  if (!TLS_CERT_PATH || !TLS_KEY_PATH) {
    console.error("TLS_CERT 与 TLS_KEY 必须同时配置（HTTPS 需要证书与私钥）");
    process.exit(1);
  }
  let cert;
  let key;
  try {
    cert = fs.readFileSync(TLS_CERT_PATH);
    key = fs.readFileSync(TLS_KEY_PATH);
  } catch (error) {
    console.error(`读取 TLS 证书或私钥失败: ${sanitizeClientMessage(error.message)}`);
    process.exit(1);
  }
  return { server: https.createServer({ cert, key }, handleRequest), scheme: "https" };
}

const { server, scheme } = createServer();

server.listen(PORT, "0.0.0.0", () => {
  console.log(`工程控制台 listening on ${scheme}://0.0.0.0:${PORT}`);
  console.log(`proxying ${PROXY_PREFIXES.join(" ")} to ${BACKEND_TARGET}`);
});
