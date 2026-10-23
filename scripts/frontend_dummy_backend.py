#!/usr/bin/env python3
"""Local-only account controls preview with synthetic credentials and no upstream calls."""
import argparse
import copy
import json
import re
from datetime import datetime, timezone, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse, parse_qs
from zoneinfo import ZoneInfo
UI_DIR=Path(__file__).resolve().parents[1]/"internal/plugin/web"
API_BASE="/v0/management/plugins/cpa-codex-controls"
RESOURCE_BASE="/v0/resource/plugins/cpa-codex-controls"
UI_INCLUDE=re.compile(r"/\*([a-z0-9-]+\.(?:css|js))\*/")
LOCATION_SETTINGS = {"enabled": False, "country": "", "region": "", "city": "", "timezone": ""}

CREDENTIAL_POLICIES = {}
CONCURRENCY_SETTINGS = {"account_queue_capacity":0,"queue_timeout_seconds":30}

HOST_SHELL = r"""<!doctype html>
<html lang="en" data-host="__HOST_MODE__">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__HOST_LABEL__ · 账号设置样式预览</title>
<style>
:root{
  --bg-secondary:#faf9f5;--bg-primary:#f0eee8;--bg-tertiary:#e9e6df;--bg-hover:var(--bg-tertiary);
  --text-primary:#2d2a26;--text-secondary:#6d6760;--text-tertiary:#a29c95;
  --border-color:#e3e1db;--border-primary:#d5d2cb;--border-hover:#cecac4;
  --primary-color:#8b8680;--primary-hover:#7f7a74;--primary-active:#726d67;--primary-contrast:#fff;
  --success-badge-bg:#d1fae5;--success-badge-text:#065f46;--success-badge-border:#6ee7b7;
  --failure-badge-bg:#c6574624;--failure-badge-text:#8a3a30;--failure-badge-border:#c6574659;
  color-scheme:light;
}
:root[data-theme=white]{
  --bg-secondary:#fff;--bg-primary:#fff;--bg-tertiary:#f6f6f6;
  --border-color:#e5e5e5;--border-primary:#d9d9d9;--border-hover:#ccc;
}
:root[data-theme=dark]{
  --bg-secondary:#151412;--bg-primary:#1d1b18;--bg-tertiary:#262320;--bg-hover:#2e2a26;
  --text-primary:#f6f4f1;--text-secondary:#c9c3bb;--text-tertiary:#9c958d;
  --border-color:#3a3530;--border-primary:#4a453f;--border-hover:#5a544d;
  --primary-hover:#9a948e;--primary-active:#a6a099;
  --success-badge-bg:#064e3b4d;--success-badge-text:#6ee7b7;--success-badge-border:#059669;
  --failure-badge-bg:#c657463d;--failure-badge-text:#f1b0a6;--failure-badge-border:#c6574680;
  color-scheme:dark;
}
html[data-host=cpamp]{
  --app-bg:#eff2f7;--app-bg-gradient:linear-gradient(120deg,#f0f7ff 0%,#e7f2ff 50%,#edf7ff 100%);
  --app-bg-blob-1-start:#7aa2ff;--app-bg-blob-1-end:#b6d4ff;--app-bg-blob-2-start:#6bc5ff;--app-bg-blob-2-end:#7debdc;
  --app-bg-blob-1-opacity:.5;--app-bg-blob-2-opacity:.5;--app-bg-blob-blur:60px;--glass-backdrop-filter:blur(20px);
  --app-surface:rgba(255,255,255,.94);--app-surface-strong:#fff;--app-surface-muted:rgba(255,255,255,.68);
  --app-border:rgba(15,23,42,.08);--app-border-strong:rgba(15,23,42,.12);
  --app-text-primary:#2c3e50;--app-text-regular:#5f6c7b;--app-text-muted:#8b95a6;
  --app-accent-soft:rgba(59,130,246,.12);--surface-subtle:#f6faff;
  --app-radius-lg:20px;--app-radius-md:12px;--app-radius-sm:8px;
  --glass-bg:#fff;--glass-border:rgba(255,255,255,.6);--glass-shadow:none;
  --app-input-bg:rgba(255,255,255,.62);--app-input-bg-focus:#fff;
  --app-input-border:var(--app-border-strong);--app-input-border-focus:#3b82f6;
  --color-primary:#3b82f6;--color-primary-light-3:#60a5fa;--color-primary-dark-2:#2563eb;
  --color-success:#22c55e;--success-color:var(--color-success);
  --primary-color:var(--color-primary);--primary-hover:var(--color-primary-light-3);
  --primary-active:var(--color-primary-dark-2);--primary-solid:#2563eb;--primary-solid-hover:#3b82f6;
  --primary-ring:rgba(59,130,246,.22);--primary-contrast:#fff;
  --color-warning:#f59e0b;--color-danger:#ef4444;
  --data-blue-base:#3b82f6;--data-green-base:#22c55e;--data-amber-base:#f59e0b;
  --data-red-base:#ef4444;--data-violet-base:#8b5cf6;--data-cyan-base:#06b6d4;
  --data-badge-success-bg:#f0fdf4;--data-badge-success-text:#16a34a;--data-badge-success-border:#bbf7d0;
  --data-badge-warning-bg:#fffbeb;--data-badge-warning-text:#d97706;--data-badge-warning-border:#fde68a;
  --data-badge-danger-bg:#fef2f2;--data-badge-danger-text:#dc2626;--data-badge-danger-border:#fecaca;
  --data-badge-info-bg:#eff6ff;--data-badge-info-text:#2563eb;--data-badge-info-border:#bfdbfe;
  --data-badge-neutral-bg:#f8fafc;--data-badge-neutral-text:#475569;--data-badge-neutral-border:#cbd5e1;
  --bg-secondary:var(--app-bg);--bg-primary:var(--app-surface);--bg-tertiary:var(--app-surface-muted);
  --bg-hover:var(--app-accent-soft);--text-primary:var(--app-text-primary);
  --text-secondary:var(--app-text-regular);--text-tertiary:var(--app-text-muted);
  --border-color:var(--app-border);--border-primary:var(--app-border-strong);
  --border-hover:rgba(59,130,246,.28);
}
html[data-host=cpamp][data-theme=dark]{
  --app-bg:#0a0a0a;--app-bg-gradient:linear-gradient(120deg,#0b1324 0%,#0a1426 50%,#091521 100%);
  --app-bg-blob-1-start:#1b2a55;--app-bg-blob-1-end:#27325c;--app-bg-blob-2-start:#0f3d59;--app-bg-blob-2-end:#0c4a4f;
  --app-bg-blob-1-opacity:.35;--app-bg-blob-2-opacity:.3;--app-bg-blob-blur:70px;
  --app-surface:rgba(24,28,40,.9);--app-surface-strong:#1b1f2a;--app-surface-muted:rgba(255,255,255,.08);
  --app-border:rgba(255,255,255,.08);--app-border-strong:rgba(255,255,255,.12);
  --app-text-primary:#e5e5e5;--app-text-regular:#a3a3a3;--app-text-muted:#7a7a7a;
  --app-accent-soft:rgba(96,165,250,.18);--surface-subtle:rgba(255,255,255,.06);
  --glass-bg:rgba(24,28,40,.72);--glass-border:rgba(255,255,255,.1);
  --app-input-bg:#1b1f2a;--app-input-bg-focus:#1b1f2a;--app-input-border-focus:#60a5fa;
  --color-primary:#60a5fa;--color-primary-light-3:#93c5fd;--color-primary-dark-2:#3b82f6;
  --color-success:#4ade80;--success-color:var(--color-success);
  --primary-solid:#60a5fa;--primary-solid-hover:#3b82f6;--primary-ring:rgba(96,165,250,.22);
  --data-blue-base:#60a5fa;--data-green-base:#4ade80;--data-amber-base:#fbbf24;
  --data-red-base:#f87171;--data-violet-base:#a78bfa;--data-cyan-base:#22d3ee;
  --data-badge-success-bg:rgba(74,222,128,.14);--data-badge-success-text:#4ade80;--data-badge-success-border:rgba(74,222,128,.24);
  --data-badge-warning-bg:rgba(251,191,36,.14);--data-badge-warning-text:#fbbf24;--data-badge-warning-border:rgba(251,191,36,.24);
  --data-badge-danger-bg:rgba(248,113,113,.14);--data-badge-danger-text:#f87171;--data-badge-danger-border:rgba(248,113,113,.24);
  --data-badge-info-bg:rgba(96,165,250,.14);--data-badge-info-text:#60a5fa;--data-badge-info-border:rgba(96,165,250,.24);
  --data-badge-neutral-bg:rgba(148,163,184,.12);--data-badge-neutral-text:#94a3b8;--data-badge-neutral-border:rgba(148,163,184,.2);
}
*{box-sizing:border-box}
html,body{width:100%;height:100%;margin:0;overflow:hidden}
body{font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:var(--bg-secondary);color:var(--text-primary)}
html[data-host=cpamp] body{background-color:var(--app-bg);background-image:var(--app-bg-gradient)}
.sidebar{position:fixed;inset:0 auto 0 0;z-index:20;width:var(--sidebar-width);padding:14px 10px;
  background:var(--bg-primary);border-right:1px solid var(--border-color)}
.brand{display:flex;align-items:center;gap:10px;height:48px;padding:0 10px;font-size:17px;font-weight:750}
.brand-mark{display:grid;place-items:center;width:34px;height:34px;border-radius:9px;background:var(--primary-color);color:#fff}
.nav-placeholder{display:grid;gap:8px;margin-top:30px}
.nav-placeholder span{height:38px;padding:9px 12px;border-radius:9px;color:var(--text-secondary)}
.nav-placeholder span.active{background:var(--bg-tertiary);color:var(--text-primary);font-weight:650}
.navbar{position:fixed;z-index:30;display:flex;align-items:center;justify-content:space-between;height:var(--header-height);
  color:var(--text-secondary)}
.navbar-left{display:flex;align-items:center;gap:10px;min-width:0}
.mobile-menu,.theme-controls button{appearance:none;display:grid;place-items:center;width:36px;height:36px;padding:0;
  border:1px solid transparent;border-radius:10px;background:transparent;color:inherit;font:inherit;cursor:pointer}
.theme-controls{display:flex;gap:3px;padding:5px;border:1px solid var(--border-color);border-radius:14px;background:var(--bg-primary)}
.theme-controls button:hover,.theme-controls button.active{background:var(--bg-tertiary);color:var(--text-primary)}
.content{position:fixed;overflow:hidden}
#plugin-frame{display:block;width:100%;height:100%;border:0;background:var(--bg-secondary)}
html[data-host=cpamc]{--sidebar-width:216px;--header-height:80px}
html[data-host=cpamc] .navbar{inset:0 0 auto var(--sidebar-width);pointer-events:none}
html[data-host=cpamc] .navbar-left{display:none}
html[data-host=cpamc] .navbar-left>span{display:none}
html[data-host=cpamc] .theme-controls{position:absolute;top:24px;right:24px;pointer-events:auto;box-shadow:0 18px 44px #0000002b}
html[data-host=cpamc] .mobile-menu{display:none}
html[data-host=cpamc] .content{inset:0 0 0 var(--sidebar-width)}
html[data-host=cpamp]{--sidebar-width:210px;--header-height:50px}
html[data-host=cpamp] .sidebar{background:color-mix(in srgb,var(--app-surface) 70%,transparent)}
html[data-host=cpamp] .navbar{inset:0 0 auto var(--sidebar-width);padding:0 20px 0 8px;background:var(--app-surface);border-bottom:1px solid var(--app-border)}
html[data-host=cpamp] .theme-controls{padding:2px;border:0;background:transparent}
html[data-host=cpamp] .theme-white{display:none}
html[data-host=cpamp] .content{inset:var(--header-height) 0 0 var(--sidebar-width)}
html[data-host=cpamp] #plugin-frame{background:var(--bg-primary)}
@media(max-width:768px){
  .sidebar{display:none}
  html[data-host] .navbar{left:0}
  html[data-host] .content{left:0}
  html[data-host=cpamc] .navbar-left{display:block;position:absolute;top:12px;left:12px;pointer-events:auto}
  html[data-host=cpamc] .mobile-menu{display:grid;background:var(--bg-primary);border-color:var(--border-color);box-shadow:0 18px 44px #0000002b}
  html[data-host=cpamc] .theme-controls{top:12px;right:12px}
  html[data-host=cpamp] .navbar{padding-right:8px}
}
</style>
</head>
<body>
<aside class="sidebar">
  <div class="brand"><span class="brand-mark">◈</span><span>__HOST_LABEL__</span></div>
  <div class="nav-placeholder"><span>仪表盘</span><span>AI 提供商</span><span>插件管理</span><span class="active">账号设置</span></div>
</aside>
<header class="navbar">
  <div class="navbar-left"><button class="mobile-menu" title="菜单">☰</button><span>账号设置</span></div>
  <div class="theme-controls" aria-label="预览主题">
    <select id="host-language" aria-label="Language"><option value="en">English</option><option value="zh-CN">简体中文</option><option value="zh-TW">繁體中文</option><option value="ru">Русский</option></select>
    <button type="button" data-action="refresh" title="刷新">↻</button>
    <button type="button" data-theme-choice="light" title="浅色主题">◐</button>
    <button type="button" class="theme-white" data-theme-choice="white" title="白色主题">○</button>
    <button type="button" data-theme-choice="dark" title="深色主题">●</button>
  </div>
</header>
<main class="content"><iframe id="plugin-frame" src="/ui" title="账号设置插件"></iframe></main>
<script>
"use strict";
const HOST_MODE="__HOST_MODE__";
const INITIAL_THEME="__INITIAL_THEME__";
const root=document.documentElement;
const frame=document.getElementById("plugin-frame");
const language=document.getElementById("host-language");
language.value=root.lang;
language.onchange=()=>{root.lang=language.value;};
const systemDark=()=>!!matchMedia("(prefers-color-scheme:dark)").matches;
let selectedTheme=INITIAL_THEME;

function resolveTheme(choice){
  if(choice==="auto")return systemDark()?"dark":"white";
  if(HOST_MODE==="cpamp"&&choice==="light")return "white";
  return choice;
}

function cpampBridgeCSS(theme){
  const computed=getComputedStyle(root);
  const declarations=[];
  for(let index=0;index<computed.length;index++){
    const name=computed.item(index);
    if(!name.startsWith("--"))continue;
    const value=computed.getPropertyValue(name).trim();
    if(value)declarations.push("  "+name+":"+value+";");
  }
  const scope=":where(html[data-cpamp-plugin-host='true'])";
  return scope+"{\n"+declarations.join("\n")+"\ncolor-scheme:"+(theme==="dark"?"dark":"light")+";min-height:100%;background:var(--bg-primary);color:var(--text-primary)}\n"+
    scope+" :where(body){min-height:100vh;margin:0;background:var(--bg-primary);color:var(--text-primary);font-family:Inter,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;font-size:14px;line-height:1.5}\n"+
    scope+" :where(body,button,input,select,textarea){font-family:Inter,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif}\n"+
    scope+" :where(input:not([type=checkbox]):not([type=radio]),select,textarea){min-height:34px;border:1px solid var(--app-input-border);border-radius:var(--app-radius-sm);background:var(--app-input-bg);color:var(--text-primary);box-shadow:none}\n"+
    scope+" :where(button,[role=button]){min-height:34px;border:1px solid var(--border-color);border-radius:var(--app-radius-md);background:var(--app-surface-muted);color:var(--text-primary);font-weight:600;line-height:1.2}\n"+
    scope+" :where(thead,th){background:color-mix(in srgb,var(--bg-tertiary) 72%,var(--bg-primary));color:var(--text-secondary)}\n"+
    scope+" :where(.card,[class*=card],[class*=panel]){border-color:var(--border-color);background:var(--bg-primary);color:var(--text-primary)}";
}

function syncCPAMPFrame(theme){
  if(HOST_MODE!=="cpamp")return;
  let doc;
  try{doc=frame.contentDocument}catch(_){return}
  if(!doc||!doc.documentElement||!doc.head)return;
  const childRoot=doc.documentElement;
  childRoot.setAttribute("data-cpamp-plugin-host","true");
  childRoot.setAttribute("data-theme",theme==="dark"?"dark":"white");
  childRoot.classList.toggle("theme-dark",theme==="dark");
  childRoot.classList.toggle("theme-light",theme!=="dark");
  let style=doc.getElementById("cpamp-dummy-host-style");
  if(!style){
    style=doc.createElement("style");
    style.id="cpamp-dummy-host-style";
    const pluginStyle=doc.head.querySelector("style,link[rel~=stylesheet]");
    doc.head.insertBefore(style,pluginStyle);
  }
  style.textContent=cpampBridgeCSS(theme);
}

function applyTheme(choice){
  selectedTheme=choice;
  const theme=resolveTheme(choice);
  const activeChoice=choice==="auto"?(HOST_MODE==="cpamp"&&theme==="white"?"light":theme):choice;
  if(theme==="light")root.removeAttribute("data-theme");else root.setAttribute("data-theme",theme);
  document.querySelectorAll("[data-theme-choice]").forEach(button=>{
    button.classList.toggle("active",button.dataset.themeChoice===activeChoice);
  });
  syncCPAMPFrame(theme);
}

document.querySelectorAll("[data-theme-choice]").forEach(button=>{
  button.addEventListener("click",()=>applyTheme(button.dataset.themeChoice));
});
document.querySelector("[data-action=refresh]").addEventListener("click",()=>frame.contentWindow.location.reload());
frame.addEventListener("load",()=>applyTheme(selectedTheme));
matchMedia("(prefers-color-scheme:dark)").addEventListener("change",()=>{
  if(selectedTheme==="auto")applyTheme("auto");
});
applyTheme(INITIAL_THEME);
</script>
</body>
</html>
"""

AUTH_FILES = [
    {
        "auth_index": "auth-demo-gemini", "name": "gemini-test@example.com.json",
        "category": "gemini-cli", "email": "gemini-test@example.com", "disabled": True,
        "unavailable": False, "quota_supported": False,
    },
    {
        "auth_index": "auth-demo-qwen", "name": "qwen-test@example.com.json",
        "category": "qwen", "email": "qwen-test@example.com", "disabled": False,
        "unavailable": False, "quota_supported": False,
    },
    {
        "auth_index": "auth-demo-codex-plus",
        "name": "codex-dev-team@example.com.json",
        "category": "codex",
        "email": "dev-team@example.com",
        "disabled": False,
        "unavailable": False,
        "priority": 10,
        "weight": 10,
        "quota_supported": True,
    },
    {
        "auth_index": "auth-demo-claude",
        "name": "claude-platform@example.com.json",
        "category": "claude",
        "email": "platform@example.com",
        "disabled": False,
        "unavailable": False,
        "priority": 0,
        "quota_supported": True,
    },
    {
        "auth_index": "auth-demo-codex-pro",
        "name": "codex-automation@example.com.json",
        "category": "codex",
        "email": "automation@example.com",
        "disabled": False,
        "unavailable": False,
        "priority": 99,
        "weight": 1000000,
        "quota_supported": True,
    },
    {
        "auth_index": "auth-demo-antigravity",
        "name": "antigravity-ai-lab@example.com.json",
        "category": "antigravity",
        "email": "ai-lab@example.com",
        "disabled": False,
        "unavailable": False,
        "priority": 5,
        "weight": 5,
        "quota_supported": True,
    },
    {
        "auth_index": "auth-demo-kimi",
        "name": "kimi-research@example.com.json",
        "category": "kimi",
        "email": "research@example.com",
        "disabled": False,
        "unavailable": True,
        "priority": 0,
        "weight": 0,
        "quota_supported": True,
    },
    {
        "auth_index": "auth-demo-xai-active",
        "name": "xai-research@example.com.json",
        "category": "xai",
        "email": "research@example.com",
        "disabled": False,
        "unavailable": False,
        "priority": 1,
        "quota_supported": True,
    },
]

AI_PROVIDER_LISTS = {
    "claude-api-key": [
        {"api-key": "sk-ant-dummy-claude-0000000001", "auth-index": "ai-demo-claude"},
        {"api-key": "sk-dummy-deepseek-anthropic-0001", "base-url": "https://api.deepseek.com/anthropic",
         "priority": 5, "weight": 3, "auth-index": "ai-demo-claude-deepseek"},
    ],
    "codex-api-key": [
        {"api-key": "sk-proxy-dummy-codex-relay-7f3a", "base-url": "https://relay.example.com/v1", "prefix": "codex",
         "priority": 10, "weight": 10, "auth-index": "ai-demo-codex-relay"},
    ],
    "gemini-api-key": [
        {"api-key": "AIzaDummyGeminiKey0000000000001", "excluded-models": ["gemini-private", "*"], "auth-index": "ai-demo-gemini"},
    ],
    "interactions-api-key": [{"api-key": "dummy-interactions-key-001", "auth-index": "ai-demo-interactions"}],
    "vertex-api-key": [
        {"api-key": "vertex-dummy-key-0000000000001", "base-url": "https://aiplatform.googleapis.com", "auth-index": "ai-demo-vertex"},
    ],
    "xai-api-key": [
        {"api-key": "xai-dummy-key-00000000000000001", "base-url": "https://api.x.ai/v1", "auth-index": "ai-demo-xai"},
    ],
    "meta-api-key": [
        {"api-key": "meta-dummy-key-0000000000000001", "base-url": "https://api.llama.com/compat/v1", "auth-index": "ai-demo-meta"},
    ],
    "openai-compatibility": [
        {"name": "openrouter", "base-url": "https://openrouter.ai/api/v1", "priority": 3,
         "api-key-entries": [{"api-key": "sk-or-v1-dummy-000000000000000001", "weight": 2, "auth-index": "ai-demo-openrouter"}]},
        {"name": "deepseek", "base-url": "https://api.deepseek.com/v1", "api-key-entries": [
            {"api-key": "sk-dummy-deepseek-00000000000001", "auth-index": "ai-demo-deepseek-1"},
            {"api-key": "sk-dummy-deepseek-00000000000002", "weight": 0, "auth-index": "ai-demo-deepseek-2"},
        ]},
        {"name": "kimi", "base-url": "https://api.moonshot.cn/v1",
         "api-key-entries": [{"api-key": "sk-dummy-kimi-00000000000000001", "auth-index": "ai-demo-kimi"}]},
        {"name": "siliconflow", "base-url": "https://api.siliconflow.cn/v1",
         "api-key-entries": [{"api-key": "sk-dummy-siliconflow-expired-01", "auth-index": "ai-demo-siliconflow"}]},
        {"name": "stepfun", "base-url": "https://api.stepfun.ai/v1",
         "api-key-entries": [{"api-key": "dummy-stepfun-key-000000000001", "auth-index": "ai-demo-stepfun"}]},
        {"name": "local", "base-url": "http://127.0.0.1:11434/v1", "auth-index": "ai-demo-local"},
        {"name": "legacy", "base-url": "https://legacy.example.com/v1", "disabled": True,
         "api-key-entries": [{"api-key": "sk-dummy-legacy-000000000000001"}]},
    ],
}

AI_BALANCE_RESPONSES = {
    "https://api.deepseek.com/user/balance": (200, {"is_available": True, "balance_infos": [
        {"currency": "CNY", "total_balance": "37.07", "granted_balance": "2.29", "topped_up_balance": "34.78"}]}),
    "https://openrouter.ai/api/v1/credits": (200, {"data": {"total_credits": 50, "total_usage": 38.4127}}),
    "https://openrouter.ai/api/v1/key": (200, {"data": {"label": "sk-or-v1-dum...001", "limit": 20, "limit_remaining": 6.25, "usage": 13.75}}),
    "https://api.moonshot.cn/v1/users/me/balance": (200, {"code": 0, "data": {
        "available_balance": 49.58894, "voucher_balance": 46.58893, "cash_balance": 3.00001}, "scode": "0x0", "status": True}),
    "https://api.siliconflow.cn/v1/user/info": (401, {"code": 30014, "data": None, "message": "Token is invalid."}),
    "https://api.stepfun.ai/v1/accounts": (200, {"object": "account", "type": "prepaid", "balance": 0, "total_cash_balance": 10, "total_voucher_balance": 5}),
}
AUTH_FILES.sort(key=lambda file: (file["category"],file["name"]))
NOW=lambda:datetime.now(timezone.utc).isoformat()

def demo_health(seed):
    end=datetime.now(timezone.utc).replace(second=0,microsecond=0)
    end=end.replace(minute=end.minute//10*10)
    buckets=[]
    for i in range(20):
        start=end-timedelta(minutes=(19-i)*10)
        buckets.append({"time":start.strftime("%H:%M")+"-"+(start+timedelta(minutes=10)).strftime("%H:%M"),
                        "success":0 if seed==0 else (i+seed)%5,
                        "failed":0 if seed==0 else (1 if i%7==seed%7 else 0)})
    return {"recent_requests":buckets,"success":sum(b["success"] for b in buckets),"failed":sum(b["failed"] for b in buckets)}

def demo_provider_health():
    result={}
    for kind,entries in AI_PROVIDER_LISTS.items():
        for i,entry in enumerate(entries):
            provider=(entry.get("name","") if kind=="openai-compatibility" else kind.removesuffix("-api-key")).lower()
            for key in entry.get("api-key-entries",[entry]):
                if key.get("api-key"):
                    result.setdefault(provider,{})[entry.get("base-url","")+"|"+key["api-key"]]=demo_health(i+1)
    return result

PLUGIN_LOGS=[{"id":"2","at":NOW(),"level":"info","message":"Plugin configuration applied"}, {"id":"1","at":NOW(),"level":"info","message":"Account controls enabled"}]
ERRORS=[{"id":"1","at":NOW(),"provider":"codex","model":"demo-model","auth_index":"auth-demo-codex-plus","status_code":429,"error_type":"rate_limit_exceeded","body":"Synthetic preview: upstream rate limit reached."}]
def moderation_defaults():
    return {"mode":"off","strategy":"keywords","scope":"latest_user","model_mode":"all","models":[],"rules":[],"failure_action":"block","log_excerpt":False,
            "api":{"url":"","model":"omni-moderation-latest","key_env":"CPA_POLICY_MODERATION_API_KEY","timeout_ms":3000,"max_in_flight":8,"thresholds":{}}}

MODERATION_SETTINGS=moderation_defaults()
MODERATION_EVENTS=[{"id":"2","at":NOW(),"auth_index":"auth-demo-codex-plus","model":"demo-model","source_format":"codex","status":"flagged","action":"block","rules":["demo-rule"]},
                   {"id":"1","at":NOW(),"auth_index":"auth-demo-claude","model":"demo-claude","source_format":"claude","status":"error","action":"allow","reason":"api_timeout_or_canceled"}]

def normalized_moderation(data,policy_only=False):
    defaults=moderation_defaults()
    result={k:data.get(k,v) for k,v in defaults.items() if k!="api"}
    for key,values in {"mode":["off","observe","pre_block"],"strategy":["keywords","api","keywords_and_api"],"scope":["latest_user","all_users"],"model_mode":["all","include","exclude"],"failure_action":["allow","block"]}.items():
        if result[key] not in values:raise ValueError("Invalid moderation "+key)
    if result["mode"]!="off" and result["strategy"]=="keywords" and not result["rules"]:raise ValueError("Keyword moderation requires at least one rule")
    ids=set()
    for rule in result["rules"]:
        if rule.get("id") in ids or not rule.get("pattern"):raise ValueError("Invalid moderation rule")
        ids.add(rule.get("id"))
        if rule.get("kind")=="regex":re.compile(rule["pattern"])
    if not policy_only:
        api={**defaults["api"],**data.get("api",{})}
        if result["mode"]!="off" and result["strategy"]!="keywords" and not api["url"]:raise ValueError("Moderation API URL is required")
        result["api"]=api
    return result

def demo_moderation_test(data):
    cfg=normalized_moderation(data.get("config",MODERATION_SETTINGS))
    model=data.get("model","")
    if cfg["mode"]=="off" or cfg["model_mode"]=="include" and model not in cfg["models"] or cfg["model_mode"]=="exclude" and model in cfg["models"]:
        return {"status":"skipped","action":"allow","reason":"policy_not_applicable"}
    text=data.get("text","")
    hits=[r["id"] for r in cfg["rules"] if (r["pattern"].lower() in text.lower() if r["kind"]=="keyword" else re.search(r["pattern"],text))] if cfg["strategy"]!="api" else []
    if hits:return {"status":"flagged","action":"block" if cfg["mode"]=="pre_block" else "observe","rules":hits}
    if cfg["strategy"]!="keywords":return {"status":"error","action":"block" if cfg["mode"]=="pre_block" and cfg["failure_action"]=="block" else "allow","reason":"api_not_configured"}
    return {"status":"passed","action":"allow"}

def normalized_location(value):
    result={"enabled":bool(value.get("enabled",False)),**{key:str(value.get(key,"")).strip() for key in ("country","region","city","timezone")}}
    result["country"]=result["country"].upper()
    if result["enabled"]:
        if any(not result[key] for key in ("country","region","city","timezone")):raise ValueError("Complete all location fields")
        ZoneInfo(result["timezone"])
    return result

def normalized_policies(data):
    result={}
    for index,policy in data.items():
        count=policy.get("max_concurrency",0)
        if type(count) is not int or count<0 or count>100000:raise ValueError("Invalid concurrency limit")
        mode=policy.get("location_mode","inherit")
        if mode not in ("inherit","custom","preserve"):raise ValueError("Invalid location mode")
        place=normalized_location({**policy.get("location",{}),"enabled":mode=="custom"});place.pop("enabled")
        concurrency_mode=policy.get("concurrency_mode","")
        if concurrency_mode not in ("","inherit","custom"):raise ValueError("Invalid concurrency mode")
        result[index]={"max_concurrency":0 if concurrency_mode=="inherit" else count,"location_mode":mode,"location":place}
        risk=policy.get("moderation")
        if risk and risk.get("mode")=="off":result[index]["moderation"]={"mode":"off"}
        elif risk and risk.get("mode")=="custom":result[index]["moderation"]={"mode":"custom","policy":normalized_moderation(risk["policy"],True)}
    return result

def normalized_concurrency(data):
    result={"account_queue_capacity":0,"queue_timeout_seconds":30}
    if set(data)-set(result)-{"default_max_concurrency"}:raise ValueError("Unknown concurrency setting")
    result.update({key:value for key,value in data.items() if key!="default_max_concurrency"})
    for field,minimum,maximum in (("account_queue_capacity",0,1000),("queue_timeout_seconds",1,120)):
        value=result[field]
        if type(value) is not int or not minimum<=value<=maximum:raise ValueError("Invalid "+field)
    return result

class Handler(BaseHTTPRequestHandler):
    host_mode="standalone"
    initial_theme="auto"
    def respond(self,status,payload,html=False):
        raw=payload.encode() if html else json.dumps(payload,ensure_ascii=False).encode()
        self.send_response(status);self.send_header("Content-Type","text/html; charset=utf-8" if html else "application/json")
        self.send_header("Cache-Control","no-store");self.send_header("Content-Length",str(len(raw)));self.end_headers();self.wfile.write(raw)
    def do_GET(self):self.dispatch("GET")
    def do_POST(self):self.dispatch("POST")
    def do_PATCH(self):self.dispatch("PATCH")
    def do_DELETE(self):self.dispatch("DELETE")
    def dispatch(self,method):
        global LOCATION_SETTINGS,CREDENTIAL_POLICIES,CONCURRENCY_SETTINGS,MODERATION_SETTINGS
        parsed=urlparse(self.path);path=parsed.path;query=parse_qs(parsed.query)
        if method=="GET" and path=="/" and self.host_mode!="standalone":
            label="CPAMC" if self.host_mode=="cpamc" else "CPAMP"
            return self.respond(200,HOST_SHELL.replace("__HOST_MODE__",self.host_mode).replace("__HOST_LABEL__",label).replace("__INITIAL_THEME__",self.initial_theme),True)
        if method=="GET" and path in ("/","/ui",RESOURCE_BASE+"/ui"):
            catalogs={lang:json.loads((UI_DIR/"locales"/(lang+".json")).read_text()) for lang in ("en","zh-CN")}
            messages="const BILLING_MESSAGES = "+json.dumps(catalogs).replace("<","\\u003c")+";\n"
            body=UI_INCLUDE.sub(lambda m:(messages if m[1]=="i18n.js" else "")+(UI_DIR/m[1]).read_text(),(UI_DIR/"ui.html").read_text())
            if self.host_mode!="standalone":body=body.replace("</head>",'<script>localStorage.setItem("managementKey", JSON.stringify("demo-management-key"));</script></head>')
            return self.respond(200,body,True)
        if self.headers.get("Authorization")!="Bearer demo-management-key":return self.respond(401,{"error":{"message":"Invalid management key"}})
        data={}
        try:
            if method!="GET":data=json.loads(self.rfile.read(int(self.headers.get("Content-Length",0))) or b"{}")
            route=(method,path)
            if route==("GET",API_BASE+"/location"):return self.respond(200,{"custom_location":LOCATION_SETTINGS,"effective_timezone":LOCATION_SETTINGS["timezone"] if LOCATION_SETTINGS["enabled"] else ""})
            if route==("GET",API_BASE+"/credential-policies"):return self.respond(200,{"policies":CREDENTIAL_POLICIES,"active":{},"waiting":{},"concurrency":CONCURRENCY_SETTINGS})
            if route==("GET",API_BASE+"/moderation"):return self.respond(200,{"moderation":MODERATION_SETTINGS,"api_key_configured":False})
            if route==("POST",API_BASE+"/moderation/validate"):return self.respond(200,{"moderation":normalized_moderation(data)})
            if route==("POST",API_BASE+"/moderation/test"):return self.respond(200,{"result":demo_moderation_test(data)})
            if route==("GET",API_BASE+"/moderation/events"):return self.respond(200,{"entries":MODERATION_EVENTS})
            if route==("GET",API_BASE+"/concurrency"):return self.respond(200,{"concurrency":CONCURRENCY_SETTINGS})
            if route==("POST",API_BASE+"/concurrency/validate"):return self.respond(200,{"concurrency":normalized_concurrency(data)})
            if route==("POST",API_BASE+"/location/validate"):return self.respond(200,{"custom_location":normalized_location(data)})
            if route==("POST",API_BASE+"/credential-policies/validate"):return self.respond(200,{"policies":normalized_policies(data)})
            if route==("PATCH",API_BASE+"/config"):
                if "moderation" in data:MODERATION_SETTINGS=normalized_moderation(data["moderation"])
                if "concurrency" in data:CONCURRENCY_SETTINGS=normalized_concurrency(data["concurrency"])
                if "custom_location" in data:LOCATION_SETTINGS=normalized_location(data["custom_location"])
                if "credential_policies" in data:CREDENTIAL_POLICIES=normalized_policies(data["credential_policies"])
                return self.respond(200,{"ok":True})
            if route==("POST",API_BASE+"/location/detect"):
                place={"country":"JP","region":"Tokyo","city":"Tokyo","timezone":"Asia/Tokyo"} if data.get("auth_index") else {"country":"HK","region":"Hong Kong","city":"Hong Kong","timezone":"Asia/Hong_Kong"}
                return self.respond(200,{"location":place,"exit_ips":["203.0.113.10"],"source":"demo","detected_at":NOW()})
            if route==("GET",API_BASE+"/auth-files"):return self.respond(200,{"files":AUTH_FILES})
            if route==("GET","/v0/management/auth-files"):
                return self.respond(200,{"files":[{**file,**demo_health(i)} for i,file in enumerate(AUTH_FILES)]})
            if route==("GET","/v0/management/api-key-usage"):return self.respond(200,demo_provider_health())
            if route==("GET",API_BASE+"/auth-files/quota"):
                file=next((file for file in AUTH_FILES if file["auth_index"]==query.get("auth_index",[""])[0]),None)
                if file is None:return self.respond(404,{"error":"Auth file not found"})
                return self.respond(200,{"fetched_at":NOW(),"auth_revision":file.get("cache_revision",""),"plan":"Plus","quota":[{"label":"5-hour limit","remaining_percent":72,"reset_at":(datetime.now(timezone.utc)+timedelta(hours=3)).isoformat()}]})
            if route==("POST",API_BASE+"/auth-files/quota/reset"):return self.respond(200,{"reset":True,"cooldown_cleared":True})
            if route==("POST","/v0/management/auth-files/refresh"):
                file=next((file for file in AUTH_FILES if file["name"]==data.get("name") and file["auth_index"]==data.get("auth_index")),None)
                if file is None:return self.respond(404,{"error":"Auth file not found"})
                file["cache_revision"]=NOW()
                return self.respond(200,{"ok":True})
            if route==("PATCH","/v0/management/auth-files/status"):
                for file in AUTH_FILES:
                    if file["auth_index"]==data.get("auth_index"):file["disabled"]=bool(data.get("disabled"))
                return self.respond(200,{"ok":True})
            if method=="GET" and path.removeprefix("/v0/management/") in AI_PROVIDER_LISTS:
                key=path.removeprefix("/v0/management/");return self.respond(200,{key:AI_PROVIDER_LISTS[key]})
            if method=="PATCH" and path.removeprefix("/v0/management/") in AI_PROVIDER_LISTS:
                key=path.removeprefix("/v0/management/")
                index=data.get("index")
                if type(index) is not int or not 0<=index<len(AI_PROVIDER_LISTS[key]):raise ValueError("Item not found")
                patch=data.get("value",{})
                if key=="openai-compatibility":
                    if set(patch)!={"disabled"} or type(patch["disabled"]) is not bool:raise ValueError("Invalid switch patch")
                elif set(patch)!={"excluded-models"} or not isinstance(patch["excluded-models"],list):raise ValueError("Invalid switch patch")
                AI_PROVIDER_LISTS[key][index].update(patch)
                return self.respond(200,{"status":"ok"})
            if route==("POST","/v0/management/api-call"):
                status,body=AI_BALANCE_RESPONSES.get(data.get("url"),(404,{}))
                return self.respond(200,{"status_code":status,"body":json.dumps(body)})
            if method=="GET" and path in (API_BASE+"/errors",API_BASE+"/plugin-logs"):
                entries=ERRORS if path.endswith("/errors") else PLUGIN_LOGS
                level=query.get("level",[""])[0];before=int(query.get("before_id",[0])[0]);limit=int(query.get("limit",[50])[0])
                entries=[item for item in entries if (not level or item.get("level")==level) and (not before or int(item["id"])<before)]
                return self.respond(200,{"entries":entries[:limit],"next_before_id":entries[limit-1]["id"] if len(entries)>limit else ""})
            if route==("DELETE",API_BASE+"/plugin-logs"):
                count=len(PLUGIN_LOGS);PLUGIN_LOGS.clear();return self.respond(200,{"deleted":count})
        except (ValueError,KeyError,TypeError) as err:return self.respond(400,{"error":{"message":str(err)}})
        self.respond(404,{"error":{"message":"Route not found"}})
    def log_message(self,*_):pass

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument("--port",type=int,default=8765);parser.add_argument("--host",choices=("standalone","cpamc","cpamp"),default="standalone");parser.add_argument("--theme",default="auto")
    args=parser.parse_args();Handler.host_mode=args.host;Handler.initial_theme=args.theme
    server=ThreadingHTTPServer(("127.0.0.1",args.port),Handler)
    print(f"Account controls preview: http://127.0.0.1:{args.port}/ui",flush=True)
    try:server.serve_forever()
    except KeyboardInterrupt:pass
    finally:server.server_close()
if __name__=="__main__":main()
