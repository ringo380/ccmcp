// Package mcpprobe resolves raw MCP server config entries into probeable
// targets and provides a stable cache key for each one.
package mcpprobe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Target is a probeable stdio MCP server resolved from a raw config entry.
type Target struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// TargetFromConfig resolves a raw ~/.claude.json MCP entry into a Target.
// ok=false means the entry cannot be probed locally; reason says why, in a form
// suitable for display as a TierUnknown reason.
func TargetFromConfig(name string, cfg any) (t Target, ok bool, reason string) {
	m, isMap := cfg.(map[string]any)
	if !isMap {
		return Target{}, false, "malformed entry - not a config object"
	}

	rawCmd, hasCmd := m["command"]
	if !hasCmd {
		if _, hasURL := m["url"]; hasURL {
			return Target{}, false, "remote server - cannot probe locally"
		}
		if _, hasType := m["type"]; hasType {
			return Target{}, false, "remote server - cannot probe locally"
		}
		return Target{}, false, "no command - cannot probe locally"
	}

	cmd, isStr := rawCmd.(string)
	if !isStr {
		return Target{}, false, "malformed entry - command is not a string"
	}

	var args []string
	if rawArgs, hasArgs := m["args"]; hasArgs {
		list, isList := rawArgs.([]any)
		if !isList {
			return Target{}, false, "malformed entry - args is not a list"
		}
		for _, a := range list {
			s, isStr := a.(string)
			if !isStr {
				return Target{}, false, "malformed entry - args contains a non-string element"
			}
			args = append(args, s)
		}
	}

	var env map[string]string
	if rawEnv, hasEnv := m["env"]; hasEnv {
		envMap, isMap := rawEnv.(map[string]any)
		if !isMap {
			return Target{}, false, "malformed entry - env is not an object"
		}
		env = make(map[string]string, len(envMap))
		for k, v := range envMap {
			s, isStr := v.(string)
			if !isStr {
				return Target{}, false, "malformed entry - env value is not a string"
			}
			env[k] = s
		}
	}

	return Target{
		Name:    name,
		Command: cmd,
		Args:    args,
		Env:     env,
	}, true, ""
}

// keyPayload is the canonical shape hashed into the cache key. Fields are
// ordered explicitly and env keys are sorted before encoding so the resulting
// JSON - and therefore the hash - is stable across runs regardless of Go's
// randomized map iteration order.
type keyPayload struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     []keyPayloadEnvKV `json:"env"`
}

type keyPayloadEnvKV struct {
	K string `json:"k"`
	V string `json:"v"`
}

// Key is a stable hash of command+args+env. Editing any of them invalidates the
// cached probe for this server. Name is display-only and does not affect it.
func (t Target) Key() string {
	envKeys := make([]string, 0, len(t.Env))
	for k := range t.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)

	envKV := make([]keyPayloadEnvKV, 0, len(envKeys))
	for _, k := range envKeys {
		envKV = append(envKV, keyPayloadEnvKV{K: k, V: t.Env[k]})
	}

	payload := keyPayload{
		Command: t.Command,
		Args:    t.Args,
		Env:     envKV,
	}

	// json.Marshal never errors on this concrete struct shape.
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
