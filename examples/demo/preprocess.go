package main

// preprocessCfg returns the *config to use for preprocessing calls
// (compaction, rewriter). When cfg.preprocessModel is empty, the original
// cfg is returned — no allocation, no behavior change. When set, a shallow
// clone with `model` overridden is returned; all other fields (api key,
// base URL, sampling params, max tokens) are preserved as-is.
//
// Shallow clone assumes config has no sync primitives or owning pointers —
// today all fields are value types (strings, ints, floats, bools). Adding
// a pointer or mutex to config would invalidate this helper.
//
// Callers pass the returned *config into chatStream. Main-agent paths must
// NOT go through this helper — they use cfg directly.
func preprocessCfg(cfg *config) *config {
	if cfg.preprocessModel == "" {
		return cfg
	}
	clone := *cfg
	clone.model = cfg.preprocessModel
	return &clone
}
