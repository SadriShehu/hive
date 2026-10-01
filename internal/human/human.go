package human

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func Tokens(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	v, unit := float64(n), ""
	for _, u := range []string{"k", "M", "B", "T"} {
		v /= 1000
		unit = u
		if v < 999.5 {
			break
		}
	}
	if v < 9.95 {
		return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0") + unit
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + unit
}

func USD(x float64) string {
	switch {
	case x < 1000:
		return fmt.Sprintf("$%.2f", x)
	case x < 1e6:
		return "$" + strings.TrimSuffix(strconv.FormatFloat(x/1000, 'f', 1, 64), ".0") + "k"
	}
	return "$" + strings.TrimSuffix(strconv.FormatFloat(x/1e6, 'f', 1, 64), ".0") + "M"
}

func Span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}
