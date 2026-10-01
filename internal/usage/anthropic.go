package usage

func Builtin() Catalog {
	c := Catalog{models: map[string]Model{}, configured: map[string]bool{}}
	for _, m := range anthropicModels {
		c.models[m.Name] = m
	}
	return c
}

func anthropic(name string, input, output, cacheRead float64) Model {
	return Model{Name: name, Input: input, Output: output, CacheRead: cacheRead, CacheWrite: input * 1.25, CacheWrite1h: input * 2}
}

var anthropicModels = []Model{
	anthropic("claude-fable-5-1", 10, 50, 0.25),
	anthropic("claude-mythos-5-1", 10, 50, 0.25),
	anthropic("claude-fable-5", 10, 50, 1),
	anthropic("claude-mythos-5", 10, 50, 1),
	anthropic("claude-opus-5-5", 4, 20, 0.20),
	anthropic("claude-opus-5", 5, 25, 0.50),
	anthropic("claude-opus-4-8", 5, 25, 0.50),
	anthropic("claude-opus-4-7", 5, 25, 0.50),
	anthropic("claude-opus-4-6", 5, 25, 0.50),
	anthropic("claude-opus-4-5", 5, 25, 0.50),
	anthropic("claude-opus-4-1", 15, 75, 1.50),
	anthropic("claude-opus-4-0", 15, 75, 1.50),
	anthropic("claude-opus-4", 15, 75, 1.50),
	anthropic("claude-sonnet-5-5", 2, 10, 0.20),
	anthropic("claude-sonnet-5", 2, 10, 0.20),
	anthropic("claude-sonnet-4-6", 3, 15, 0.30),
	anthropic("claude-sonnet-4-5", 3, 15, 0.30),
	anthropic("claude-sonnet-4-0", 3, 15, 0.30),
	anthropic("claude-sonnet-4", 3, 15, 0.30),
	anthropic("claude-haiku-4-5", 1, 5, 0.10),
	anthropic("claude-haiku-3-5", 0.80, 4, 0.08),
}
