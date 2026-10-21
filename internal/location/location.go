// Package location validates explicit location overrides and aligns Codex time contexts.
package location

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"
)

// Location is opt-in. A nil compiled location means that the request is untouched.
type Location struct {
	Country  string `json:"country" yaml:"country"`
	Region   string `json:"region" yaml:"region"`
	City     string `json:"city" yaml:"city"`
	Timezone string `json:"timezone" yaml:"timezone"`
}

type Compiled struct {
	Location
	zone *time.Location
}

type Settings struct {
	Enabled  bool `json:"enabled" yaml:"enabled"`
	Location `yaml:",inline"`
}

func (s Settings) Compile() (*Compiled, error) {
	if !s.Enabled {
		return nil, nil
	}
	return Compile(s.Location)
}

func (c *Compiled) Zone() *time.Location { return c.zone }

func Compile(value Location) (*Compiled, error) {
	value.Country = strings.ToUpper(strings.TrimSpace(value.Country))
	value.Region = strings.TrimSpace(value.Region)
	value.City = strings.TrimSpace(value.City)
	value.Timezone = strings.TrimSpace(value.Timezone)
	if len(value.Country) != 2 || value.Country[0] < 'A' || value.Country[0] > 'Z' || value.Country[1] < 'A' || value.Country[1] > 'Z' {
		return nil, fmt.Errorf("country must be a two-letter country code")
	}
	for _, field := range []struct{ name, value string }{{"region", value.Region}, {"city", value.City}, {"timezone", value.Timezone}} {
		if field.value == "" || len([]rune(field.value)) > 128 || strings.ContainsFunc(field.value, unicode.IsControl) {
			return nil, fmt.Errorf("%s must contain 1 to 128 characters without control characters", field.name)
		}
	}
	if value.Timezone == "Local" {
		return nil, fmt.Errorf("timezone must be an explicit IANA timezone, not Local")
	}
	zone, err := time.LoadLocation(value.Timezone)
	if err != nil {
		return nil, fmt.Errorf("timezone must be a valid IANA timezone")
	}
	return &Compiled{Location: value, zone: zone}, nil
}

func (c *Compiled) Date(now time.Time) string {
	return now.In(c.zone).Format("2006-01-02")
}
