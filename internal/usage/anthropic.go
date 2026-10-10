package usage

func Builtin() Catalog {
	c := Catalog{models: map[string]Model{}, configured: map[string]bool{}}
	for _, m := range append(anthropicModels, otherModels...) {
		c.models[m.Name] = m
	}
	return c
}

func anthropic(name, tier string, input, output, cacheRead float64) Model {
	return Model{Name: name, Tier: tier, Input: input, Output: output, CacheRead: cacheRead,
		CacheWrite: input * 1.25, CacheWrite1h: input * 2}
}

var anthropicModels = []Model{
	anthropic("claude-fable-5-1", TierFrontier, 10, 50, 0.25),
	anthropic("claude-mythos-5-1", TierFrontier, 10, 50, 0.25),
	anthropic("claude-fable-5", TierFrontier, 10, 50, 1),
	anthropic("claude-mythos-5", TierFrontier, 10, 50, 1),
	anthropic("claude-opus-5-5", TierStrong, 4, 20, 0.20),
	anthropic("claude-opus-5", TierStrong, 5, 25, 0.50),
	anthropic("claude-opus-4-8", TierStrong, 5, 25, 0.50),
	anthropic("claude-opus-4-7", TierStrong, 5, 25, 0.50),
	anthropic("claude-opus-4-6", TierStrong, 5, 25, 0.50),
	anthropic("claude-opus-4-5", TierStrong, 5, 25, 0.50),
	anthropic("claude-opus-4-1", TierBalanced, 15, 75, 1.50),
	anthropic("claude-opus-4-0", TierBalanced, 15, 75, 1.50),
	anthropic("claude-opus-4", TierBalanced, 15, 75, 1.50),
	anthropic("claude-sonnet-5-5", TierBalanced, 2, 10, 0.20),
	anthropic("claude-sonnet-5", TierBalanced, 2, 10, 0.20),
	anthropic("claude-sonnet-4-6", TierBalanced, 3, 15, 0.30),
	anthropic("claude-sonnet-4-5", TierBalanced, 3, 15, 0.30),
	anthropic("claude-sonnet-4-0", TierBalanced, 3, 15, 0.30),
	anthropic("claude-sonnet-4", TierBalanced, 3, 15, 0.30),
	anthropic("claude-haiku-4-5", TierFast, 1, 5, 0.10),
	anthropic("claude-haiku-3-5", TierFast, 0.80, 4, 0.08),
}
