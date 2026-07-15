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
	Networks    []string `yaml:"networks"`
	Code        string   `yaml:"code"`
	Class       string   `yaml:"class"`
	Reason      string   `yaml:"reason"`
	Cooldown    string   `yaml:"cooldown"`
	MaxAttempts int      `yaml:"max_attempts"` // 0 = no count limit for this code
	Window      string   `yaml:"window"`       // empty = use defaults.window
}

type yamlFile struct {
	Version          string                 `yaml:"rules_version"`
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

	macRules, err := buildMACRules(raw.MACRules)
	if err != nil {
		return nil, err
	}

	networkCodeIndex, err := buildNetworkCodeIndex(raw.NetworkCodeRules)
	if err != nil {
		return nil, err
	}

	return &Table{
		Version:          raw.Version,
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
		var window time.Duration
		if e.MaxAttempts > 0 {
			// Window only applies when there is a count limit.
			if e.Window != "" {
				window, err = time.ParseDuration(e.Window)
				if err != nil {
					return nil, fmt.Errorf("network_code_rules[networks=%v code=%s].window: %w", e.Networks, e.Code, err)
				}
			} else {
				window = 384 * time.Hour
			}
		}
		rule := NetworkCodeRule{
			Class:       RetryClass(e.Class),
			Reason:      e.Reason,
			Cooldown:    cooldown,
			MaxAttempts: e.MaxAttempts,
			Window:      window,
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
