package rules

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ---- raw YAML types (unexported) ----------------------------------------

type yamlMACRule struct {
	Class    string `yaml:"class"`
	Reason   string `yaml:"reason"`
	Cooldown string `yaml:"cooldown"`
}

type yamlNetworkCodeEntry struct {
	Networks []string `yaml:"networks"`
	Code     string   `yaml:"code"`
	Class    string   `yaml:"class"`
	Reason   string   `yaml:"reason"`
	Cooldown string   `yaml:"cooldown"`
}

type yamlDefaults struct {
	Window      string `yaml:"window"`
	MaxAttempts int    `yaml:"max_attempts"`
}

type yamlFile struct {
	Version          string                 `yaml:"rules_version"`
	Defaults         yamlDefaults           `yaml:"defaults"`
	MACRules         map[string]yamlMACRule `yaml:"mac_rules"`
	NetworkCodeRules []yamlNetworkCodeEntry `yaml:"network_code_rules"`
}

// ---- public loader -------------------------------------------------------

// Load reads and parses the rules YAML at path, returning a query-ready Table.
func Load(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rules file: %w", err)
	}

	var raw yamlFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse rules yaml: %w", err)
	}

	window, err := time.ParseDuration(raw.Defaults.Window)
	if err != nil {
		return nil, fmt.Errorf("defaults.window: %w", err)
	}
	if raw.Defaults.MaxAttempts <= 0 {
		return nil, fmt.Errorf("defaults.max_attempts must be > 0")
	}

	macRules, err := buildMACRules(raw.MACRules)
	if err != nil {
		return nil, err
	}

	networkCodeIndex, err := buildNetworkCodeIndex(raw.NetworkCodeRules)
	if err != nil {
		return nil, err
	}

	return &Table{
		Version: raw.Version,
		Defaults: Defaults{
			Window:      window,
			MaxAttempts: raw.Defaults.MaxAttempts,
		},
		MACRules:         macRules,
		NetworkCodeIndex: networkCodeIndex,
	}, nil
}

// ---- builders -----------------------------------------------------------

func buildMACRules(raw map[string]yamlMACRule) (map[string]MACRule, error) {
	out := make(map[string]MACRule, len(raw))
	for mac, r := range raw {
		cooldown, err := parseCooldown(r.Cooldown)
		if err != nil {
			return nil, fmt.Errorf("mac_rules[%q].cooldown: %w", mac, err)
		}
		out[mac] = MACRule{
			Class:    RetryClass(r.Class),
			Reason:   r.Reason,
			Cooldown: cooldown,
		}
	}
	return out, nil
}

func buildNetworkCodeIndex(entries []yamlNetworkCodeEntry) (map[string]NetworkCodeRule, error) {
	index := make(map[string]NetworkCodeRule, len(entries))
	for _, e := range entries {
		cooldown, err := parseCooldown(e.Cooldown)
		if err != nil {
			return nil, fmt.Errorf("network_code_rules[networks=%v code=%s].cooldown: %w", e.Networks, e.Code, err)
		}
		rule := NetworkCodeRule{
			Class:    RetryClass(e.Class),
			Reason:   e.Reason,
			Cooldown: cooldown,
		}
		for _, network := range e.Networks {
			index[network+":"+e.Code] = rule
		}
	}
	return index, nil
}

func parseCooldown(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
