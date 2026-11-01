package store

import "testing"

func TestQuotasRoundTrip(t *testing.T) {
	st := teamStore(t)
	if quotas, err := st.Quotas(); err != nil || len(quotas) != 0 {
		t.Fatalf("a fresh store has quotas %v, %v", quotas, err)
	}
	must(t, st.SetQuota(Quota{Tool: "claude", FiveUsed: 7, FiveReset: 1_000, SevenUsed: 22, SevenReset: 2_000, At: 5}))
	must(t, st.SetQuota(Quota{Tool: "codex", SevenUsed: 50, At: 6}))

	quotas, err := st.Quotas()
	if err != nil {
		t.Fatal(err)
	}
	if got := quotas["claude"]; got.FiveUsed != 7 || got.FiveReset != 1_000 || got.SevenUsed != 22 ||
		got.SevenReset != 2_000 || got.At != 5 {
		t.Errorf("claude quota = %+v", got)
	}
	if got := quotas["codex"]; got.SevenUsed != 50 || got.FiveUsed != 0 {
		t.Errorf("codex quota = %+v", got)
	}

	// A new report replaces the whole row, so a window it leaves out goes.
	must(t, st.SetQuota(Quota{Tool: "claude", FiveUsed: 9, FiveReset: 3_000, At: 7}))
	quotas, _ = st.Quotas()
	if got := quotas["claude"]; got.FiveUsed != 9 || got.SevenUsed != 0 || got.At != 7 {
		t.Errorf("replaced claude quota = %+v", got)
	}
	if got := quotas["claude"]; got.Empty() {
		t.Error("a quota with a five-hour window reads as empty")
	}
}
