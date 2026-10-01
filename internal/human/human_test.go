package human

import (
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0", 980: "980", 1000: "1k", 1234: "1.2k", 9949: "9.9k", 9951: "10k", 12_345: "12k",
		980_000: "980k", 999_950: "1M", 1_200_000: "1.2M", 48_000_000: "48M", 2_500_000_000: "2.5B",
	} {
		if got := Tokens(n); got != want {
			t.Errorf("Tokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestUSD(t *testing.T) {
	for x, want := range map[float64]string{0.004: "$0.00", 0.42: "$0.42", 42.63: "$42.63", 1234: "$1.2k", 2_000_000: "$2M"} {
		if got := USD(x); got != want {
			t.Errorf("USD(%v) = %q, want %q", x, got, want)
		}
	}
}

func TestSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{
		32 * time.Second: "32s", 48 * time.Minute: "48m", 2*time.Hour + 13*time.Minute: "2h13m", 3 * time.Hour: "3h", 5*time.Hour + 2*time.Minute: "5h02m",
	} {
		if got := Span(d); got != want {
			t.Errorf("Span(%v) = %q, want %q", d, got, want)
		}
	}
}
