package plugin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func locationFixture(ip, zone string) []byte {
	raw, _ := json.Marshal(map[string]any{"success": true, "ip": ip, "country_code": "JP", "region": "Tokyo", "city": "Tokyo", "timezone": map[string]string{"id": zone}})
	return raw
}
func TestProbeLocationFixedIPAndConflict(t *testing.T) {
	for _, test := range []struct {
		name, secondZone                                        string
		badIP, badTimezone, lookupFailure, v6Failure, wantError bool
	}{
		{name: "dual", secondZone: "Asia/Tokyo"},
		{name: "ipv4-only", v6Failure: true},
		{name: "different-timezones", secondZone: "America/New_York", wantError: true},
		{name: "rotating-ip-mismatch", badIP: true, wantError: true},
		{name: "invalid-zone", badTimezone: true, wantError: true},
		{name: "lookup-error", lookupFailure: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			get := func(endpoint string) ([]byte, error) {
				if strings.HasPrefix(endpoint, "https://api.ipify.org") {
					return []byte(`{"ip":"8.8.8.8"}`), nil
				}
				if strings.HasPrefix(endpoint, "https://api6.ipify.org") {
					if test.v6Failure {
						return nil, fmt.Errorf("IPv6 unavailable")
					}
					return []byte(`{"ip":"2001:4860:4860::8888"}`), nil
				}
				ip := "8.8.8.8"
				zone := "Asia/Tokyo"
				if strings.HasPrefix(endpoint, "https://ipwho.is/2001:4860:4860::8888?") {
					ip = "2001:4860:4860::8888"
					zone = test.secondZone
				} else if !strings.HasPrefix(endpoint, "https://ipwho.is/8.8.8.8?") {
					t.Fatalf("queried wrong IP: %s", endpoint)
				}
				if test.badIP {
					ip = "1.1.1.1"
				}
				if test.badTimezone {
					zone = "Bad/Zone"
				}
				if test.lookupFailure {
					return nil, fmt.Errorf("rate limited")
				}
				return locationFixture(ip, zone), nil
			}
			loc, ips, err := probeLocation(get)
			if (err != nil) != test.wantError {
				t.Fatalf("location=%+v ips=%v err=%v", loc, ips, err)
			}
			if err == nil && (loc.Timezone != "Asia/Tokyo" || len(ips) == 0) {
				t.Fatal("missing location")
			}
		})
	}
}
func TestLocationGetterUsesOnlySelectedProxyAndSafeHeaders(t *testing.T) {
	var calls int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Host != "geo.invalid" {
			t.Errorf("unexpected host %s", r.URL.Host)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("upstream credential sent to geo service")
		}
		if r.Header.Get("Proxy-Authorization") == "" {
			t.Error("configured proxy authentication missing")
		}
		fmt.Fprint(w, `{"ip":"8.8.8.8"}`)
	}))
	defer proxy.Close()
	a := newTestApp(t)
	defer a.Shutdown()
	get, closeClient, err := a.locationGetter("", strings.Replace(proxy.URL, "http://", "http://dummy:password@", 1))
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	if _, err := get("http://geo.invalid/"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("proxy requests=%d", calls)
	}
	a.SetHostCaller(func(method string, payload any) (json.RawMessage, error) {
		if method != hostHTTPDo {
			t.Fatalf("unexpected method %s", method)
		}
		req := payload.(hostHTTPRequest)
		if req.HostCallbackID != "callback" || len(req.Headers) != 1 || req.Headers.Get("Accept") != "application/json" || len(req.Body) != 0 {
			t.Fatalf("unexpected geo request: %+v", req)
		}
		return json.Marshal(hostHTTPResponse{StatusCode: 200, Body: []byte(`{"ip":"8.8.8.8"}`)})
	})
	get, closeClient, err = a.locationGetter("callback", "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	if _, err = get("https://api.ipify.org?format=json"); err != nil {
		t.Fatal(err)
	}
}
func TestDetectionProxyDoesNotFallbackForMissingAuth(t *testing.T) {
	a := newTestApp(t)
	defer a.Shutdown()
	a.SetHostCaller(func(method string, payload any) (json.RawMessage, error) {
		return nil, fmt.Errorf("secret must not leak")
	})
	_, err := a.detectionProxy(ManagementRequest{}, locationDetectRequest{AuthIndex: "missing"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("missing auth did not fail safely")
	}
}
