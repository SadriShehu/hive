package usage

// otherModels are the models the other tools run, rated by tier so that
// hive can pick between them. They carry no prices: a tool that reports
// none shows one only with a [[model]] in config.toml. A name is spelled
// with dashes where the tool writes dots (gpt-5.5 is gpt-5-5), which is how
// Match finds it.
var otherModels = []Model{
	rated("gpt-6-pro", TierFrontier),
	rated("gpt-6", TierStrong),
	rated("gpt-6-astra", TierStrong),
	rated("gpt-6-sol", TierStrong),
	rated("gpt-6-luna", TierStrong),
	rated("gpt-6-1-sol", TierStrong),
	rated("gpt-5-6-terra", TierBalanced),
	rated("gpt-5-6-luna", TierBalanced),
	rated("gpt-5-6-sol", TierBalanced),
	rated("gpt-5-5", TierBalanced),
	rated("gpt-5-4", TierBalanced),
	rated("gpt-5-3-codex", TierBalanced),
	rated("gpt-5-2-codex", TierBalanced),
	rated("gpt-5-2", TierBalanced),
	rated("gpt-5-1-codex-max", TierBalanced),
	rated("gpt-5-1", TierBalanced),
	rated("gpt-5-4-mini", TierFast),
	rated("gpt-5-mini", TierFast),
	rated("gpt-4-1", TierFast),

	rated("gemini-3-1-pro-preview", TierStrong),
	rated("gemini-3-pro-preview", TierStrong),
	rated("gemini-2-5-pro", TierStrong),
	rated("gemini-3-8-flash", TierBalanced),
	rated("gemini-3-7-flash", TierBalanced),
	rated("gemini-3-6-flash", TierBalanced),
	rated("gemini-3-5-flash", TierBalanced),
	rated("gemini-3-flash-preview", TierBalanced),
	rated("gemini-2-5-flash", TierBalanced),
	rated("gemini-3-5-flash-lite", TierFast),
	rated("gemini-3-1-flash-lite", TierFast),
	rated("gemini-2-5-flash-lite", TierFast),

	rated("deepseek-v4-pro", TierStrong),
	rated("deepseek-flash", TierFast),
	rated("grok-4-6", TierStrong),
	rated("grok-4-3", TierBalanced),
	rated("glm-5-2-maas", TierBalanced),
}

func rated(name, tier string) Model { return Model{Name: name, Tier: tier} }
