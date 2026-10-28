package main

import (
	"mime"
	"net/http"
	"net/url"
	"strings"
)

func inspectorRunAllowed(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "inspector requires application/json", http.StatusUnsupportedMediaType)
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, "inspector requires same-origin requests", http.StatusForbidden)
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	origin, err := url.Parse(origins[0])
	target := &url.URL{Scheme: scheme, Host: r.Host}
	if len(origins) != 1 || err != nil || origin.Opaque != "" || origin.User != nil ||
		origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" ||
		origin.Scheme != target.Scheme || !strings.EqualFold(origin.Hostname(), target.Hostname()) ||
		inspectorOriginPort(origin) != inspectorOriginPort(target) {
		http.Error(w, "inspector requires same-origin requests", http.StatusForbidden)
		return false
	}
	return true
}

func inspectorOriginPort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if value.Scheme == "https" {
		return "443"
	}
	return "80"
}
