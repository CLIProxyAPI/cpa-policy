package plugin

import (
	"bytes"
	"cpa-codex-controls/internal/location"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type locationDetectRequest struct {
	AuthIndex string `json:"auth_index"`
	// ConfiguredProxy is supplied only by the native-provider list, which already
	// receives this value from CPA. Empty means inherit the host's global proxy.
	ConfiguredProxy *string `json:"configured_proxy,omitempty"`
}

func (a *App) detectLocation(req ManagementRequest) ManagementResponse {
	var input locationDetectRequest
	if err := decodeStrict(req.Body, &input); err != nil {
		return errorResponse(err)
	}
	proxy, err := a.detectionProxy(req, input)
	if err != nil {
		return JSONError(400, "location_detection_failed", err.Error())
	}
	getter, closeClient, err := a.locationGetter(req.HostCallbackID, proxy)
	if err != nil {
		return JSONError(400, "location_detection_failed", err.Error())
	}
	defer closeClient()
	result, ips, err := probeLocation(getter)
	if err != nil {
		return JSONError(502, "location_detection_failed", err.Error())
	}
	return JSONResponse(200, map[string]any{"location": result, "exit_ips": ips, "source": func() string {
		if proxy == "" {
			return "host_global"
		}
		return location.IdentifyProxy(proxy).Name
	}(), "detected_at": a.store.Now()})
}
func (a *App) detectionProxy(req ManagementRequest, input locationDetectRequest) (string, error) {
	if input.ConfiguredProxy != nil {
		return strings.TrimSpace(*input.ConfiguredProxy), nil
	}
	if input.AuthIndex != "" {
		return a.authFileProxy(input.AuthIndex, req.HostCallbackID)
	}
	return "", nil
}

type locationGet func(string) ([]byte, error)

func (a *App) locationGetter(callback, proxy string) (locationGet, func(), error) {
	if proxy == "" {
		if a.hostCaller == nil {
			return nil, nil, fmt.Errorf("Host HTTP callback is unavailable")
		}
		return func(endpoint string) ([]byte, error) {
			raw, err := a.hostCaller(hostHTTPDo, hostHTTPRequest{HostCallbackID: callback, Method: "GET", URL: endpoint, Headers: http.Header{"Accept": []string{"application/json"}}})
			if err != nil {
				return nil, fmt.Errorf("Exit detection failed through the host's global proxy")
			}
			var response hostHTTPResponse
			if json.Unmarshal(raw, &response) != nil || response.StatusCode != 200 || len(response.Body) > 8192 {
				return nil, fmt.Errorf("Location service returned an invalid response or is rate limited")
			}
			return response.Body, nil
		}, func() {}, nil
	}
	transport := &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}
	if !strings.EqualFold(proxy, "direct") && !strings.EqualFold(proxy, "none") {
		u, err := url.Parse(proxy)
		if err != nil || u.Hostname() == "" {
			return nil, nil, fmt.Errorf("Invalid proxy URL")
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, nil, fmt.Errorf("Unsupported proxy scheme")
		}
		transport.Proxy = http.ProxyURL(u)
	}
	client := &http.Client{Transport: transport, Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(endpoint string) ([]byte, error) {
		req, _ := http.NewRequest("GET", endpoint, nil)
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Exit detection timed out or failed through the selected proxy")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("Location service returned an error or is rate limited")
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
		if err != nil || len(body) > 8192 {
			return nil, fmt.Errorf("Invalid location response size")
		}
		return body, nil
	}, client.CloseIdleConnections, nil
}

// Probe both families, then look up those exact IPs. A rotating proxy must not
// silently geolocate a different "current" IP during the second request.
func probeLocation(get locationGet) (location.Location, []string, error) {
	var chosen *location.Compiled
	ips := []string{}
	var probeErr error
	for _, endpoint := range []string{"https://api.ipify.org?format=json", "https://api6.ipify.org?format=json"} {
		raw, err := get(endpoint)
		if err != nil {
			probeErr = err
			continue
		}
		var exit struct {
			IP string `json:"ip"`
		}
		if json.Unmarshal(raw, &exit) != nil {
			probeErr = fmt.Errorf("Invalid exit IP response")
			continue
		}
		ip, err := netip.ParseAddr(exit.IP)
		if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			probeErr = fmt.Errorf("Exit detection did not return a public IP")
			continue
		}
		if len(ips) > 0 && ips[0] == ip.String() {
			continue
		}
		body, err := get("https://ipwho.is/" + ip.String() + "?fields=ip,success,country_code,region,city,timezone.id")
		if err != nil {
			return location.Location{}, nil, err
		}
		var result struct {
			Success  bool   `json:"success"`
			IP       string `json:"ip"`
			Country  string `json:"country_code"`
			Region   string `json:"region"`
			City     string `json:"city"`
			Timezone struct {
				ID string `json:"id"`
			} `json:"timezone"`
		}
		if json.NewDecoder(bytes.NewReader(body)).Decode(&result) != nil || !result.Success {
			return location.Location{}, nil, fmt.Errorf("Invalid location response")
		}
		reported, e := netip.ParseAddr(result.IP)
		if e != nil || reported != ip {
			return location.Location{}, nil, fmt.Errorf("Location service returned a different exit IP")
		}
		compiled, err := location.Compile(location.Location{Country: result.Country, Region: result.Region, City: result.City, Timezone: result.Timezone.ID})
		if err != nil {
			return location.Location{}, nil, fmt.Errorf("Location service did not return a complete region and IANA timezone")
		}
		if chosen != nil && chosen.Timezone != compiled.Timezone {
			return location.Location{}, nil, fmt.Errorf("IPv4 and IPv6 exits have different timezones; configure a location manually")
		}
		chosen = compiled
		ips = append(ips, ip.String())
	}
	if chosen == nil {
		if probeErr == nil {
			probeErr = fmt.Errorf("No exit IP detected")
		}
		return location.Location{}, nil, probeErr
	}
	return chosen.Location, ips, nil
}
