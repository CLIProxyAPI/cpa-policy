package sqlite

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cpa-key-billing/internal/billing"
)

func (d *DB) CredentialUsage(authIndexes []string, scope string, since time.Time) (map[string]billing.CredentialUsage, error) {
	if len(authIndexes) == 0 {
		return map[string]billing.CredentialUsage{}, nil
	}
	args := make([]any, len(authIndexes))
	for i, authIndex := range authIndexes {
		args[i] = authIndex
	}
	filter := "auth_index IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")"
	statsFilter := filter + " AND at >= ?"
	statsArgs := append(append([]any(nil), args...), nanos(since))
	if scope != "" {
		statsFilter += " AND scope = ?"
		statsArgs = append(statsArgs, scope)
	}
	result, err := d.credentialUsageStats(statsFilter, statsArgs)
	if err != nil {
		return nil, err
	}
	quota, err := d.credentialQuotaObservations(filter, args)
	if err != nil {
		return nil, err
	}
	for authIndex, observations := range quota {
		usage := result[authIndex]
		usage.Quota = observations
		result[authIndex] = usage
	}
	return result, nil
}

func (d *DB) credentialUsageStats(filter string, args []any) (map[string]billing.CredentialUsage, error) {
	result := make(map[string]billing.CredentialUsage)
	rows, err := d.db.Query(`SELECT auth_index, count(*), coalesce(sum(failed != 0), 0),
		coalesce(sum(uncached_input_tokens + cache_read_tokens + cache_write_tokens), 0),
		coalesce(sum(cache_read_tokens), 0), coalesce(sum(billed_output_tokens), 0)
		FROM request_events WHERE `+filter+` GROUP BY auth_index`, args...)
	if err != nil {
		return nil, fmt.Errorf("Read credential usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var authIndex string
		var usage billing.CredentialUsage
		if err := rows.Scan(&authIndex, &usage.Stats.Requests, &usage.Stats.Failed,
			&usage.Stats.InputTokens, &usage.Stats.CacheReadTokens, &usage.Stats.OutputTokens); err != nil {
			return nil, fmt.Errorf("Read credential usage: %w", err)
		}
		result[authIndex] = usage
	}
	return result, rows.Err()
}

func (d *DB) credentialQuotaObservations(filter string, args []any) (map[string][]billing.QuotaHeaderObservation, error) {
	// Keep each window's fields from the same report when only some windows update.
	rows, err := d.db.Query(`WITH headers AS (
		SELECT r.id, r.auth_index, r.provider, r.at, lower(h.key) AS key,
			CASE h.type WHEN 'text' THEN h.value WHEN 'array' THEN json_extract(h.value, '$[0]') END AS value
		FROM request_events r, json_each(r.response_headers_json) h
		WHERE `+filter+` AND r.response_headers_json != '{}'
	), reports AS (
		SELECT * FROM headers WHERE typeof(value) = 'text'
	), latest AS (
		SELECT id, row_number() OVER (
			PARTITION BY auth_index, provider, key ORDER BY at DESC, id DESC) AS position
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
