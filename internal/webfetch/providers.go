package webfetch

import (
	"fmt"
	"strings"
)

// providerDescriptor is the metadata that drives provider construction so a
// new search backend only adds one table entry (role-split registry: search
// providers expose search only, fetch providers fetch only, full providers
// both — the orchestrator dispatches on that shape).
type providerDescriptor struct {
	name        string
	endpointEnv string // env var for an explicit (self-hosted) endpoint
	keyEnv      string // env var for the API key
	defaultEP   string
	// requiresKey marks hosted providers that are unusable without an API
	// key; self-hosted providers (SearXNG) stay usable keyless.
	requiresKey bool
	build       func(client *Client, endpoint, apiKey string) searchProvider
}

var searchProviderRegistry = map[string]providerDescriptor{
	providerBrave: {
		name:        providerBrave,
		endpointEnv: "BRAVE_SEARCH_ENDPOINT",
		keyEnv:      "BRAVE_API_KEY",
		defaultEP:   DefaultSearchEndpoint,
		requiresKey: true,
		build: func(client *Client, endpoint, apiKey string) searchProvider {
			return braveProvider{client: client, endpoint: endpoint, apiKey: apiKey}
		},
	},
	providerExa: {
		name:        providerExa,
		endpointEnv: "EXA_SEARCH_ENDPOINT",
		keyEnv:      "EXA_API_KEY",
		defaultEP:   DefaultExaEndpoint,
		requiresKey: true,
		build: func(client *Client, endpoint, apiKey string) searchProvider {
			return exaProvider{client: client, endpoint: endpoint, apiKey: apiKey}
		},
	},
	providerSearXNG: {
		name:        providerSearXNG,
		endpointEnv: "SEARXNG_URL",
		keyEnv:      "SEARXNG_API_KEY",
		defaultEP:   DefaultSearXNGEndpoint,
		build: func(client *Client, endpoint, apiKey string) searchProvider {
			return searxngProvider{client: client, endpoint: endpoint, apiKey: apiKey}
		},
	},
}

// resolveSearchProvider builds one provider using donor-style config
// precedence: key = explicit config value (the command layer layers
// environment variables above config, keeping the engine environment-pure);
// endpoint = explicit config value > descriptor default (the default is a
// hint, not an explicit choice). The second return reports whether the
// provider is usable: hosted providers need a resolved key; a provider
// whose key is empty is reported unconfigured so callers fail precisely.
func resolveSearchProvider(name string, client *Client, cfg Config) (searchProvider, bool, error) {
	descriptor, ok := searchProviderRegistry[name]
	if !ok {
		return nil, false, unsupportedSearchProviderError(name)
	}
	apiKey := strings.TrimSpace(apiKeyFor(cfg, name))
	endpoint := strings.TrimSpace(endpointFor(cfg, name))
	if endpoint == "" {
		endpoint = descriptor.defaultEP
	}
	configured := apiKey != "" || !descriptor.requiresKey
	return descriptor.build(client, endpoint, apiKey), configured, nil
}

func apiKeyFor(cfg Config, name string) string {
	switch name {
	case providerBrave:
		return cfg.BraveAPIKey
	case providerExa:
		return cfg.ExaAPIKey
	case providerSearXNG:
		return cfg.SearXNGAPIKey
	default:
		return ""
	}
}

func endpointFor(cfg Config, name string) string {
	switch name {
	case providerBrave:
		return cfg.SearchEndpoint
	case providerExa:
		return cfg.ExaSearchEndpoint
	case providerSearXNG:
		return cfg.SearXNGSearchEndpoint
	default:
		return ""
	}
}

// unsupportedSearchProviderError preserves the historical error shape.
func unsupportedName(name string) error {
	return fmt.Errorf("unsupported search provider %q", name)
}
