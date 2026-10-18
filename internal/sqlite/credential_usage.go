package sqlite

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cpa-key-billing/internal/billing"
)

func (d *DB) CredentialUsageStats(authIndexes []string, scope string, from, to time.Time) (map[string]billing.CredentialUsageStats, error) {
	filter, args := authIndexFilter(authIndexes)
	filter += " AND at >= ? AND at < ?"
	args = append(args, nanos(from), nanos(to))
	if scope != "" {
		filter += " AND scope = ?"
		args = append(args, scope)
	}
	result := make(map[string]billing.CredentialUsageStats)
	rows, err := d.db.Query(`SELECT auth_index, count(*), coalesce(sum(failed != 0), 0),
		coalesce(sum(uncached_input_tokens + cache_read_tokens + cache_write_tokens), 0),
		coalesce(sum(cache_read_tokens), 0), coalesce(sum(billed_output_tokens), 0), coalesce(sum(total_usd), 0)
		FROM request_events WHERE `+filter+` GROUP BY auth_index`, args...)
	if err != nil {
		return nil, fmt.Errorf("Read credential usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var authIndex string
		var stats billing.CredentialUsageStats
		if err := rows.Scan(&authIndex, &stats.Requests, &stats.Failed,
			&stats.InputTokens, &stats.CacheReadTokens, &stats.OutputTokens, &stats.CostUSD); err != nil {
			return nil, fmt.Errorf("Read credential usage: %w", err)
		}
		result[authIndex] = stats
	}
	return result, rows.Err()
}

func (d *DB) LatestQuotaHeaders(authIndexes []string, since time.Time) (map[string][]billing.QuotaHeaderObservation, error) {
	filter, args := authIndexFilter(authIndexes)
	filter += " AND at >= ?"
	args = append(args, nanos(since))
	// Each window is the newest report of its header, of its length and of the
	// limit the response named, so windows that share header names, such as a
	// 5-hour and a 24-hour primary window, each keep their newest report.
	rows, err := d.db.Query(`WITH events AS (
		SELECT id, auth_index, provider, at, response_headers_json,
			coalesce(json_extract(response_headers_json, '$."X-Codex-Active-Limit"[0]'), '') AS active_limit
		FROM request_events WHERE `+filter+` AND response_headers_json != '{}'
	), headers AS (
		SELECT r.id, r.auth_index, r.provider, r.at, r.active_limit, lower(h.key) AS key,
			CASE h.type WHEN 'text' THEN h.value WHEN 'array' THEN json_extract(h.value, '$[0]') END AS value
		FROM events r, json_each(r.response_headers_json) h
	), reports AS (
		SELECT * FROM headers WHERE typeof(value) = 'text'
	), latest AS (
		SELECT id, row_number() OVER (
			PARTITION BY auth_index, provider, key, active_limit, CASE WHEN key LIKE '%-window-minutes' THEN value END
			ORDER BY at DESC, id DESC) AS position
		FROM reports
	)
	SELECT auth_index, provider, at, json_group_object(key, json_array(value))
	FROM reports WHERE id IN (SELECT id FROM latest WHERE position = 1)
	GROUP BY id ORDER BY at, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("Read credential quota: %w", err)
	}
	result := make(map[string][]billing.QuotaHeaderObservation)
	defer rows.Close()
	for rows.Next() {
		var authIndex, raw string
		var at int64
		var observation billing.QuotaHeaderObservation
		if err := rows.Scan(&authIndex, &observation.Provider, &at, &raw); err != nil {
			return nil, fmt.Errorf("Read credential quota: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &observation.Headers); err != nil {
			return nil, fmt.Errorf("Decode credential quota: %w", err)
		}
		observation.At = time.Unix(0, at).UTC()
		result[authIndex] = append(result[authIndex], observation)
	}
	return result, rows.Err()
}

func authIndexFilter(authIndexes []string) (string, []any) {
	args := make([]any, len(authIndexes))
	for i, authIndex := range authIndexes {
		args[i] = authIndex
	}
	return "auth_index IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")", args
}

func (d *DB) QuotaHeaderHistory(authIndex string, from, to time.Time) ([]billing.QuotaHeaderObservation, error) {
	rows, err := d.db.Query(`SELECT provider, at, response_headers_json FROM request_events
		WHERE auth_index = ? AND at >= ? AND at < ? AND response_headers_json != '{}' ORDER BY at, id`,
		authIndex, nanos(from), nanos(to))
	if err != nil {
		return nil, fmt.Errorf("Read quota history: %w", err)
	}
	defer rows.Close()
	type report struct {
		provider, headers string
		at                int64
	}
	var reports []report
	var last report
	for rows.Next() {
		var next report
		if err := rows.Scan(&next.provider, &next.at, &next.headers); err != nil {
			return nil, fmt.Errorf("Read quota history: %w", err)
		}
		if next.headers != last.headers {
			if len(reports) > 0 && reports[len(reports)-1] != last {
				reports = append(reports, last)
			}
			reports = append(reports, next)
		}
		last = next
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Read quota history: %w", err)
	}
	if len(reports) > 0 && reports[len(reports)-1] != last {
		reports = append(reports, last)
	}
	history := make([]billing.QuotaHeaderObservation, len(reports))
	for i, report := range reports {
		var values map[string]any
		if err := json.Unmarshal([]byte(report.headers), &values); err != nil {
			return nil, fmt.Errorf("Decode quota history: %w", err)
		}
		// Headers are stored as value lists; read plain text values the same way.
		headers := make(http.Header, len(values))
		for key, value := range values {
			if list, ok := value.([]any); ok && len(list) > 0 {
				value = list[0]
			}
			if text, ok := value.(string); ok {
				headers[key] = []string{text}
			}
		}
		history[i] = billing.QuotaHeaderObservation{Provider: report.provider, At: time.Unix(0, report.at).UTC(), Headers: headers}
	}
	return history, nil
}

func (d *DB) QuotaPeriodUsage(period billing.QuotaPeriod, scope string) (billing.QuotaPeriodUsage, error) {
	filter := "auth_index = ? AND lower(provider) = ? AND at >= ? AND at < ?"
	args := []any{period.AuthIndex, strings.ToLower(period.Provider), nanos(period.Start), nanos(period.End)}
	if scope != "" {
		filter += " AND scope = ?"
		args = append(args, scope)
	}
	matchModels := func(fragments []string) string {
		terms := make([]string, len(fragments))
		for i, fragment := range fragments {
			terms[i] = "instr(lower(upstream_model), ?) > 0"
			args = append(args, strings.ToLower(fragment))
		}
		return "(" + strings.Join(terms, " OR ") + ")"
	}
	if len(period.Models.Include) > 0 {
		filter += " AND " + matchModels(period.Models.Include)
	}
	if len(period.Models.Exclude) > 0 {
		filter += " AND NOT " + matchModels(period.Models.Exclude)
	}
	var usage billing.QuotaPeriodUsage
	err := d.db.QueryRow(`SELECT
		coalesce(sum(uncached_input_tokens + cache_read_tokens + cache_write_tokens + billed_output_tokens), 0),
		coalesce(sum(total_usd), 0)
		FROM request_events WHERE `+filter, args...).Scan(&usage.Tokens, &usage.CostUSD)
	if err != nil {
		return usage, fmt.Errorf("Read quota period usage: %w", err)
	}
	return usage, nil
}
