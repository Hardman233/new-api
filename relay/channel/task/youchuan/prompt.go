package youchuan

import (
	"fmt"
	"strconv"
	"strings"
)

var supportedPromptVersions = map[string]struct{}{
	"v6": {}, "v6.1": {}, "niji 6": {}, "niji 7": {}, "v7": {}, "v8.1": {}, "v8.2": {},
}

// ValidatePromptParameters validates the known official prompt switches while
// deliberately leaving unknown switches untouched for forward compatibility.
func ValidatePromptParameters(text string) error {
	fields := strings.Fields(text)
	version := ""
	for i := 0; i < len(fields); i++ {
		name, value, hasInline := promptFlag(fields[i])
		if name != "--version" && name != "--v" && name != "--niji" {
			continue
		}
		if !hasInline {
			if i+1 >= len(fields) || strings.HasPrefix(fields[i+1], "--") {
				return fmt.Errorf("%s requires a value", name)
			}
			value = fields[i+1]
			i++
		}
		if value == "niji" && i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
			value += " " + fields[i+1]
			i++
		}
		if name == "--niji" {
			value = "niji " + strings.TrimSpace(value)
		}
		version = normalizePromptVersion(value)
		if _, ok := supportedPromptVersions[version]; !ok {
			return fmt.Errorf("unsupported prompt version %q", value)
		}
	}

	for i := 0; i < len(fields); i++ {
		name, value, hasInline := promptFlag(fields[i])
		if !strings.HasPrefix(name, "--") {
			continue
		}
		if isPromptSwitch(name) {
			if name == "--tile" && version == "niji 7" {
				return fmt.Errorf("--tile is not supported by niji 7")
			}
			if name == "--draft" && version != "" && !oneOf(version, "v7", "v8.1", "v8.2") {
				return fmt.Errorf("--draft is not supported by %s", version)
			}
			if name == "--hd" && version != "" && !oneOf(version, "v8.1", "v8.2") {
				return fmt.Errorf("--hd is not supported by %s", version)
			}
			if name == "--turbo" && version != "" && !oneOf(version, "v6", "v6.1", "niji 6", "niji 7", "v7") {
				return fmt.Errorf("--turbo is not supported by %s", version)
			}
			continue
		}
		if name == "--no" {
			if i+1 >= len(fields) || strings.HasPrefix(fields[i+1], "--") {
				return fmt.Errorf("--no requires a value")
			}
			for i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
				i++
			}
			continue
		}
		if name == "--version" || name == "--v" || name == "--niji" {
			if !hasInline {
				i++
				if i < len(fields) && fields[i] == "niji" && i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
					i++
				}
			}
			continue
		}
		if !isKnownValueFlag(name) {
			continue
		}
		if !hasInline {
			if i+1 >= len(fields) || strings.HasPrefix(fields[i+1], "--") {
				return fmt.Errorf("%s requires a value", name)
			}
			value = fields[i+1]
			i++
		}
		if err := validatePromptValue(name, value, version); err != nil {
			return err
		}
	}
	return nil
}

func promptFlag(raw string) (name, value string, hasInline bool) {
	parts := strings.SplitN(raw, "=", 2)
	name = strings.ToLower(parts[0])
	if len(parts) == 2 {
		return name, parts[1], true
	}
	return name, "", false
}

func isPromptSwitch(name string) bool {
	switch name {
	case "--raw", "--tile", "--draft", "--fast", "--turbo", "--hd":
		return true
	default:
		return false
	}
}

func isKnownValueFlag(name string) bool {
	switch name {
	case "--ar", "--aspect", "--chaos", "--c", "--cref", "--cw", "--profile", "--p", "--quality", "--q", "--seed", "--stop", "--stylize", "--s", "--sref", "--sw", "--sv", "--weird", "--w", "--exp", "--iw", "--oref", "--ow", "--bs", "--motion":
		return true
	default:
		return false
	}
}

func validatePromptValue(name, value, version string) error {
	switch name {
	case "--ar", "--aspect":
		if !validAspectRatio(value) {
			return fmt.Errorf("%s has invalid aspect ratio %q", name, value)
		}
	case "--chaos", "--c":
		return validateFloatRange(name, value, 0, 100)
	case "--cw":
		if version != "" && !oneOf(version, "v6", "v6.1", "niji 6") {
			return fmt.Errorf("--cw is not supported by %s", version)
		}
		return validateFloatRange(name, value, 0, 100)
	case "--quality", "--q":
		allowed := map[string]bool{"0.25": true, "0.5": true, "1": true, "2": true, "3": true, "4": true}
		if version == "v6" || version == "niji 6" {
			allowed = map[string]bool{"0.25": true, "0.5": true, "1": true}
		} else if version == "v6.1" {
			allowed = map[string]bool{"0.5": true, "1": true, "2": true}
		} else if version == "v7" {
			allowed = map[string]bool{"1": true, "2": true, "4": true}
		} else if version == "v8.1" {
			allowed = map[string]bool{"1": true, "4": true}
		} else if version == "v8.2" {
			allowed = map[string]bool{"1": true, "2": true, "3": true, "4": true}
		}
		if !allowed[value] {
			return fmt.Errorf("%s value %q is not supported by %s", name, value, displayVersion(version))
		}
	case "--seed":
		if _, err := strconv.ParseUint(value, 10, 32); err != nil {
			return fmt.Errorf("--seed must be between 0 and 4294967295")
		}
	case "--stop":
		if version != "" && oneOf(version, "niji 7", "v7", "v8.1", "v8.2") {
			return fmt.Errorf("--stop is not supported by %s", version)
		}
		return validateFloatRange(name, value, 10, 100)
	case "--stylize", "--s":
		return validateFloatRange(name, value, 0, 1000)
	case "--sw":
		return validateFloatRange(name, value, 0, 1000)
	case "--sv":
		return validateFloatRange(name, value, 1, 6)
	case "--weird", "--w":
		return validateFloatRange(name, value, 0, 3000)
	case "--profile", "--p":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s requires a non-empty value", name)
		}
	case "--exp":
		if version != "" && !oneOf(version, "v7", "v8.1", "v8.2") {
			return fmt.Errorf("--exp is not supported by %s", version)
		}
		return validateFloatRange(name, value, 0, 100)
	case "--iw":
		max := 3.0
		if version == "niji 7" {
			max = 2
		}
		return validateFloatRange(name, value, 0, max)
	case "--ow":
		if version != "" && version != "v7" {
			return fmt.Errorf("--ow is not supported by %s", version)
		}
		return validateFloatRange(name, value, 1, 1000)
	case "--bs":
		if value != "1" && value != "2" && value != "4" {
			return fmt.Errorf("--bs must be one of 1, 2, or 4")
		}
	case "--motion":
		if value != "low" && value != "high" {
			return fmt.Errorf("--motion must be low or high")
		}
	case "--cref", "--sref", "--oref":
		if version != "" {
			if name == "--cref" && !oneOf(version, "v6", "v6.1", "niji 6") {
				return fmt.Errorf("--cref is not supported by %s", version)
			}
			if name == "--oref" && version != "v7" {
				return fmt.Errorf("--oref is not supported by %s", version)
			}
		}
		if !strings.HasPrefix(strings.ToLower(value), "http://") && !strings.HasPrefix(strings.ToLower(value), "https://") {
			return fmt.Errorf("%s requires an HTTP image URL", name)
		}
	}
	return nil
}

func validateFloatRange(name, raw string, min, max float64) error {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < min || value > max {
		return fmt.Errorf("%s must be between %s and %s", name, formatNumber(min), formatNumber(max))
	}
	return nil
}

func validAspectRatio(raw string) bool {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ':' || r == '/' })
	if len(parts) != 2 {
		return false
	}
	left, leftErr := strconv.ParseFloat(parts[0], 64)
	right, rightErr := strconv.ParseFloat(parts[1], 64)
	return leftErr == nil && rightErr == nil && left > 0 && right > 0
}

func normalizePromptVersion(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "niji6" {
		return "niji 6"
	}
	if raw == "niji7" {
		return "niji 7"
	}
	if !strings.HasPrefix(raw, "v") && (strings.Contains(raw, ".") || raw == "6" || raw == "7") {
		raw = "v" + raw
	}
	return raw
}

func displayVersion(version string) string {
	if version == "" {
		return "the selected model"
	}
	return version
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
