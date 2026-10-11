package plugin

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthQuotaTransport(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://invalid-proxy.example:8888")
	for _, route := range []string{"host", "http", "https", "direct", "none"} {
		t.Run(route, func(t *testing.T) {
			calls := 0
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer dummy-token" || r.Host != "quota.example.invalid" {
					t.Error("incorrect quota request")
				}
				if route == "http" || route == "https" {
					if !r.URL.IsAbs() || r.Header.Get("Proxy-Authorization") != "Basic ZHVtbXktdXNlcjpkdW1teS1wYXNz" {
						t.Error("request bypassed the authenticated proxy")
					}
				}
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			if route == "https" {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			proxy := route
			if route == "host" {
				proxy = ""
			} else if route == "http" || route == "https" {
				proxy = strings.Replace(server.URL, "://", "://dummy-user:dummy-pass@", 1)
			}
			client, err := newAuthQuotaClient(func(method string, payload any) (json.RawMessage, error) {
				request := payload.(hostHTTPRequest)
				if route != "host" || method != hostHTTPDo || request.HostCallbackID != "dummy-callback" || request.Headers.Get("Authorization") != "Bearer dummy-token" {
					t.Error("incorrect host transport or callback context")
				}
				calls++
				return mustJSONRaw(t, hostHTTPResponse{StatusCode: 200, Body: []byte(`{"ok":true}`)}), nil
			}, "dummy-callback", map[string]any{"access_token": "dummy-token", "proxy_url": proxy})
			if err != nil {
				t.Fatal(err)
			}
			defer client.close()
			if route == "https" {
				roots := x509.NewCertPool()
				roots.AddCert(server.Certificate())
				client.client.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
				client.client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
			} else if route == "direct" || route == "none" {
				client.client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					if address != "quota.example.invalid:80" {
						t.Errorf("direct request dialed a proxy: %s", address)
					}
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				}
			}
			response, err := client.upstream(http.MethodGet, "http://quota.example.invalid/usage", nil, nil)
			if err != nil || response["ok"] != true || calls != 1 {
				t.Fatalf("response = %v, calls = %d, error = %v", response, calls, err)
			}
		})
	}
}

func TestAuthQuotaProxyErrors(t *testing.T) {
	for _, failure := range []string{"upstream", "transport"} {
		t.Run(failure, func(t *testing.T) {
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"message":"dummy-token dummy-user dummy/pass dummy%2Fpass"}`)
			}))
			defer proxy.Close()
			if failure == "transport" {
				proxy.Close()
			}
			client, err := newAuthQuotaClient(func(string, any) (json.RawMessage, error) {
				t.Error("failed proxy request fell back to the host")
				return nil, errors.New("unexpected host call")
			}, "", map[string]any{"access_token": "dummy-token", "proxy_url": strings.Replace(proxy.URL, "://", "://dummy-user:dummy%2Fpass@", 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer client.close()
			_, err = client.upstream(http.MethodGet, "http://upstream.invalid/quota", nil, nil)
			if err == nil {
				t.Fatal("expected proxy error")
			}
			for _, secret := range []string{"dummy-token", "dummy-user", "dummy/pass", "dummy%2Fpass"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %s", secret)
				}
			}
		})
	}
}
