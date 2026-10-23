package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

type Result struct {
	Status       string   `json:"status"`
	Action       string   `json:"action"`
	Reason       string   `json:"reason,omitempty"`
	Rules        []string `json:"rules,omitempty"`
	RuleNames    []string `json:"rule_names,omitempty"`
	Categories   []string `json:"categories,omitempty"`
	MediaIgnored bool     `json:"media_ignored,omitempty"`
	// Text exists only during the host call; management responses never expose it.
	Text string `json:"-"`
}

type Engine struct{ active atomic.Int32 }

func Failure(p Policy, reason string) Result {
	r := Result{Status: "error", Action: "allow", Reason: reason}
	if p.Mode == "pre_block" && p.FailureAction == "block" {
		r.Action = "block"
	}
	return r
}

func (e *Engine) Check(ctx context.Context, compiled *Compiled, api API, model, format string, body []byte) Result {
	if !compiled.Applies(model) {
		return Result{Status: "skipped", Action: "allow", Reason: "policy_not_applicable"}
	}
	p := compiled.Policy
	input := Extract(format, body, p.Scope)
	if input.Reason == "no_user_text" {
		return Result{Status: "skipped", Action: "allow", Reason: input.Reason}
	}
	if input.Reason != "" {
		return Failure(p, input.Reason)
	}
	result := Result{Status: "passed", Action: "allow", MediaIgnored: input.Media, Text: input.Text}
	if p.Strategy != "api" {
		for i, rule := range compiled.patterns {
			if rule.MatchString(input.Text) {
				result.Rules = append(result.Rules, p.Rules[i].ID)
				result.RuleNames = append(result.RuleNames, p.Rules[i].Name)
			}
		}
		if len(result.Rules) > 0 {
			result.Status = "flagged"
		}
	}
	// Local hits do not leave the server and do not consume an audit API call.
	if result.Status != "flagged" && p.Strategy != "keywords" {
		flagged, categories, reason := e.audit(ctx, api, input.Text)
		if reason != "" {
			result = Failure(p, reason)
			result.Text, result.MediaIgnored = input.Text, input.Media
		} else {
			result.Categories = categories
			if flagged {
				result.Status = "flagged"
			}
		}
	}
	if result.Status == "flagged" {
		if p.Mode == "pre_block" {
			result.Action = "block"
		} else {
			result.Action = "observe"
		}
	}
	return result
}

// audit has no retry worker, cache, or idle connection. All network activity is
// bounded by the call context and the configured timeout and concurrency cap.
func (e *Engine) audit(parent context.Context, api API, text string) (bool, []string, string) {
	if api.URL == "" || api.Model == "" {
		return false, nil, "api_not_configured"
	}
	if e.active.Add(1) > int32(api.MaxInFlight) {
		e.active.Add(-1)
		return false, nil, "api_busy"
	}
	defer e.active.Add(-1)
	key := ""
	if api.KeyEnv != "" {
		key = strings.TrimSpace(os.Getenv(api.KeyEnv))
		if key == "" {
			return false, nil, "api_key_missing"
		}
		if strings.ContainsAny(key, "\r\n") || len(key) > 8192 {
			return false, nil, "api_key_invalid"
		}
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(api.TimeoutMS)*time.Millisecond)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"model": api.Model, "input": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api.URL, bytes.NewReader(body))
	if err != nil {
		return false, nil, "api_request_invalid"
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil, "api_timeout_or_canceled"
		}
		return false, nil, "api_unreachable"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, nil, "api_http_error"
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return false, nil, "api_response_invalid"
	}
	var output struct {
		Results []struct {
			Flagged    *bool              `json:"flagged"`
			Categories map[string]bool    `json:"categories"`
			Scores     map[string]float64 `json:"category_scores"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &output) != nil || len(output.Results) != 1 || output.Results[0].Flagged == nil {
		return false, nil, "api_response_invalid"
	}
	r := output.Results[0]
	if len(r.Categories) > 100 || len(r.Scores) > 100 {
		return false, nil, "api_response_invalid"
	}
	if r.Categories != nil {
		anyFlagged := false
		for _, flagged := range r.Categories {
			anyFlagged = anyFlagged || flagged
		}
		if anyFlagged != *r.Flagged {
			return false, nil, "api_response_invalid"
		}
	}
	hits := []string{}
	for category, flagged := range r.Categories {
		if !categoryName.MatchString(category) {
			return false, nil, "api_response_invalid"
		}
		if _, overridden := api.Thresholds[category]; !overridden && flagged {
			hits = append(hits, category)
		}
	}
	for category, threshold := range api.Thresholds {
		score, ok := r.Scores[category]
		if !ok || score < 0 || score > 1 {
			return false, nil, "api_category_missing"
		}
		if score >= threshold {
			hits = append(hits, category)
		}
	}
	slices.Sort(hits)
	if len(api.Thresholds) == 0 {
		return *r.Flagged, hits, ""
	}
	if r.Categories == nil {
		return false, nil, "api_categories_missing"
	}
	return len(hits) > 0, hits, ""
}
