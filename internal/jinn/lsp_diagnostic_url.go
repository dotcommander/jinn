package jinn

import "net/url"

func diagnosticTargetURL(description any) string {
	object, ok := description.(map[string]any)
	if !ok {
		return ""
	}
	target, ok := object["href"].(string)
	if !ok {
		return ""
	}
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return parsed.String()
}
