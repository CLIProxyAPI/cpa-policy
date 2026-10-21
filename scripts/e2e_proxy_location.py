#!/usr/bin/env python3
"""Exercise the built library in a real CPA process against a local fake upstream."""
import argparse
import copy
import json
import queue
import shutil
import sqlite3
import socket
import subprocess
import tempfile
import threading
import time
from datetime import datetime
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, build_opener, ProxyHandler
from zoneinfo import ZoneInfo

from dummy_provider import DummyProviderHandler, ThreadingHTTPServer

BASE = "/v0/management/plugins/cpa-codex-controls"
CAPTURED = queue.Queue()
HOLD_STARTED = threading.Event()
HOLD_RELEASE = threading.Event()
FAIL_NEXT = threading.Event()


class CaptureProvider(DummyProviderHandler):
    def handle(self):
        try:
            super().handle()
        except ConnectionResetError:
            # CPA can close an idle upstream connection during config reload.
            pass

    def do_POST(self):
        if FAIL_NEXT.is_set():
            FAIL_NEXT.clear()
            self.read_json_body()
            self.send_json(400, {"error": {"message": "diagnostic-test location-test-key", "type": "bad_request"}, "api_key": "dummy-upstream-secret"})
            return
        super().do_POST()

    def read_json_body(self):
        body = super().read_json_body()
        captured = copy.deepcopy(body)
        captured["_test_transport"] = getattr(self.server, "test_label", "direct")
        CAPTURED.put(captured)
        if "hold-for-concurrency-test" in json.dumps(body.get("input")):
            HOLD_STARTED.set()
            assert HOLD_RELEASE.wait(15), "held request was never released"
        return body

    def log_message(self, *_):
        pass


def available_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cpa", type=Path, required=True)
    parser.add_argument("--plugin", type=Path, required=True)
    args = parser.parse_args()
    cpa, library = args.cpa.resolve(), args.plugin.resolve()
    assert cpa.is_file() and library.is_file(), "CPA binary and plugin library are required"
    upstream = ThreadingHTTPServer(("127.0.0.1", 0), CaptureProvider)
    threading.Thread(target=upstream.serve_forever, daemon=True).start()
    proxies = []
    for label in ("tokyo", "newyork"):
        proxy = ThreadingHTTPServer(("127.0.0.1", 0), CaptureProvider)
        proxy.test_label = label
        threading.Thread(target=proxy.serve_forever, daemon=True).start()
        proxies.append(proxy)
    proxy_urls = [f"http://test-user:test-password@127.0.0.1:{proxy.server_port}" for proxy in proxies]
    host = None
    try:
        with tempfile.TemporaryDirectory(prefix="cpa-location-e2e-") as directory:
            root = Path(directory)
            (root / "plugins").mkdir()
            shutil.copy2(library, root / "plugins" / ("cpa-codex-controls" + library.suffix))
            port = available_port()
            config = {
                "host": "127.0.0.1", "port": port, "auth-dir": str(root / "auth"),
                "api-keys": ["location-test-key"],
                "remote-management": {"allow-remote": False, "secret-key": "location-management-key", "disable-control-panel": True},
                "plugins": {"enabled": True, "dir": str(root / "plugins"), "configs": {"cpa-codex-controls": {
                    "enabled": True, "priority": 100, "state_file": str(root / "billing.db"), "diagnostics_file": str(root / "diagnostics.db")
                }}},
                "codex-api-key": [{"api-key": "dummy-upstream-key", "base-url": f"http://127.0.0.1:{upstream.server_port}/v1", "models": [{"name": "location-test", "is-compat": True}]}],
            }
            for i, model in enumerate(("tokyo-test", "newyork-test")):
                config["codex-api-key"].append({"api-key": "dummy-proxy-key-" + str(i), "base-url": "http://mock-upstream.invalid/v1", "proxy-url": proxy_urls[i], "models": [{"name": model, "is-compat": True}]})
            with sqlite3.connect(root / "billing.db") as legacy:
                legacy.executescript("""
                    CREATE TABLE plugin_logs(id INTEGER PRIMARY KEY, at INTEGER, level TEXT, message TEXT);
                    CREATE TABLE request_events(id INTEGER PRIMARY KEY, at INTEGER, provider TEXT, auth_index TEXT, upstream_model TEXT, billing_model TEXT, failed INTEGER, latency_ms INTEGER, ttft_ms INTEGER);
                    CREATE TABLE request_errors(request_event_id INTEGER PRIMARY KEY, status_code INTEGER, error_type TEXT, body TEXT);
                """)
                legacy.execute("INSERT INTO plugin_logs VALUES(1,?,'info','historical diagnostic log')", (time.time_ns(),))
                legacy.execute("INSERT INTO request_events VALUES(1,?,'codex','old-auth','old-model','',1,123,40)", (time.time_ns(),))
                legacy.execute("INSERT INTO request_errors VALUES(1,503,'upstream_error','historical upstream failure')")
            original_history = (root / "billing.db").read_bytes()
            (root / "config.yaml").write_text(json.dumps(config))
            with (root / "host.log").open("w+") as log:
                host = subprocess.Popen([str(cpa), "-config", str(root / "config.yaml"), "-local-model"], cwd=root, stdout=log, stderr=log)
                opener = build_opener(ProxyHandler({}))

                def call(method, path, body=None, key="location-management-key"):
                    data = json.dumps(body).encode() if body is not None else None
                    request = Request(f"http://127.0.0.1:{port}" + path, data=data, method=method,
                                      headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
                    with opener.open(request, timeout=20) as response:
                        raw = response.read()
                        return json.loads(raw) if response.headers.get_content_type() == "application/json" else raw

                def wait_for(predicate):
                    deadline = time.monotonic() + 30
                    while time.monotonic() < deadline:
                        if host.poll() is not None:
                            raise AssertionError("CPA exited: " + (root / "host.log").read_text()[-4000:])
                        try:
                            if predicate():
                                return
                        except (HTTPError, URLError, ConnectionError):
                            pass
                        time.sleep(0.1)
                    raise AssertionError("CPA readiness/configuration timed out: " + (root / "host.log").read_text()[-4000:])

                try:
                    wait_for(lambda: call("GET", BASE + "/location")["effective_timezone"] == "")

                    for route in ("/keys", "/plans", "/routes", "/prices", "/analysis", "/events", "/ai-providers/usage", "/auth-files/history"):
                        try:
                            call("GET", BASE + route)
                            raise AssertionError("retired API is still available: " + route)
                        except HTTPError as error:
                            assert error.code == 404, (route, error.code)
                    environment = "<environment_context><current_date>2000-01-01</current_date><timezone>UTC</timezone><cwd>/workspace</cwd></environment_context>"
                    desktop = "<codex_apps_client_time_context><current_date>2000-01-01</current_date><timezone>UTC</timezone></codex_apps_client_time_context>"
                    request = {
                        "model": "location-test", "input": [
                            {"role": "user", "content": [{"type": "input_text", "text": environment}]},
                            {"role": "developer", "content": [{"type": "input_text", "text": desktop}]},
                            {"role": "user", "content": [{"type": "input_text", "text": "Keep UTC and 2000-01-01 in this message."}]},
                        ],
                        "tools": [{"type": "web_search_preview", "user_location": {"type": "approximate", "timezone": "UTC"}}],
                    }

                    def send(stream, model="location-test"):
                        call("POST", "/v1/responses", dict(request, stream=stream, model=model), key="location-test-key")
                        return CAPTURED.get(timeout=3)

                    untouched = send(False)
                    assert untouched["input"] == request["input"], "default must preserve input"
                    assert untouched["tools"][0]["user_location"]["timezone"] == "UTC"
                    settings = {"enabled": True, "country": "US", "region": "California", "city": "Los Angeles", "timezone": "America/Los_Angeles"}
                    assert call("POST", BASE + "/location/validate", settings)["custom_location"] == settings
                    assert call("GET", BASE + "/location")["effective_timezone"] == "", "validation changed live configuration"
                    call("PATCH", BASE + "/config", {"custom_location": settings})
                    wait_for(lambda: call("GET", BASE + "/location")["effective_timezone"] == settings["timezone"])
                    for stream in (False, True):
                        before = datetime.now(ZoneInfo(settings["timezone"])).strftime("%Y-%m-%d")
                        captured = send(stream)
                        after = datetime.now(ZoneInfo(settings["timezone"])).strftime("%Y-%m-%d")
                        for index, source in enumerate((environment, desktop)):
                            actual = captured["input"][index]["content"][0]["text"]
                            assert actual in [source.replace("2000-01-01", date).replace(">UTC<", ">America/Los_Angeles<") for date in (before, after)], actual
                        assert captured["input"][2] == request["input"][2], "ordinary text changed"
                        assert captured["tools"][0]["user_location"] == {"type": "approximate", **{k: v for k, v in settings.items() if k != "enabled"}}
                    tokyo = {"enabled": True, "country": "JP", "region": "Tokyo", "city": "Tokyo", "timezone": "Asia/Tokyo"}
                    newyork = {"enabled": True, "country": "US", "region": "New York", "city": "New York", "timezone": "America/New_York"}
                    for stream in (False, True):
                        for model, label, loc in (("tokyo-test", "tokyo", settings), ("newyork-test", "newyork", settings), ("location-test", "direct", settings)):
                            captured = send(stream, model)
                            assert captured["_test_transport"] == label, captured
                            assert captured["tools"][0]["user_location"] == {"type": "approximate", **{k:v for k,v in loc.items() if k != "enabled"}}
                            for item in captured["input"][:2]:
                                assert ">" + loc["timezone"] + "<" in item["content"][0]["text"]
                                assert datetime.now(ZoneInfo(loc["timezone"])).strftime("%Y-%m-%d") in item["content"][0]["text"]
                    # The global switch controls credentials without individual policies.
                    call("PATCH", BASE + "/config", {"custom_location": dict(settings, enabled=False)})
                    wait_for(lambda: call("GET", BASE + "/location")["effective_timezone"] == "")
                    assert send(False, "tokyo-test")["input"] == request["input"]
                    assert send(False)["input"] == request["input"]
                    call("PATCH", BASE + "/config", {"custom_location": settings})
                    wait_for(lambda: call("GET", BASE + "/location")["effective_timezone"] == settings["timezone"])
                    persisted = call("GET", BASE + "/config")
                    assert persisted["priority"] == 100 and persisted["enabled"] is True and persisted["state_file"] == str(root / "billing.db")
                    try:
                        call("POST", BASE + "/location/validate", dict(settings, timezone="Bad/Zone"))
                        raise AssertionError("invalid timezone was accepted")
                    except HTTPError as error:
                        assert error.code == 400
                    call("PATCH", BASE + "/config", {"custom_location": dict(settings, enabled=False)})
                    wait_for(lambda: call("GET", BASE + "/location")["effective_timezone"] == "")
                    assert send(False)["input"] == request["input"], "disable did not restore passthrough"
                    # Policies bind to the host index used by the existing provider table.
                    entries = call("GET", "/v0/management/codex-api-key")["codex-api-key"]
                    first_index = entries[0]["auth-index"]
                    tokyo_index = entries[1]["auth-index"]
                    policy_location = {k:v for k,v in tokyo.items() if k != "enabled"}
                    policies = {first_index: {"max_concurrency": 1, "location_mode": "custom", "location": policy_location},
                                tokyo_index: {"max_concurrency": 0, "location_mode": "preserve", "location": policy_location}}
                    call("POST", BASE + "/credential-policies/validate", policies)
                    call("PATCH", BASE + "/config", {"credential_policies": policies, "custom_location": settings})
                    wait_for(lambda: call("GET", BASE + "/credential-policies")["policies"] == policies)
                    assert send(False)["tools"][0]["user_location"]["timezone"] == "Asia/Tokyo", "per-row did not override global"
                    assert send(False, "tokyo-test")["input"] == request["input"], "preserve did not opt out of global"
                    # Explicit account on works while global is off; restoring inheritance follows global again.
                    call("PATCH", BASE + "/config", {"custom_location": dict(settings, enabled=False)})
                    wait_for(lambda: call("GET", BASE + "/location")["effective_timezone"] == "")
                    assert send(False)["tools"][0]["user_location"]["timezone"] == "Asia/Tokyo"
                    inherited = copy.deepcopy(policies)
                    inherited[first_index]["location_mode"] = "inherit"
                    call("PATCH", BASE + "/config", {"credential_policies": inherited})
                    wait_for(lambda: call("GET", BASE + "/credential-policies")["policies"] == inherited)
                    assert send(False)["input"] == request["input"]
                    call("PATCH", BASE + "/config", {"custom_location": settings})
                    wait_for(lambda: call("GET", BASE + "/location")["effective_timezone"] == settings["timezone"])
                    assert send(False)["tools"][0]["user_location"]["timezone"] == settings["timezone"]
                    call("PATCH", BASE + "/config", {"credential_policies": policies})
                    wait_for(lambda: call("GET", BASE + "/credential-policies")["policies"] == policies)
                    # Both response modes retain their credential slot through execution.
                    from concurrent.futures import ThreadPoolExecutor
                    for stream in (False, True):
                        HOLD_STARTED.clear()
                        HOLD_RELEASE.clear()
                        held_request = copy.deepcopy(request)
                        held_request["input"][2]["content"][0]["text"] = "hold-for-concurrency-test"
                        with ThreadPoolExecutor() as executor:
                            pending = executor.submit(call, "POST", "/v1/responses", dict(held_request, stream=stream), "location-test-key")
                            try:
                                assert HOLD_STARTED.wait(8), "upstream did not start"
                                CAPTURED.get(timeout=3)
                                assert call("GET", BASE + "/credential-policies")["active"][first_index] == 1
                                try:
                                    send(stream)
                                    raise AssertionError("credential concurrency limit did not reject")
                                except HTTPError as error:
                                    assert error.code == 429 and error.headers.get("Retry-After") == "1"
                                    assert "upstream_concurrency_limit" in error.read().decode()
                                assert send(False, "tokyo-test")["input"] == request["input"], "independent credential blocked"
                            finally:
                                HOLD_RELEASE.set()
                            pending.result(timeout=10)
                        wait_for(lambda: call("GET", BASE + "/credential-policies")["active"].get(first_index, 0) == 0)
                        assert send(False)["tools"][0]["user_location"]["timezone"] == "Asia/Tokyo"
                        wait_for(lambda: call("GET", BASE + "/credential-policies")["active"].get(first_index, 0) == 0)
                    # Explicit account limits, bounded queues and cancellation in the real C ABI.
                    queued_policies = copy.deepcopy(policies)
                    queued_policies[first_index]["max_concurrency"] = 1
                    concurrency = {"account_queue_capacity": 1, "queue_timeout_seconds": 2}
                    call("PATCH", BASE + "/config", {"credential_policies": queued_policies, "concurrency": concurrency})
                    wait_for(lambda: call("GET", BASE + "/concurrency")["concurrency"] == concurrency)
                    wait_for(lambda: call("GET", BASE + "/credential-policies")["policies"] == queued_policies)
                    for stream in (False, True):
                        HOLD_STARTED.clear(); HOLD_RELEASE.clear()
                        with ThreadPoolExecutor() as executor:
                            running = executor.submit(call, "POST", "/v1/responses", dict(held_request, stream=stream), "location-test-key")
                            try:
                                assert HOLD_STARTED.wait(8); CAPTURED.get(timeout=3)
                                waiting = executor.submit(call, "POST", "/v1/responses", dict(request, stream=stream), "location-test-key")
                                wait_for(lambda: call("GET", BASE + "/credential-policies")["waiting"].get(first_index) == 1)
                                assert not waiting.done() and CAPTURED.empty(), "queued request reached upstream before admission"
                                try:
                                    call("POST", "/v1/responses", dict(request, stream=stream), "location-test-key")
                                    raise AssertionError("full queue was accepted")
                                except HTTPError as error:
                                    assert error.code == 429 and "concurrency_queue_full" in error.read().decode()
                                HOLD_RELEASE.set(); running.result(timeout=10); waiting.result(timeout=10)
                                CAPTURED.get(timeout=3)
                            finally:
                                HOLD_RELEASE.set()
                        wait_for(lambda: not call("GET", BASE + "/credential-policies")["waiting"])
                        wait_for(lambda: not call("GET", BASE + "/credential-policies")["active"])
                    HOLD_STARTED.clear(); HOLD_RELEASE.clear()
                    with ThreadPoolExecutor() as executor:
                        running = executor.submit(call, "POST", "/v1/responses", dict(held_request, stream=False), "location-test-key")
                        try:
                            assert HOLD_STARTED.wait(8); CAPTURED.get(timeout=3)
                            # A disconnected client must leave its queue position without being sent upstream.
                            sock = socket.create_connection(("127.0.0.1", port))
                            body = json.dumps(dict(request, stream=False)).encode()
                            sock.sendall(("POST /v1/responses HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: Bearer location-test-key\r\nContent-Type: application/json\r\nContent-Length: " + str(len(body)) + "\r\n\r\n").encode() + body)
                            wait_for(lambda: call("GET", BASE + "/credential-policies")["waiting"].get(first_index) == 1)
                            canceled_at = time.monotonic()
                            sock.shutdown(socket.SHUT_RDWR); sock.close()
                            wait_for(lambda: not call("GET", BASE + "/credential-policies")["waiting"])
                            assert time.monotonic() - canceled_at < 1.25, "cancellation waited for the 2-second queue timeout"
                            assert CAPTURED.empty(), "disconnected waiter reached upstream"
                            started = time.monotonic()
                            try:
                                call("POST", "/v1/responses", dict(request, stream=False), "location-test-key")
                                raise AssertionError("queue timeout was accepted")
                            except HTTPError as error:
                                assert error.code == 429 and "concurrency_queue_timeout" in error.read().decode()
                                assert 1.5 <= time.monotonic() - started < 5
                            assert CAPTURED.empty(), "timed out request reached upstream"
                            wait_for(lambda: not call("GET", BASE + "/credential-policies")["waiting"])
                        finally:
                            HOLD_RELEASE.set()
                        running.result(timeout=10)
                    # Retired global defaults cannot limit zero, missing, or old inherited accounts.
                    for mode in ("zero", "missing", "old-inherit"):
                        unlimited = copy.deepcopy(policies)
                        unlimited[first_index]["max_concurrency"] = 0
                        if mode == "missing":
                            unlimited.pop(first_index)
                        elif mode == "old-inherit":
                            unlimited[first_index].update(max_concurrency=7, concurrency_mode="inherit")
                        expected = copy.deepcopy(unlimited)
                        if mode == "old-inherit":
                            expected[first_index].pop("concurrency_mode")
                            expected[first_index]["max_concurrency"] = 0
                        call("PATCH", BASE + "/config", {"credential_policies": unlimited, "concurrency": dict(concurrency, default_max_concurrency=1)})
                        wait_for(lambda: call("GET", BASE + "/credential-policies")["policies"] == expected)
                        assert call("GET", BASE + "/concurrency")["concurrency"] == concurrency
                        HOLD_STARTED.clear(); HOLD_RELEASE.clear()
                        with ThreadPoolExecutor() as executor:
                            running = executor.submit(call, "POST", "/v1/responses", dict(held_request, stream=False), "location-test-key")
                            try:
                                assert HOLD_STARTED.wait(8); CAPTURED.get(timeout=3)
                                for stream in (False, True):
                                    send(stream)
                                    assert not call("GET", BASE + "/credential-policies")["waiting"], "unlimited request entered queue"
                            finally:
                                HOLD_RELEASE.set()
                            running.result(timeout=10)
                        wait_for(lambda: not call("GET", BASE + "/credential-policies")["active"])
                    call("PATCH", BASE + "/config", {"credential_policies": policies, "concurrency": {"account_queue_capacity": 0, "queue_timeout_seconds": 30}})
                    wait_for(lambda: call("GET", BASE + "/credential-policies")["policies"] == policies)
                    prior_failures = call("GET", BASE + "/errors")["entries"]
                    FAIL_NEXT.set()
                    try:
                        call("POST", "/v1/responses", dict(request, stream=False), key="location-test-key")
                        raise AssertionError("dummy failure unexpectedly succeeded")
                    except HTTPError as error:
                        assert error.code == 400
                    CAPTURED.get(timeout=3)
                    wait_for(lambda: len(call("GET", BASE + "/errors")["entries"]) == len(prior_failures) + 1)
                    # A real process restart must restore policy configuration.
                    host.terminate()
                    host.wait(timeout=8)
                    host = subprocess.Popen([str(cpa), "-config", str(root / "config.yaml"), "-local-model"], cwd=root, stdout=log, stderr=log)
                    wait_for(lambda: call("GET", BASE + "/credential-policies")["policies"] == policies)
                    assert send(False)["tools"][0]["user_location"]["timezone"] == "Asia/Tokyo"
                    assert (root / "billing.db").read_bytes() == original_history
                    failures = call("GET", BASE + "/errors")["entries"]
                    assert len(failures) == len(prior_failures) + 1
                    historical = [f for f in failures if f["body"] == "historical upstream failure"]
                    assert len(historical) == 1 and historical[0]["latency_ms"] == 123 and historical[0]["ttft_ms"] == 40
                    assert failures[0]["status_code"] == 400 and "diagnostic-test" in failures[0]["body"]
                    assert "location-test-key" not in json.dumps(failures) and "dummy-upstream-secret" not in json.dumps(failures)
                    logs = call("GET", BASE + "/plugin-logs?limit=200")["entries"]
                    assert len([row for row in logs if row["message"] == "historical diagnostic log"]) == 1
                    print("PASS: real CPA; two actual proxy paths; global/account on/off and inheritance; preserve opt-out; stream/nonstream 429 + Retry-After; independent credentials; lifecycle release; retired defaults ignored; zero/missing/inherited accounts unlimited without queuing; bounded queues; queue timeout and client cancellation; restart persistence; removed-feature API rejection; unpriced request passthrough; read-only diagnostic history import without duplication or source changes")

                finally:
                    host.terminate()
                    try:
                        host.wait(timeout=8)
                    except subprocess.TimeoutExpired:
                        host.kill()
                        host.wait()
                    host = None
    finally:
        if host is not None:
            host.terminate()
            host.wait(timeout=8)
        upstream.shutdown()
        upstream.server_close()
        for proxy in proxies:
            proxy.shutdown()
            proxy.server_close()


if __name__ == "__main__":
    main()
